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
use tauri_plugin_updater::UpdaterExt;
use tracing::{error, warn};

const TRAY_ID: &str = "prism-tray";
const PANEL_LABEL: &str = "panel";
/// Window size in logical pixels: a 400x600 panel plus a 16px gutter on every side so the CSS shadow can fade
/// out inside the transparent window instead of being clipped square at its edge. Mirrors tauri.conf.json.
const PANEL_SIZE: (f64, f64) = (432.0, 632.0);
/// The panel's global shortcut unless `panel_shortcut` says otherwise.
#[allow(non_snake_case)]
fn DEFAULT_SHORTCUT() -> Shortcut {
    Shortcut::new(Some(Modifiers::CONTROL.union(Modifiers::ALT)), Code::KeyP)
}

struct AppState {
    gateway: Arc<Gateway>,
}

/// What the panel needs to know about a newer release. `installable` is false on Linux outside an
/// AppImage, where the updater cannot replace a package-manager install; the panel links to the
/// release instead.
#[derive(Clone, Serialize)]
struct UpdateInfo {
    version: String,
    current: String,
    notes: Option<String>,
    date: Option<String>,
    installable: bool,
}

/// Progress of an update, for the panel. Emitted on `prism://update`.
#[derive(Clone, Serialize)]
#[serde(tag = "state", rename_all = "snake_case")]
enum UpdateEvent {
    Available(UpdateInfo),
    UpToDate,
    Downloading { downloaded: u64, total: Option<u64> },
    Installing,
    Error { message: String },
}

#[derive(Default)]
struct UpdateState {
    update: Mutex<Option<tauri_plugin_updater::Update>>,
    info: Mutex<Option<UpdateInfo>>,
    checked_at: Mutex<Option<String>>,
    busy: AtomicBool,
}

const UPDATE_EVENT: &str = "prism://update";
/// Startup delay before the first check, then the interval between checks.
const UPDATE_FIRST_CHECK: std::time::Duration = std::time::Duration::from_secs(20);
const UPDATE_INTERVAL: std::time::Duration = std::time::Duration::from_secs(6 * 60 * 60);

