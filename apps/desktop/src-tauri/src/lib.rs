//! Prism desktop tray app: hosts `prism-core`, tray panel, and Tauri commands.

use std::path::PathBuf;
use std::sync::atomic::{AtomicBool, AtomicU64, Ordering};
use std::sync::{Arc, Mutex};
use std::time::{SystemTime, UNIX_EPOCH};

use prism_core::{
    AgentConfig, AgentView, Attention, Decision, Gateway, GatewayEvent, NewRule, PanelAnchor,
    PendingCall, Posture, PrismConfig, Rule, ServerConfig, ServerView, Settings, ToolInfo,
};
use serde::Serialize;
use tauri::image::Image;
use tauri::menu::{Menu, MenuItem};
use tauri::tray::{MouseButton, MouseButtonState, TrayIconBuilder, TrayIconEvent};
use tauri::{
    AppHandle, Emitter, LogicalSize, Manager, PhysicalPosition, RunEvent, State, WebviewUrl,
    WebviewWindowBuilder, WindowEvent,
};
use tauri_plugin_global_shortcut::{Code, GlobalShortcutExt, Modifiers, Shortcut, ShortcutState};
use tauri_plugin_notification::NotificationExt;
use tracing::{error, warn};

const TRAY_ID: &str = "prism-tray";
const PANEL_LABEL: &str = "panel";
/// Window size in logical pixels: a 400x600 panel plus a 16px gutter on every side so the CSS shadow can fade
/// out inside the transparent window instead of being clipped square at its edge. Mirrors tauri.conf.json.
const PANEL_SIZE: (f64, f64) = (432.0, 632.0);

struct AppState {
    gateway: Arc<Gateway>,
}

static LAST_SHOW_MS: AtomicU64 = AtomicU64::new(0);
static IGNORE_FOCUS_LOSS: AtomicBool = AtomicBool::new(false);
/// Set once the panel has actually received focus since it was shown; blur only hides after that.
static SEEN_FOCUS: AtomicBool = AtomicBool::new(false);
/// Calls resolved without a human that asked for a badge, not yet seen. Cleared when the panel opens.
static UNSEEN: AtomicU64 = AtomicU64::new(0);
/// Last cursor position when opening from the tray, reused for later auto-opens.
static TRAY_HINT: Mutex<Option<PhysicalPosition<f64>>> = Mutex::new(None);
/// The tray icon's rectangle from the last tray event, on the platforms that report it (macOS and
/// Windows). Physical pixels. Linux tray events carry no usable rect.
static TRAY_RECT: Mutex<Option<(PhysicalPosition<i32>, tauri::PhysicalSize<u32>)>> =
    Mutex::new(None);

#[derive(Clone, Serialize)]
struct ConnectSnippetDto {
    url: String,
    mcp_json: String,
}

fn now_ms() -> u64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|d| d.as_millis() as u64)
        .unwrap_or(0)
}

fn panel_window(app: &AppHandle) -> Option<tauri::WebviewWindow> {
    app.get_webview_window(PANEL_LABEL)
}

/// Place the panel next to the tray. macOS and Windows report the icon's rect through tray
/// events, so the panel anchors to it: below a top bar, above a bottom taskbar, clamped to the
/// work area so it never covers the bar or leaves the screen. Linux uses the cursor position when
/// opening from the tray menu, falling back to the desktop panel's reserved work area.
fn position_panel(app: &AppHandle, window: &tauri::WebviewWindow) {
    let anchor = app
        .try_state::<AppState>()
        .map(|s| s.gateway.panel_anchor())
        .unwrap_or_default();

    if anchor == PanelAnchor::Auto {
        let rect = TRAY_RECT.lock().ok().and_then(|r| *r);
        if let Some((tray_pos, tray_size)) = rect {
            match position_by_tray_rect(app, window, tray_pos, tray_size) {
                Ok(true) => return,
                Ok(false) => {}
                Err(err) => warn!(%err, "tray-anchored positioning failed"),
            }
        }
        let hint = TRAY_HINT.lock().ok().and_then(|h| *h);
        if let Some(point) = hint {
            match position_by_cursor(app, window, point) {
                Ok(true) => return,
                Ok(false) => {}
                Err(err) => warn!(%err, "cursor-anchored positioning failed"),
            }
        }
    }

    if let Err(err) = position_by_work_area(window, anchor) {
        warn!(%err, "could not position panel; leaving it where the window manager put it");
    }
}

