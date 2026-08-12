//! IPC commands exposed to the web frontend.

use std::path::Path;
use std::time::Duration;

use serde::Serialize;
use tauri::Manager;

use crate::sidecar::{spawn_sidecar, stop_sidecar};
use crate::AppState;

const SPAWN_TIMEOUT: Duration = Duration::from_secs(30);
const STOP_GRACE: Duration = Duration::from_secs(5);

/// Session payload handed to the frontend so it can skip the PIN screen.
#[derive(Debug, Clone, Serialize)]
pub struct SessionInfo {
    pub api_url: String,
    pub token: String,
    pub refresh_token: String,
    pub client_id: String,
    pub device_name: String,
}

/// Sidecar health snapshot.
#[derive(Debug, Clone, Serialize)]
pub struct BackendStatus {
    pub running: bool,
    pub pid: u32,
    pub port: u16,
    pub uptime_secs: u64,
    pub url: String,
}

/// Return the desktop session (token + gateway URL). The frontend uses this
/// to authenticate without the PIN screen. refresh_token is empty: the
/// desktop client token is valid for 10 years and never needs rotation.
#[tauri::command]
pub fn get_session(state: tauri::State<'_, AppState>) -> Result<SessionInfo, String> {
    let api_url = state.api_url.lock().unwrap().clone();
    if api_url.is_empty() {
        return Err("backend not ready yet".into());
    }
    Ok(SessionInfo {
        api_url,
        token: state.token.clone(),
        refresh_token: String::new(),
        client_id: "desktop-local".into(),
        device_name: "Lele Desktop".into(),
    })
}

/// Report sidecar liveness.
#[tauri::command]
pub fn backend_status(state: tauri::State<'_, AppState>) -> BackendStatus {
    let mut guard = state.sidecar.lock().unwrap();
    match guard.as_mut() {
        Some(h) if h.is_alive() => BackendStatus {
            running: true,
            pid: h.pid(),
            port: h.port(),
            uptime_secs: h.uptime_secs(),
            url: h.url().to_string(),
        },
        _ => BackendStatus { running: false, pid: 0, port: 0, uptime_secs: 0, url: String::new() },
    }
}

/// Restart the backend: stop the sidecar, respawn it, and navigate the main
/// window to the new gateway URL.
#[tauri::command]
pub fn restart_backend(app: tauri::AppHandle, state: tauri::State<'_, AppState>) -> Result<(), String> {
    let bin_path = sidecar_bin_path(&app);
    let token = state.token.clone();
    let url = do_restart_inner(&app, &state, &bin_path, &token)?;
    // Navigate the main window to the new URL.
    if let Some(w) = app.get_webview_window("main") {
        if let Ok(parsed) = url.parse::<tauri::Url>() {
            let _ = w.navigate(parsed);
        }
    }
    Ok(())
}

/// Shared restart logic used by both the IPC command and the tray menu.
/// Returns the new gateway URL.
pub fn do_restart(app: &tauri::AppHandle) -> Result<String, String> {
    let state = app.state::<AppState>();
    let bin_path = sidecar_bin_path(app);
    let token = state.token.clone();
    do_restart_inner(app, &state, &bin_path, &token)
}

fn do_restart_inner(
    _app: &tauri::AppHandle,
    state: &tauri::State<'_, AppState>,
    bin_path: &Path,
    token: &str,
) -> Result<String, String> {
    // Stop the existing sidecar (if any).
    {
        let mut guard = state.sidecar.lock().unwrap();
        if let Some(mut h) = guard.take() {
            let _ = stop_sidecar(&mut h, STOP_GRACE);
        }
    }
    // Respawn.
    let handle = spawn_sidecar(bin_path, token, SPAWN_TIMEOUT).map_err(|e| e.to_string())?;
    let url = handle.url().to_string();
    *state.api_url.lock().unwrap() = url.clone();
    *state.sidecar.lock().unwrap() = Some(handle);
    Ok(url)
}

/// Resolve the path of the lele sidecar binary, in priority order:
///   1. The `LELE_SIDECAR_BIN` environment override.
///   2. A bundled binary in `resource_dir`/binaries whose name starts with
///      `lele-` (the first such entry wins).
///   3. A plain `lele` executable on PATH.
///
/// Kept local to this module so commands and the tray menu do not depend on
/// a private helper in `lib.rs`.
fn sidecar_bin_path(app: &tauri::AppHandle) -> std::path::PathBuf {
    if let Ok(p) = std::env::var("LELE_SIDECAR_BIN") {
        if !p.is_empty() {
            return std::path::PathBuf::from(p);
        }
    }

    if let Ok(dir) = app.path().resource_dir() {
        let bin_dir = dir.join("binaries");
        if let Ok(entries) = std::fs::read_dir(&bin_dir) {
            for entry in entries.flatten() {
                let name = entry.file_name();
                if let Some(name) = name.to_str() {
                    if name.starts_with("lele-") {
                        return entry.path();
                    }
                }
            }
        }
    }

    std::path::PathBuf::from("lele")
}

/// Open the lele data directory (~/.lele) in the OS file manager.
#[tauri::command]
pub fn open_data_dir() -> Result<(), String> {
    do_open_data_dir();
    Ok(())
}

/// Shared open-data-dir logic (also used by the tray menu).
pub fn do_open_data_dir() {
    let dir = crate::bootstrap::data_dir();
    #[cfg(target_os = "linux")]
    { let _ = std::process::Command::new("xdg-open").arg(&dir).spawn(); }
    #[cfg(target_os = "macos")]
    { let _ = std::process::Command::new("open").arg(&dir).spawn(); }
    #[cfg(target_os = "windows")]
    { let _ = std::process::Command::new("explorer").arg(&dir).spawn(); }
}

/// Quit the app, stopping the sidecar first.
#[tauri::command]
pub fn quit_app(app: tauri::AppHandle, _state: tauri::State<'_, AppState>) {
    do_quit(&app);
}

/// Shared quit logic (also used by the tray menu).
pub fn do_quit(app: &tauri::AppHandle) {
    let state = app.state::<AppState>();
    let mut guard = state.sidecar.lock().unwrap();
    if let Some(mut h) = guard.take() {
        let _ = stop_sidecar(&mut h, STOP_GRACE);
    }
    drop(guard);
    app.exit(0);
}