static LAST_SHOW_MS: AtomicU64 = AtomicU64::new(0);
static IGNORE_FOCUS_LOSS: AtomicBool = AtomicBool::new(false);
/// Set once the panel has actually received focus since it was shown; blur only hides after that.
static SEEN_FOCUS: AtomicBool = AtomicBool::new(false);
/// Calls resolved without a human that asked for a badge, not yet seen. Cleared when the panel opens.
static UNSEEN: AtomicU64 = AtomicU64::new(0);
/// Last cursor position when opening from the tray, reused for later auto-opens.
/// Where the cursor was when the tray was last used, and when. Fresh, it says where the panel
/// should open; stale, it still says which monitor the tray is on.
static TRAY_HINT: Mutex<Option<(PhysicalPosition<f64>, std::time::Instant)>> = Mutex::new(None);
/// How long a tray click counts as "the user just clicked here".
const HINT_FRESH_FOR: std::time::Duration = std::time::Duration::from_secs(2);
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
/// the tray was clicked within the last couple of seconds. Anything else (the keyboard shortcut,
/// auto-open on a pending call) lands in a fixed corner of the tray's monitor: the corner the
/// desktop panel's reserved area points at, top right when nothing is reserved, always inside
/// the work area.
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
        let hint = TRAY_HINT
            .lock()
            .ok()
            .and_then(|h| *h)
            .filter(|(_, at)| at.elapsed() < HINT_FRESH_FOR);
        if let Some((point, _)) = hint {
            match position_by_cursor(app, window, point) {
                Ok(true) => return,
                Ok(false) => {}
                Err(err) => warn!(%err, "cursor-anchored positioning failed"),
            }
        }
    }

    if let Err(err) = position_by_work_area(app, window, anchor) {
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
    let win = panel_size(window, monitor.scale_factor())?;
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
    let win = panel_size(window, monitor.scale_factor())?;
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

/// The monitor the tray lives on: under the icon's rect where the platform reports one, else
/// under the last tray click however old. The tray does not move, so an old click still names
/// the right screen.
fn tray_monitor(app: &AppHandle) -> tauri::Result<Option<tauri::Monitor>> {
    if let Some((pos, size)) = TRAY_RECT.lock().ok().and_then(|r| *r) {
        let cx = pos.x as f64 + size.width as f64 / 2.0;
        let cy = pos.y as f64 + size.height as f64 / 2.0;
        if let Some(monitor) = app.monitor_from_point(cx, cy)? {
            return Ok(Some(monitor));
        }
    }
    if let Some((point, _)) = TRAY_HINT.lock().ok().and_then(|h| *h) {
        return app.monitor_from_point(point.x, point.y);
    }
    Ok(None)
}

/// The panel's size in physical pixels. A window that has never been shown can report zero, so
/// the configured size stands in until then.
fn panel_size(
    window: &tauri::WebviewWindow,
    scale: f64,
) -> tauri::Result<tauri::PhysicalSize<u32>> {
    let size = window.outer_size()?;
    if size.width > 0 && size.height > 0 {
        return Ok(size);
    }
    Ok(tauri::LogicalSize::new(PANEL_SIZE.0, PANEL_SIZE.1).to_physical(scale))
}

/// A fixed corner of the tray's monitor, chosen from what the desktop has reserved: below a top
/// bar, above a bottom one, and top right when nothing is reserved. The user's `panel_anchor`
/// setting overrides the guess.
fn position_by_work_area(
    app: &AppHandle,
    window: &tauri::WebviewWindow,
    anchor: PanelAnchor,
) -> tauri::Result<()> {
    let monitor = match tray_monitor(app)? {
        Some(m) => m,
        None => match window.primary_monitor()? {
            Some(m) => m,
            None => match window.current_monitor()? {
                Some(m) => m,
                None => return Ok(()),
            },
        },
    };
    let screen_pos = *monitor.position();
    let screen = *monitor.size();
    let work = monitor.work_area();
    let win = panel_size(window, monitor.scale_factor())?;
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

    // The last tray click, on this monitor, when the desktop has reserved nothing: the bar is
    // there even if the work area does not say so. Half a tall bar keeps the panel clear of it.
    let bar_allowance = (24.0 * monitor.scale_factor()).round() as i32;
    let tray_click = TRAY_HINT
        .lock()
        .ok()
        .and_then(|h| *h)
        .map(|(p, _)| PhysicalPosition::new(p.x.round() as i32, p.y.round() as i32))
        .filter(|p| {
            p.x >= screen_pos.x
                && p.x < screen_pos.x + screen.width as i32
                && p.y >= screen_pos.y
                && p.y < screen_pos.y + screen.height as i32
        });
    let nothing_reserved =
        strut_top == 0 && strut_bottom == 0 && strut_left == 0 && strut_right == 0;

    let (at_bottom, at_left) = match anchor {
        PanelAnchor::TopRight => (false, false),
        PanelAnchor::TopLeft => (false, true),
        PanelAnchor::BottomRight => (true, false),
        PanelAnchor::BottomLeft => (true, true),
        PanelAnchor::Auto => match tray_click.filter(|_| nothing_reserved) {
            Some(p) => (p.y >= screen_pos.y + screen.height as i32 / 2, false),
            None => {
                // A vertical panel on the left (dock-style) is the only case that pulls us
                // left; otherwise trays live at the right end of a top or bottom bar.
                let vertical_left = strut_left > 0
                    && strut_left >= strut_right
                    && strut_left > strut_top.max(strut_bottom);
                (strut_bottom > strut_top, vertical_left)
            }
        },
    };

    let x = if at_left {
        work_left + margin
    } else {
        work_right - win.width as i32 - margin
    };
    let mut y = if at_bottom {
        work_bottom - win.height as i32 - margin
    } else {
        work_top + margin
    };
    if anchor == PanelAnchor::Auto && nothing_reserved {
        if let Some(p) = tray_click {
            y = if at_bottom {
                y.min(p.y - bar_allowance - win.height as i32 - margin)
            } else {
                y.max(p.y + bar_allowance + margin)
            };
        }
    }
    window.set_position(PhysicalPosition::new(x, y))
}

/// The tray was just used: the cursor is on it. Kept for this run and, since the tray does
/// not move, for the next one.
fn remember_tray_hint(app: &AppHandle) {
    if let Some(pos) = note_cursor_hint(app) {
        if let Some(path) = tray_hint_path(app) {
            let _ = std::fs::write(path, format!("{} {}\n", pos.x, pos.y));
        }
    }
}

/// Anchor the next show to the cursor without claiming the tray is there.
fn note_cursor_hint(app: &AppHandle) -> Option<PhysicalPosition<f64>> {
    let pos = app.cursor_position().ok()?;
    if let Ok(mut hint) = TRAY_HINT.lock() {
        *hint = Some((pos, std::time::Instant::now()));
    }
    Some(pos)
}

/// Where the last tray click was recorded between runs. Only a point, and only so the panel
/// knows which edge the bar is on before the tray has been clicked in this run.
fn tray_hint_path(app: &AppHandle) -> Option<PathBuf> {
    app.path().app_data_dir().ok().map(|d| d.join("tray-hint"))
}

/// Load the previous run's tray click as a stale hint: right monitor and edge, never "fresh".
fn recall_tray_hint(app: &AppHandle) {
    let Some(text) = tray_hint_path(app).and_then(|p| std::fs::read_to_string(p).ok()) else {
        return;
    };
    let mut parts = text
        .split_whitespace()
        .filter_map(|n| n.parse::<f64>().ok());
    if let (Some(x), Some(y)) = (parts.next(), parts.next()) {
        let long_ago = std::time::Instant::now()
            .checked_sub(HINT_FRESH_FOR * 2)
            .unwrap_or_else(std::time::Instant::now);
        if let Ok(mut hint) = TRAY_HINT.lock() {
            if hint.is_none() {
                *hint = Some((PhysicalPosition::new(x, y), long_ago));
            }
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
        // Some window managers place a window themselves when it is mapped and ignore the
        // position set while it was hidden, so it is set again now and once more after the map
        // has settled.
        position_panel(app, &window);
        let app = app.clone();
        tauri::async_runtime::spawn(async move {
            tokio::time::sleep(std::time::Duration::from_millis(80)).await;
            if let Some(window) = panel_window(&app) {
                position_panel(&app, &window);
            }
            tokio::time::sleep(std::time::Duration::from_millis(170)).await;
            IGNORE_FOCUS_LOSS.store(false, Ordering::SeqCst);
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
    #[serde(default)]
    command: String,
    #[serde(default)]
    args: Vec<String>,
    #[serde(default)]
    env: std::collections::BTreeMap<String, String>,
    /// Remote server endpoint. When set, `command` is ignored.
    #[serde(default)]
    url: Option<String>,
    #[serde(default)]
    auth: prism_core::HttpAuth,
    #[serde(default)]
    headers: std::collections::BTreeMap<String, String>,
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
        url: args.url,
        auth: args.auth,
        headers: args.headers,
        oauth_ref: None,
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

/// Start a browser sign-in for an OAuth server and open it. Returns the URL for the panel.
#[tauri::command]
async fn sign_in_server(
    app: AppHandle,
    state: State<'_, AppState>,
    server_id: String,
) -> Result<String, String> {
    use tauri_plugin_opener::OpenerExt;
    let url = state
        .gateway
        .sign_in_server(&server_id)
        .await
        .map_err(map_err)?;
    if let Err(err) = app.opener().open_url(&url, None::<&str>) {
        warn!(%err, "could not open the browser for a server sign-in");
    }
    Ok(url)
}

#[tauri::command]
async fn sign_out_server(state: State<'_, AppState>, server_id: String) -> Result<(), String> {
    state
        .gateway
        .sign_out_server(&server_id)
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
    agent_id: Option<String>,
    attention: Option<bool>,
    day: Option<String>,
    reason: Option<String>,
) -> Result<Vec<prism_core::AuditEntry>, String> {
    let limit = limit.unwrap_or(20);
    let attention = attention.unwrap_or(false);
    if agent_id.is_none() && !attention && day.is_none() && reason.is_none() {
        return Ok(state.gateway.audit(limit).await);
    }
    Ok(state
        .gateway
        .audit(usize::MAX)
        .await
        .into_iter()
        .filter(|entry| agent_id.as_deref().is_none_or(|id| entry.agent_id == id))
        .filter(|entry| !attention || prism_core::activity::needs_attention(entry))
        .filter(|entry| {
            day.as_deref().is_none_or(|day| {
                entry
                    .at
                    .with_timezone(&chrono::Local)
                    .format("%Y-%m-%d")
                    .to_string()
                    == day
            })
        })
        .filter(|entry| {
            reason.as_deref().is_none_or(|reason| {
                entry.native.as_ref().and_then(|n| n.would_hold.as_deref()) == Some(reason)
            })
        })
        .take(limit)
        .collect())
}

#[tauri::command]
async fn get_activity(
    state: State<'_, AppState>,
    days: Option<u32>,
) -> Result<prism_core::activity::ActivitySummary, String> {
    Ok(state.gateway.activity(days.unwrap_or(7)).await)
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

// ----- native actions (observe) ---------------------------------------------------------

/// Core status plus what only the desktop knows per host: where its hook file lives and
/// whether the current hook URL is in it.
#[derive(Serialize)]
struct NativeStatusDto {
    #[serde(flatten)]
    status: prism_core::NativeStatus,
    setup: Vec<HostSetupDto>,
}

#[derive(Serialize)]
struct HostSetupDto {
    host: String,
    settings_path: String,
    hook_installed: bool,
    /// Codex only: whether `~/.codex/config.toml` holds a trust entry for the group the Prism hook
    /// sits in. Codex reviews new and changed hooks in `/hooks` and skips them until trusted. The
    /// entry is matched by position, so a rotated token still needs a fresh review.
    hook_trusted: Option<bool>,
}

#[derive(Serialize)]
struct HookInstallResult {
    path: String,
    backup: Option<String>,
}

fn home_path() -> Result<PathBuf, String> {
    std::env::var_os("HOME")
        .or_else(|| std::env::var_os("USERPROFILE"))
        .map(PathBuf::from)
        .ok_or_else(|| "home directory not found".to_string())
}

/// Where each host reads its user-level hooks from. Both files share the `{"hooks": {...}}` shape.
fn host_settings_path(host: &str) -> Result<PathBuf, String> {
    let home = home_path()?;
    match host {
        prism_core::native::HOST_CLAUDE_CODE => Ok(home.join(".claude").join("settings.json")),
        prism_core::native::HOST_CODEX => Ok(home.join(".codex").join("hooks.json")),
        other => Err(format!("unknown agent host {other}")),
    }
}

/// The hook entry for one host. Claude Code posts natively; Codex has no HTTP hook type, so a
/// one-line curl does the post. It is synchronous: Codex 0.147 skips hooks marked `async`, so the
/// timeouts are tight instead. A loopback post is milliseconds, a stopped Prism refuses at once,
/// and a hung one is cut off by `-m`.
fn host_hook_entry(host: &str, url: &str) -> Result<serde_json::Value, String> {
    match host {
        prism_core::native::HOST_CLAUDE_CODE => Ok(serde_json::json!({
            "type": "http", "url": url, "timeout": 5
        })),
        prism_core::native::HOST_CODEX => {
            let post = |null: &str| {
                format!("curl -s --connect-timeout 1 -m 3 -o {null} -X POST -H Content-Type:application/json --data-binary @- {url}")
            };
            Ok(serde_json::json!({
                "type": "command",
                "command": post("/dev/null"),
                "commandWindows": post("NUL").replacen("curl ", "curl.exe ", 1),
                "timeout": 5,
                "statusMessage": "Prism"
            }))
        }
        other => Err(format!("unknown agent host {other}")),
    }
}

fn read_settings_json(path: &PathBuf) -> Result<serde_json::Value, String> {
    if !path.exists() {
        return Ok(serde_json::json!({}));
    }
    let text = std::fs::read_to_string(path).map_err(|err| err.to_string())?;
    if text.trim().is_empty() {
        return Ok(serde_json::json!({}));
    }
    let value: serde_json::Value = serde_json::from_str(&text)
        .map_err(|err| format!("{} is not valid JSON: {err}", path.display()))?;
    if !value.is_object() {
        return Err(format!("{} is not a JSON object", path.display()));
    }
    Ok(value)
}

/// Any hook that points at a Prism hook route, whatever the token: the one to replace.
fn is_prism_hook(hook: &serde_json::Value) -> bool {
    ["url", "command", "commandWindows"].iter().any(|key| {
        hook.get(key)
            .and_then(|v| v.as_str())
            .is_some_and(|v| v.contains("/hooks/") && v.contains("127.0.0.1"))
    })
}

/// The index of the PreToolUse group holding the hook for `url`, if any.
fn prism_group_index(settings: &serde_json::Value, url: &str) -> Option<usize> {
    settings
        .pointer("/hooks/PreToolUse")
        .and_then(|v| v.as_array())?
        .iter()
        .position(|group| {
            group
                .get("hooks")
                .and_then(|h| h.as_array())
                .is_some_and(|hooks| {
                    hooks.iter().any(|hook| {
                        is_prism_hook(hook)
                            && ["url", "command"].iter().any(|key| {
                                hook.get(key)
                                    .and_then(|v| v.as_str())
                                    .is_some_and(|v| v.contains(url))
                            })
                    })
                })
        })
}

/// Codex keeps hook trust in `config.toml` as
/// `[hooks.state."<hooks.json>:pre_tool_use:<group>:<hook>"]` with a `trusted_hash`, and
/// `enabled = false` when the user switched it off. Read, never written: trusting is the user's
/// review step inside Codex.
fn codex_hook_trusted(hooks_path: &std::path::Path, group: usize) -> bool {
    let Some(dir) = hooks_path.parent() else {
        return false;
    };
    let Ok(config) = std::fs::read_to_string(dir.join("config.toml")) else {
        return false;
    };
    let header = format!(
        "[hooks.state.\"{}:pre_tool_use:{group}:0\"]",
        hooks_path.display()
    );
    let mut in_section = false;
    let mut trusted = false;
    let mut enabled = true;
    for line in config.lines().map(str::trim) {
        if line.starts_with('[') {
            if in_section {
                break;
            }
            in_section = line == header;
            continue;
        }
        if in_section {
            if line.starts_with("trusted_hash") {
                trusted = true;
            } else if line.starts_with("enabled") && line.ends_with("false") {
                enabled = false;
            }
        }
    }
    trusted && enabled
}

#[tauri::command]
async fn get_native_status(state: State<'_, AppState>) -> Result<NativeStatusDto, String> {
    let status = state.gateway.native_status().await;
    let mut setup = Vec::new();
    for host in &status.hosts {
        let path = host_settings_path(&host.host)?;
        let group = read_settings_json(&path)
            .ok()
            .and_then(|settings| prism_group_index(&settings, &host.hook_url));
        let hook_trusted = match (host.host.as_str(), group) {
            (prism_core::native::HOST_CODEX, Some(group)) => Some(codex_hook_trusted(&path, group)),
            (prism_core::native::HOST_CODEX, None) => Some(false),
            _ => None,
        };
        setup.push(HostSetupDto {
            host: host.host.clone(),
            settings_path: path.display().to_string(),
            hook_installed: group.is_some(),
            hook_trusted,
        });
    }
    Ok(NativeStatusDto { status, setup })
}

#[tauri::command]
async fn set_observe_native(state: State<'_, AppState>, on: bool) -> Result<(), String> {
    state.gateway.set_observe_native(on).await.map_err(map_err)
}

#[tauri::command]
async fn rotate_hook_token(state: State<'_, AppState>) -> Result<(), String> {
    state.gateway.rotate_hook_token().await.map_err(map_err)
}

/// The exact `hooks` entry for the host's file, for the copy button.
#[tauri::command]
async fn get_host_hook_snippet(state: State<'_, AppState>, host: String) -> Result<String, String> {
    let url = state.gateway.hook_url(&host);
    let value = serde_json::json!({
        "hooks": { "PreToolUse": [ { "hooks": [ host_hook_entry(&host, &url)? ] } ] }
    });
    serde_json::to_string_pretty(&value).map_err(|err| err.to_string())
}

/// Merge the hook into the host's user-level hooks file. Every other key and every other hook
/// is left alone; an earlier Prism hook (an old token, say) is replaced. The previous file is
/// kept beside it as `.bak`.
#[tauri::command]
async fn install_host_hook(
    state: State<'_, AppState>,
    host: String,
) -> Result<HookInstallResult, String> {
    let url = state.gateway.hook_url(&host);
    let entry = host_hook_entry(&host, &url)?;
    let path = host_settings_path(&host)?;
    let mut settings = read_settings_json(&path)?;
    let root = settings.as_object_mut().expect("object checked above");
    let hooks = root.entry("hooks").or_insert_with(|| serde_json::json!({}));
    if !hooks.is_object() {
        return Err("\"hooks\" in the hooks file is not an object".into());
    }
    let pre = hooks
        .as_object_mut()
        .expect("checked")
        .entry("PreToolUse")
        .or_insert_with(|| serde_json::json!([]));
    let Some(groups) = pre.as_array_mut() else {
        return Err("\"hooks.PreToolUse\" in the hooks file is not an array".into());
    };
    for group in groups.iter_mut() {
        if let Some(list) = group.get_mut("hooks").and_then(|h| h.as_array_mut()) {
            list.retain(|hook| !is_prism_hook(hook));
        }
    }
    groups.retain(|group| {
        group
            .get("hooks")
            .and_then(|h| h.as_array())
            .is_none_or(|list| !list.is_empty())
    });
    groups.push(serde_json::json!({ "hooks": [ entry ] }));
    let backup = if path.exists() {
        let bak = path.with_extension("json.bak");
        std::fs::copy(&path, &bak).map_err(|err| err.to_string())?;
        Some(bak.display().to_string())
    } else {
        if let Some(dir) = path.parent() {
            std::fs::create_dir_all(dir).map_err(|err| err.to_string())?;
        }
        None
    };
    let text = serde_json::to_string_pretty(&settings).map_err(|err| err.to_string())?;
    let tmp = path.with_extension("json.tmp");
    std::fs::write(&tmp, format!("{text}\n")).map_err(|err| err.to_string())?;
    std::fs::rename(&tmp, &path).map_err(|err| err.to_string())?;
    Ok(HookInstallResult {
        path: path.display().to_string(),
        backup,
    })
}

/// Write the would-have-asked entries of the last 30 days to the Downloads folder.
#[tauri::command]
async fn export_native_report(
    app: AppHandle,
    state: State<'_, AppState>,
) -> Result<String, String> {
    let body = state.gateway.native_export(30).await;
    let dir = app
        .path()
        .download_dir()
        .or_else(|_| app.path().home_dir())
        .map_err(|err| err.to_string())?;
    let name = format!(
        "prism-native-{}.jsonl",
        chrono::Utc::now().format("%Y-%m-%d")
    );
    let path = dir.join(name);
    std::fs::write(&path, body).map_err(|err| err.to_string())?;
    Ok(path.display().to_string())
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
            let body = format!("{} wants to connect", agent.name);
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

/// Whether the updater can replace this install by itself. The bundler stamps the bundle type
/// into the binary; a bare `cargo build` or an unknown package manager gets the release page instead.
/// Deb and rpm installs go through `pkexec`, so the user sees a privilege prompt on those.
fn update_installable() -> bool {
    use tauri::utils::{config::BundleType, platform::bundle_type};
    if cfg!(target_os = "linux") {
        matches!(
            bundle_type(),
            Some(BundleType::AppImage | BundleType::Deb | BundleType::Rpm)
        )
    } else {
        true
    }
}

/// Ask the release endpoint whether something newer exists. Remembers the answer for the panel and
/// tells it through the update event. Errors are reported, never fatal: an offline check is normal.
async fn check_for_update(app: &AppHandle, announce: bool) -> Result<Option<UpdateInfo>, String> {
    let state = app.state::<UpdateState>();
    let updater = app.updater().map_err(|e| e.to_string())?;
    let result = updater.check().await;
    if let Ok(mut at) = state.checked_at.lock() {
        *at = Some(chrono::Utc::now().to_rfc3339());
    }
    match result {
        Ok(Some(update)) => {
            let info = UpdateInfo {
                version: update.version.clone(),
                current: update.current_version.clone(),
                notes: update.body.clone(),
                date: update.date.map(|d| d.to_string()),
                installable: update_installable(),
            };
            if let Ok(mut slot) = state.update.lock() {
                *slot = Some(update);
            }
            if let Ok(mut slot) = state.info.lock() {
                *slot = Some(info.clone());
            }
            if announce {
                let _ = app.emit(UPDATE_EVENT, UpdateEvent::Available(info.clone()));
            }
            Ok(Some(info))
        }
        Ok(None) => {
            if let Ok(mut slot) = state.update.lock() {
                *slot = None;
            }
            if let Ok(mut slot) = state.info.lock() {
                *slot = None;
            }
            if announce {
                let _ = app.emit(UPDATE_EVENT, UpdateEvent::UpToDate);
            }
            Ok(None)
        }
        Err(err) => {
            warn!(%err, "update check failed");
            if announce {
                let _ = app.emit(
                    UPDATE_EVENT,
                    UpdateEvent::Error {
                        message: err.to_string(),
                    },
                );
            }
            Err(err.to_string())
        }
    }
}

fn start_update_checks(app: AppHandle) {
    tauri::async_runtime::spawn(async move {
        tokio::time::sleep(UPDATE_FIRST_CHECK).await;
        loop {
            let _ = check_for_update(&app, true).await;
            tokio::time::sleep(UPDATE_INTERVAL).await;
        }
    });
}

#[derive(Clone, Serialize)]
struct UpdateStatusDto {
    current: String,
    available: Option<UpdateInfo>,
    checked_at: Option<String>,
    installable: bool,
}

#[tauri::command]
fn get_update_status(app: AppHandle, state: State<'_, UpdateState>) -> UpdateStatusDto {
    UpdateStatusDto {
        current: app.package_info().version.to_string(),
        available: state.info.lock().ok().and_then(|i| i.clone()),
        checked_at: state.checked_at.lock().ok().and_then(|c| c.clone()),
        installable: update_installable(),
    }
}

#[tauri::command]
async fn check_update(app: AppHandle) -> Result<Option<UpdateInfo>, String> {
    check_for_update(&app, false).await
}

/// Download, install and relaunch. The panel watches `prism://update` for progress. On Windows the
/// installer takes over and the process exits; elsewhere Prism restarts itself.
#[tauri::command]
async fn install_update(app: AppHandle) -> Result<(), String> {
    let state = app.state::<UpdateState>();
    if state.busy.swap(true, Ordering::SeqCst) {
        return Err("an update is already installing".into());
    }
    let update = state.update.lock().ok().and_then(|u| u.clone());
    let Some(update) = update else {
        state.busy.store(false, Ordering::SeqCst);
        return Err("no update has been found yet".into());
    };
    if !update_installable() {
        state.busy.store(false, Ordering::SeqCst);
        return Err("this install cannot update itself; download the new release instead".into());
    }
    let progress_app = app.clone();
    let mut downloaded: u64 = 0;
    let result = update
        .download_and_install(
            move |chunk, total| {
                downloaded += chunk as u64;
                let _ =
                    progress_app.emit(UPDATE_EVENT, UpdateEvent::Downloading { downloaded, total });
            },
            || {},
        )
        .await;
    match result {
        Ok(()) => {
            let _ = app.emit(UPDATE_EVENT, UpdateEvent::Installing);
            tokio::time::sleep(std::time::Duration::from_millis(400)).await;
            app.restart();
        }
        Err(err) => {
            state.busy.store(false, Ordering::SeqCst);
            let message = err.to_string();
            let _ = app.emit(
                UPDATE_EVENT,
                UpdateEvent::Error {
                    message: message.clone(),
                },
            );
            Err(message)
        }
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

    // Menu launches get the session PATH, which lacks the shell's additions; servers are found
    // by name on PATH, so ask the login shell before anything spawns.
    prism_core::adopt_login_shell_path();

    let builder = tauri::Builder::default()
        .plugin(tauri_plugin_notification::init())
        .plugin(tauri_plugin_global_shortcut::Builder::new().build())
        .plugin(tauri_plugin_updater::Builder::new().build())
        .plugin(tauri_plugin_opener::init())
        .manage(UpdateState::default())
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

            recall_tray_hint(app.handle());
            build_tray(app.handle())?;
            forward_events(app.handle().clone(), gateway);
            start_update_checks(app.handle().clone());

            // Dev affordance: `PRISM_SHOW_PANEL=1 cargo tauri dev` opens the panel without a tray click.
            if std::env::var_os("PRISM_SHOW_PANEL").is_some() {
                let handle = app.handle().clone();
                tauri::async_runtime::spawn(async move {
                    tokio::time::sleep(std::time::Duration::from_millis(800)).await;
                    note_cursor_hint(&handle);
                    show_panel(&handle);
                });
            }

            // Ctrl+Alt+P everywhere. Ctrl/Cmd+Shift+Space was the first choice, but that is
            // 1Password's quick-access key on every platform. `panel_shortcut` in prism.json
            // overrides it; an empty string turns it off.
            let configured = app
                .try_state::<AppState>()
                .and_then(|s| s.gateway.panel_shortcut());
            let shortcut = match configured.as_deref().map(str::trim) {
                Some("") => None,
                Some(text) => match text.parse::<Shortcut>() {
                    Ok(shortcut) => Some(shortcut),
                    Err(err) => {
                        warn!(%err, shortcut = text, "panel_shortcut is not a key combination; using the default");
                        Some(DEFAULT_SHORTCUT())
                    }
                },
                None => Some(DEFAULT_SHORTCUT()),
            };
            if let Some(shortcut) = shortcut {
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
            sign_in_server,
            sign_out_server,
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
            get_update_status,
            check_update,
            install_update,
            get_native_status,
            set_observe_native,
            rotate_hook_token,
            get_host_hook_snippet,
            install_host_hook,
            get_activity,
            export_native_report,
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