/// Anchor the panel to where the user just clicked. A tray on a top bar puts the cursor near the
/// top of the monitor, so the panel hangs below it; a bottom bar makes it sit above. Horizontally
/// the panel centres on the cursor and clamps to the monitor.
fn position_by_cursor(
    app: &AppHandle,
    window: &tauri::WebviewWindow,
    point: PhysicalPosition<f64>,
) -> tauri::Result<bool> {
    let monitor = match app.monitor_from_point(point.x, point.y)? {
        Some(m) => m,
        None => return Ok(false),
    };
    let pos = *monitor.position();
    let size = *monitor.size();
    let win = window.outer_size()?;
    let margin = (8.0 * monitor.scale_factor()).round() as i32;
    let (px, py) = (point.x.round() as i32, point.y.round() as i32);

    let left = pos.x + margin;
    let right = pos.x + size.width as i32 - win.width as i32 - margin;
    let top = pos.y + margin;
    let bottom = pos.y + size.height as i32 - win.height as i32 - margin;

    let x = (px - win.width as i32 / 2).clamp(left.min(right), right.max(left));
    let near_top = py < pos.y + size.height as i32 / 2;
    let y = if near_top {
        py + margin
    } else {
        py - win.height as i32 - margin
    };
    let y = y.clamp(top.min(bottom), bottom.max(top));
    window.set_position(PhysicalPosition::new(x, y))?;
    Ok(true)
}

/// Anchor the panel to the tray icon itself. An icon in the top half of its monitor means a top
/// bar, so the panel hangs below the work area's top edge; an icon in the bottom half means a
/// taskbar, so the panel sits on the work area's bottom edge. Horizontally it centres on the icon.
/// Every edge clamps to the work area, so a taskbar on any side is never covered.
fn position_by_tray_rect(
    app: &AppHandle,
    window: &tauri::WebviewWindow,
    tray_pos: PhysicalPosition<i32>,
    tray_size: tauri::PhysicalSize<u32>,
) -> tauri::Result<bool> {
    let centre_x = tray_pos.x as f64 + tray_size.width as f64 / 2.0;
    let centre_y = tray_pos.y as f64 + tray_size.height as f64 / 2.0;
    let monitor = match app.monitor_from_point(centre_x, centre_y)? {
        Some(m) => m,
        None => return Ok(false),
    };
    let pos = *monitor.position();
    let size = *monitor.size();
    let work = monitor.work_area();
    let win = window.outer_size()?;
    let margin = (8.0 * monitor.scale_factor()).round() as i32;

    let work_left = work.position.x + margin;
    let work_top = work.position.y + margin;
    let work_right = work.position.x + work.size.width as i32 - win.width as i32 - margin;
    let work_bottom = work.position.y + work.size.height as i32 - win.height as i32 - margin;

    let x = (centre_x.round() as i32 - win.width as i32 / 2)
        .clamp(work_left.min(work_right), work_right.max(work_left));
    let icon_in_top_half = centre_y < pos.y as f64 + size.height as f64 / 2.0;
    let y = if icon_in_top_half {
        // Below the icon, or the work area's top if the bar is reserved.
        (tray_pos.y + tray_size.height as i32 + margin).max(work_top)
    } else {
        // Above the icon, or the work area's bottom if the taskbar is reserved.
        (tray_pos.y - win.height as i32 - margin).min(work_bottom)
    };
    let y = y.clamp(work_top.min(work_bottom), work_bottom.max(work_top));
    window.set_position(PhysicalPosition::new(x, y))?;
    Ok(true)
}

