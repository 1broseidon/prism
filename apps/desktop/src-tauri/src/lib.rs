//! Prism desktop tray app: hosts `prism-core`, tray panel, and Tauri commands.

use std::path::PathBuf;
use std::sync::atomic::{AtomicBool, AtomicU64, Ordering};
use std::sync::{Arc, Mutex};
use std::time::{SystemTime, UNIX_EPOCH};

use prism_core::{
    AgentView, Decision, Gateway, GatewayEvent, PanelAnchor, PendingCall, PrismConfig, Rule,
    ServerConfig, ServerView,
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
use tauri_plugin_positioner::{Position, WindowExt};
use tracing::{error, warn};

const TRAY_ID: &str = "prism-tray";
const PANEL_LABEL: &str = "panel";

struct AppState {
    gateway: Arc<Gateway>,
}

static LAST_SHOW_MS: AtomicU64 = AtomicU64::new(0);
static IGNORE_FOCUS_LOSS: AtomicBool = AtomicBool::new(false);
/// Set once the panel has actually received focus since it was shown; blur only hides after that.
static SEEN_FOCUS: AtomicBool = AtomicBool::new(false);
/// Where the cursor was the last time the user opened the panel from the tray. On Linux the tray
/// never reports its position, but the cursor on the tray menu is right next to the icon.
static TRAY_HINT: Mutex<Option<PhysicalPosition<f64>>> = Mutex::new(None);

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
/// events, so the positioner plugin can anchor to it. Linux trays (StatusNotifierItem) never
/// report a position, so there the panel anchors to the corner of the screen edge that holds
/// the desktop panel, inferred from the monitor's reserved work area.
fn position_panel(app: &AppHandle, window: &tauri::WebviewWindow) {
    let anchor = app
        .try_state::<AppState>()
        .map(|s| s.gateway.panel_anchor())
        .unwrap_or_default();

    if anchor == PanelAnchor::Auto
        && !cfg!(target_os = "linux")
        && (window.move_window(Position::TrayBottomCenter).is_ok()
            || window.move_window(Position::TrayCenter).is_ok())
    {
        return;
    }

    if anchor == PanelAnchor::Auto {
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

fn show_panel(app: &AppHandle) {
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

fn idle_icon() -> Result<Image<'static>, tauri::Error> {
    Image::from_bytes(include_bytes!("../icons/tray-idle.png"))
}

fn pending_icon() -> Result<Image<'static>, tauri::Error> {
    Image::from_bytes(include_bytes!("../icons/tray-pending.png"))
}

fn set_tray_icon(app: &AppHandle, pending: bool) {
    if let Some(tray) = app.tray_by_id(TRAY_ID) {
        let icon = if pending { pending_icon() } else { idle_icon() };
        if let Ok(icon) = icon {
            let _ = tray.set_icon(Some(icon));
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

#[derive(Debug, serde::Deserialize)]
struct AddServerArgs {
    name: String,
    command: String,
    args: Vec<String>,
    env: std::collections::BTreeMap<String, String>,
}

#[tauri::command]
async fn add_server(
    state: State<'_, AppState>,
    args: AddServerArgs,
) -> Result<ServerConfig, String> {
    let server = ServerConfig {
        id: String::new(),
        name: args.name,
        command: args.command,
        args: args.args,
        env: args.env,
        enabled: true,
    };
    state.gateway.add_server(server).await.map_err(map_err)
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
        GatewayEvent::CallDecided { .. }
        | GatewayEvent::Audit(_)
        | GatewayEvent::AgentDecided { .. } => {
            let status = gateway.status().await;
            if status.pending_count == 0 && status.pending_agents == 0 {
                set_tray_icon(app, false);
            }
        }
        _ => {}
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
    let icon = idle_icon()?;

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
            tauri_plugin_positioner::on_tray_event(tray.app_handle(), &event);
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
                .unwrap_or_else(|_| tracing_subscriber::EnvFilter::new("info")),
        )
        .try_init();

    let builder = tauri::Builder::default()
        .plugin(tauri_plugin_positioner::init())
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
                    .inner_size(400.0, 600.0)
                    .decorations(false)
                    .transparent(true)
                    .always_on_top(true)
                    .skip_taskbar(true)
                    .visible(false)
                    .resizable(false)
                    .build()?;
            }

            if let Some(window) = app.get_webview_window(PANEL_LABEL) {
                let _ = window.set_size(LogicalSize::new(400.0, 600.0));
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
            decide_agent,
            remove_agent,
            list_pending,
            decide,
            list_rules,
            delete_rule,
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