fn position_by_work_area(window: &tauri::WebviewWindow, anchor: PanelAnchor) -> tauri::Result<()> {
    let monitor = match window.current_monitor()? {
        Some(m) => m,
        None => match window.primary_monitor()? {
            Some(m) => m,
            None => return Ok(()),
        },
    };
    let screen_pos = *monitor.position();
    let screen = *monitor.size();
    let work = monitor.work_area();
    let win = window.outer_size()?;
    let margin = (8.0 * monitor.scale_factor()).round() as i32;

    let work_left = work.position.x;
    let work_top = work.position.y;
    let work_right = work_left + work.size.width as i32;
    let work_bottom = work_top + work.size.height as i32;

    // Struts: how much each edge of the screen a desktop panel has reserved.
    let strut_top = work_top - screen_pos.y;
    let strut_bottom = (screen_pos.y + screen.height as i32) - work_bottom;
    let strut_left = work_left - screen_pos.x;
    let strut_right = (screen_pos.x + screen.width as i32) - work_right;

    let (at_bottom, at_left) = match anchor {
        PanelAnchor::TopRight => (false, false),
        PanelAnchor::TopLeft => (false, true),
        PanelAnchor::BottomRight => (true, false),
        PanelAnchor::BottomLeft => (true, true),
        PanelAnchor::Auto => {
            // A vertical panel on the left (dock-style) is the only case that pulls us left;
            // otherwise trays live at the right end of a top or bottom bar.
            let vertical_left = strut_left > 0
                && strut_left >= strut_right
                && strut_left > strut_top.max(strut_bottom);
            (strut_bottom > strut_top, vertical_left)
        }
    };

    let x = if at_left {
        work_left + margin
    } else {
        work_right - win.width as i32 - margin
    };
    let y = if at_bottom {
        work_bottom - win.height as i32 - margin
    } else {
        work_top + margin
    };
    window.set_position(PhysicalPosition::new(x, y))
}

fn remember_tray_hint(app: &AppHandle) {
    if let Ok(pos) = app.cursor_position() {
        if let Ok(mut hint) = TRAY_HINT.lock() {
            *hint = Some(pos);
        }
    }
}

/// Keep the icon's rectangle from a tray event in physical pixels. The rect arrives logical on
/// macOS and physical on Windows; the monitor under the cursor supplies the scale either way.
fn remember_tray_rect(app: &AppHandle, rect: &tauri::Rect) {
    let scale = app
        .cursor_position()
        .ok()
        .and_then(|p| app.monitor_from_point(p.x, p.y).ok().flatten())
        .map(|m| m.scale_factor())
        .unwrap_or(1.0);
    let pos = rect.position.to_physical::<i32>(scale);
    let size = rect.size.to_physical::<u32>(scale);
    if size.width == 0 && size.height == 0 {
        return;
    }
    if let Ok(mut slot) = TRAY_RECT.lock() {
        *slot = Some((pos, size));
    }
}

fn show_panel(app: &AppHandle) {
    // Opening the panel is how the operator sees badged calls, so the badge clears here.
    if UNSEEN.swap(0, Ordering::SeqCst) > 0 {
        if let Some(state) = app.try_state::<AppState>() {
            let gateway = state.gateway.clone();
            let app = app.clone();
            tauri::async_runtime::spawn(async move {
                settle_tray_icon(&app, &gateway).await;
            });
        }
    }
    if let Some(window) = panel_window(app) {
        IGNORE_FOCUS_LOSS.store(true, Ordering::SeqCst);
        SEEN_FOCUS.store(false, Ordering::SeqCst);
        LAST_SHOW_MS.store(now_ms(), Ordering::SeqCst);
        position_panel(app, &window);
        let _ = window.show();
        let _ = window.set_focus();
        let app = app.clone();
        tauri::async_runtime::spawn(async move {
            tokio::time::sleep(std::time::Duration::from_millis(250)).await;
            IGNORE_FOCUS_LOSS.store(false, Ordering::SeqCst);
            let _ = app;
        });
    }
}

fn hide_panel_window(app: &AppHandle) {
    if let Some(window) = panel_window(app) {
        let _ = window.hide();
    }
}

fn toggle_panel(app: &AppHandle) {
    if let Some(window) = panel_window(app) {
        match window.is_visible() {
            Ok(true) => {
                let _ = window.hide();
            }
            _ => show_panel(app),
        }
    }
}

/// Pale glyph for dark bars. macOS ignores the colour (template), GNOME and KDE bars are dark,
/// and Windows gets the ink variant when its taskbar is light.
fn idle_icon(app: &AppHandle) -> Result<Image<'static>, tauri::Error> {
    #[cfg(target_os = "windows")]
    {
        let light = app
            .get_webview_window("panel")
            .and_then(|w| w.theme().ok())
            .map(|t| t == tauri::Theme::Light)
            .unwrap_or(false);
        if light {
            return Image::from_bytes(include_bytes!("../icons/tray-idle-ink.png"));
        }
    }
    #[cfg(not(target_os = "windows"))]
    let _ = app;
    Image::from_bytes(include_bytes!("../icons/tray-idle.png"))
}

fn pending_icon() -> Result<Image<'static>, tauri::Error> {
    Image::from_bytes(include_bytes!("../icons/tray-pending.png"))
}

fn set_tray_icon(app: &AppHandle, pending: bool) {
    if let Some(tray) = app.tray_by_id(TRAY_ID) {
        let icon = if pending {
            pending_icon()
        } else {
            idle_icon(app)
        };
        if let Ok(icon) = icon {
            // Idle is a template on macOS so the menu bar tints it; pending keeps its amber.
            let _ = tray.set_icon_with_as_template(Some(icon), !pending);
        }
    }
}

fn map_err(err: prism_core::Error) -> String {
    err.to_string()
}

#[tauri::command]
async fn get_status(state: State<'_, AppState>) -> Result<prism_core::GatewayStatus, String> {
    Ok(state.gateway.status().await)
}

#[tauri::command]
async fn list_servers(state: State<'_, AppState>) -> Result<Vec<ServerView>, String> {
    Ok(state.gateway.servers().await)
}

#[derive(serde::Deserialize)]
struct AddServerArgs {
    name: String,
    command: String,
    args: Vec<String>,
    env: std::collections::BTreeMap<String, String>,
}

#[tauri::command]
async fn add_server(state: State<'_, AppState>, args: AddServerArgs) -> Result<ServerView, String> {
    let server = ServerConfig {
        id: String::new(),
        name: args.name,
        command: args.command,
        args: args.args,
        env: args.env,
        enabled: true,
        credential_ref: None,
    };
    let added = state.gateway.add_server(server).await.map_err(map_err)?;
    state
        .gateway
        .servers()
        .await
        .into_iter()
        .find(|server| server.id == added.id)
        .ok_or_else(|| "server is no longer configured".to_string())
}

#[tauri::command]
async fn remove_server(state: State<'_, AppState>, server_id: String) -> Result<(), String> {
    state
        .gateway
        .remove_server(&server_id)
        .await
        .map_err(map_err)
}

#[tauri::command]
async fn restart_server(state: State<'_, AppState>, server_id: String) -> Result<(), String> {
    state
        .gateway
        .restart_server(&server_id)
        .await
        .map_err(map_err)
}

#[tauri::command]
async fn list_agents(state: State<'_, AppState>) -> Result<Vec<AgentView>, String> {
    Ok(state.gateway.agents().await)
}

#[tauri::command]
async fn create_manual_agent(
    state: State<'_, AppState>,
    name: String,
) -> Result<prism_core::ManualToken, String> {
    state
        .gateway
        .create_manual_agent(&name)
        .await
        .map_err(map_err)
}

#[tauri::command]
async fn replace_manual_token(
    state: State<'_, AppState>,
    agent_id: String,
) -> Result<prism_core::ManualToken, String> {
    state
        .gateway
        .replace_manual_token(&agent_id)
        .await
        .map_err(map_err)
}

#[tauri::command]
async fn decide_agent(
    state: State<'_, AppState>,
    agent_id: String,
    approve: bool,
) -> Result<(), String> {
    state
        .gateway
        .decide_agent(&agent_id, approve)
        .await
        .map_err(map_err)
}

#[tauri::command]
async fn remove_agent(state: State<'_, AppState>, agent_id: String) -> Result<(), String> {
    state.gateway.remove_agent(&agent_id).await.map_err(map_err)
}

#[tauri::command]
async fn list_signins(
    state: State<'_, AppState>,
) -> Result<Vec<prism_core::PendingSignIn>, String> {
    Ok(state.gateway.pending_signins())
}

#[tauri::command]
async fn decide_signin(
    state: State<'_, AppState>,
    id: String,
    approve: bool,
) -> Result<(), String> {
    state.gateway.decide_signin(&id, approve).map_err(map_err)
}

#[tauri::command]
async fn revoke_agent_tokens(state: State<'_, AppState>, agent_id: String) -> Result<(), String> {
    state
        .gateway
        .revoke_agent_tokens(&agent_id)
        .await
        .map_err(map_err)
}

#[tauri::command]
async fn list_pending(state: State<'_, AppState>) -> Result<Vec<PendingCall>, String> {
    Ok(state.gateway.pending().await)
}

#[tauri::command]
async fn decide(state: State<'_, AppState>, id: String, decision: Decision) -> Result<(), String> {
    state.gateway.decide(&id, decision).await.map_err(map_err)
}

#[tauri::command]
async fn list_rules(state: State<'_, AppState>) -> Result<Vec<Rule>, String> {
    Ok(state.gateway.rules().await)
}

#[tauri::command]
async fn delete_rule(state: State<'_, AppState>, rule_id: String) -> Result<(), String> {
    state.gateway.delete_rule(&rule_id).await.map_err(map_err)
}

#[tauri::command]
async fn add_rule(state: State<'_, AppState>, rule: NewRule) -> Result<Rule, String> {
    state.gateway.add_rule(rule).await.map_err(map_err)
}

#[tauri::command]
async fn set_agent_policy(
    state: State<'_, AppState>,
    agent_id: String,
    posture: Option<Posture>,
    attention: Option<Attention>,
) -> Result<AgentConfig, String> {
    state
        .gateway
        .set_agent_policy(&agent_id, posture, attention)
        .await
        .map_err(map_err)
}

#[tauri::command]
async fn get_settings(state: State<'_, AppState>) -> Result<Settings, String> {
    Ok(state.gateway.settings().await)
}

#[tauri::command]
async fn set_settings(state: State<'_, AppState>, settings: Settings) -> Result<(), String> {
    state.gateway.set_settings(settings).await.map_err(map_err)
}

#[tauri::command]
async fn list_server_tools(
    state: State<'_, AppState>,
    server_id: String,
) -> Result<Vec<ToolInfo>, String> {
    Ok(state.gateway.server_tools(&server_id).await)
}

#[tauri::command]
async fn list_audit(
    state: State<'_, AppState>,
    limit: Option<usize>,
) -> Result<Vec<prism_core::AuditEntry>, String> {
    Ok(state.gateway.audit(limit.unwrap_or(20)).await)
}

#[tauri::command]
fn hide_panel(app: AppHandle) -> Result<(), String> {
    hide_panel_window(&app);
    Ok(())
}

#[tauri::command]
async fn get_connect_snippet(state: State<'_, AppState>) -> Result<ConnectSnippetDto, String> {
    let snippet = state.gateway.connect_snippet().map_err(map_err)?;
    Ok(ConnectSnippetDto {
        url: snippet.url,
        mcp_json: snippet.mcp_json,
    })
}

fn config_paths(app: &AppHandle) -> Result<(PathBuf, PathBuf), String> {
    let config_dir = app.path().app_config_dir().map_err(|err| err.to_string())?;
    let data_dir = app.path().app_data_dir().map_err(|err| err.to_string())?;
    std::fs::create_dir_all(&config_dir).map_err(|err| err.to_string())?;
    std::fs::create_dir_all(&data_dir).map_err(|err| err.to_string())?;
    Ok((config_dir.join("prism.json"), data_dir.join("audit.jsonl")))
}

fn ensure_auto_open_default(path: &PathBuf) {
    if !path.exists() {
        let config = PrismConfig::default();
        if let Err(err) = config.save(path) {
            warn!(%err, "failed to write default prism.json");
        }
    }
}

fn forward_events(app: AppHandle, gateway: Arc<Gateway>) {
    tauri::async_runtime::spawn(async move {
        let mut rx = gateway.subscribe();
        loop {
            match rx.recv().await {
                Ok(event) => {
                    handle_gateway_event(&app, &gateway, &event).await;
                    if let Err(err) = app.emit("prism://event", &event) {
                        warn!(%err, "failed to emit prism://event");
                    }
                }
                Err(tokio::sync::broadcast::error::RecvError::Lagged(n)) => {
                    warn!(n, "gateway event subscriber lagged");
                }
                Err(tokio::sync::broadcast::error::RecvError::Closed) => break,
            }
        }
    });
}

async fn handle_gateway_event(app: &AppHandle, gateway: &Gateway, event: &GatewayEvent) {
    match event {
        GatewayEvent::PendingCall(call) => {
            let body = format!(
                "{} wants to call {}/{}",
                call.agent_name, call.server_name, call.tool
            );
            attention(app, gateway, &body).await;
        }
        GatewayEvent::AgentRequested(agent) => {
            let body = format!("{} wants to connect to Prism", agent.name);
            attention(app, gateway, &body).await;
        }
        GatewayEvent::SignInRequested(signin) => {
            let body = format!("{} wants to sign in again", signin.agent_name);
            attention(app, gateway, &body).await;
        }
        // A call resolved without a human. The rule or agent says how loudly to surface it.
        GatewayEvent::Audit(entry) if entry.attention != Attention::Silent => {
            UNSEEN.fetch_add(1, Ordering::SeqCst);
            set_tray_icon(app, true);
            if entry.attention >= Attention::Notify {
                let outcome = match entry.verdict {
                    prism_core::AuditVerdict::Allowed => "allowed",
                    prism_core::AuditVerdict::Denied => "denied",
                    prism_core::AuditVerdict::Timeout => "timed out",
                    prism_core::AuditVerdict::Error => "failed",
                };
                let body = format!("{} · {} {}", entry.agent_name, entry.tool, outcome);
                if let Err(err) = app
                    .notification()
                    .builder()
                    .title("Prism")
                    .body(body)
                    .show()
                {
                    warn!(%err, "notification failed");
                }
            }
            if entry.attention == Attention::Open {
                show_panel(app);
            }
        }
        GatewayEvent::CallDecided { .. }
        | GatewayEvent::Audit(_)
        | GatewayEvent::AgentDecided { .. }
        | GatewayEvent::SignInDecided { .. } => {
            settle_tray_icon(app, gateway).await;
        }
        _ => {}
    }
}

/// Idle icon once nothing is waiting and nothing badged is unseen.
async fn settle_tray_icon(app: &AppHandle, gateway: &Gateway) {
    let status = gateway.status().await;
    if status.pending_count == 0
        && status.pending_agents == 0
        && status.pending_signins == 0
        && UNSEEN.load(Ordering::SeqCst) == 0
    {
        set_tray_icon(app, false);
    }
}

/// Something needs a human: flip the tray icon, notify, and open the panel if configured.
async fn attention(app: &AppHandle, gateway: &Gateway, body: &str) {
    set_tray_icon(app, true);
    if let Err(err) = app
        .notification()
        .builder()
        .title("Prism")
        .body(body)
        .show()
    {
        warn!(%err, "notification failed");
    }
    if gateway.status().await.auto_open_on_pending {
        show_panel(app);
    }
}

fn build_tray(app: &AppHandle) -> Result<(), Box<dyn std::error::Error>> {
    let open = MenuItem::with_id(app, "open", "Open Prism", true, None::<&str>)?;
    let quit = MenuItem::with_id(app, "quit", "Quit", true, None::<&str>)?;
    let menu = Menu::with_items(app, &[&open, &quit])?;
    let icon = idle_icon(app)?;

    #[allow(unused_mut)]
    let mut builder = TrayIconBuilder::with_id(TRAY_ID)
        .icon(icon)
        .menu(&menu)
        .show_menu_on_left_click(false)
        .tooltip("Prism")
        .on_menu_event(|app, event| match event.id.as_ref() {
            "open" => {
                remember_tray_hint(app);
                show_panel(app);
            }
            "quit" => {
                app.exit(0);
            }
            _ => {}
        })
        .on_tray_icon_event(|tray, event| {
            match &event {
                TrayIconEvent::Click { rect, .. }
                | TrayIconEvent::Enter { rect, .. }
                | TrayIconEvent::Move { rect, .. } => remember_tray_rect(tray.app_handle(), rect),
                _ => {}
            }
            if let TrayIconEvent::Click {
                button: MouseButton::Left,
                button_state: MouseButtonState::Up,
                ..
            } = event
            {
                remember_tray_hint(tray.app_handle());
                toggle_panel(tray.app_handle());
            }
        });

    #[cfg(target_os = "macos")]
    {
        builder = builder.icon_as_template(true);
    }

    builder.build(app)?;
    Ok(())
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    let _ = tracing_subscriber::fmt()
        .with_env_filter(
            tracing_subscriber::EnvFilter::try_from_default_env()
                .unwrap_or_else(|_| tracing_subscriber::EnvFilter::new("info"))
                .add_directive("rmcp=off".parse().expect("valid log directive")),
        )
        .try_init();

    let builder = tauri::Builder::default()
        .plugin(tauri_plugin_notification::init())
        .plugin(tauri_plugin_global_shortcut::Builder::new().build())
        .setup(|app| {
            #[cfg(target_os = "macos")]
            {
                app.set_activation_policy(tauri::ActivationPolicy::Accessory);
            }

            if app.get_webview_window(PANEL_LABEL).is_none() {
                WebviewWindowBuilder::new(app, PANEL_LABEL, WebviewUrl::App("index.html".into()))
                    .title("Prism")
                    .inner_size(PANEL_SIZE.0, PANEL_SIZE.1)
                    .decorations(false)
                    .transparent(true)
                    .always_on_top(true)
                    .skip_taskbar(true)
                    .visible(false)
                    .resizable(false)
                    .build()?;
            }

            if let Some(window) = app.get_webview_window(PANEL_LABEL) {
                let _ = window.set_size(LogicalSize::new(PANEL_SIZE.0, PANEL_SIZE.1));
            }

            let (config_path, audit_path) = config_paths(app.handle())?;
            ensure_auto_open_default(&config_path);

            let gateway = tauri::async_runtime::block_on(Gateway::start(config_path, audit_path))
                .map_err(|err| {
                error!(%err, "failed to start gateway");
                err
            })?;

            app.manage(AppState {
                gateway: gateway.clone(),
            });

            build_tray(app.handle())?;
            forward_events(app.handle().clone(), gateway);

            // Dev affordance: `PRISM_SHOW_PANEL=1 cargo tauri dev` opens the panel without a tray click.
            if std::env::var_os("PRISM_SHOW_PANEL").is_some() {
                let handle = app.handle().clone();
                tauri::async_runtime::spawn(async move {
                    tokio::time::sleep(std::time::Duration::from_millis(800)).await;
                    remember_tray_hint(&handle);
                    show_panel(&handle);
                });
            }

            let shortcut = if cfg!(target_os = "macos") {
                Shortcut::new(Some(Modifiers::SUPER.union(Modifiers::SHIFT)), Code::Space)
            } else {
                Shortcut::new(
                    Some(Modifiers::CONTROL.union(Modifiers::SHIFT)),
                    Code::Space,
                )
            };
            if let Err(err) = app
                .global_shortcut()
                .on_shortcut(shortcut, |app, _sc, event| {
                    if event.state == ShortcutState::Pressed {
                        toggle_panel(app);
                    }
                })
            {
                warn!(%err, "global shortcut unavailable; the tray icon still opens the panel");
            }

            Ok(())
        })
        .on_window_event(|window, event| {
            if window.label() != PANEL_LABEL {
                return;
            }
            match event {
                WindowEvent::Focused(true) => {
                    SEEN_FOCUS.store(true, Ordering::SeqCst);
                }
                WindowEvent::Focused(false) => {
                    if IGNORE_FOCUS_LOSS.load(Ordering::SeqCst)
                        || !SEEN_FOCUS.load(Ordering::SeqCst)
                    {
                        return;
                    }
                    let elapsed = now_ms().saturating_sub(LAST_SHOW_MS.load(Ordering::SeqCst));
                    if elapsed < 300 {
                        return;
                    }
                    let _ = window.hide();
                }
                WindowEvent::CloseRequested { api, .. } => {
                    api.prevent_close();
                    let _ = window.hide();
                }
                _ => {}
            }
        })
        .invoke_handler(tauri::generate_handler![
            get_status,
            list_servers,
            add_server,
            remove_server,
            restart_server,
            list_agents,
            create_manual_agent,
            replace_manual_token,
            decide_agent,
            remove_agent,
            revoke_agent_tokens,
            list_signins,
            decide_signin,
            list_pending,
            decide,
            list_rules,
            delete_rule,
            add_rule,
            set_agent_policy,
            get_settings,
            set_settings,
            list_server_tools,
            list_audit,
            hide_panel,
            get_connect_snippet,
        ]);

    let app = match builder.build(tauri::generate_context!()) {
        Ok(app) => app,
        Err(err) => {
            eprintln!("error while building Prism: {err}");
            std::process::exit(1);
        }
    };

    app.run(|app, event| {
        if let RunEvent::Exit = event {
            if let Some(state) = app.try_state::<AppState>() {
                let gateway = state.gateway.clone();
                tauri::async_runtime::block_on(gateway.shutdown());
            }
        }
    });
}
