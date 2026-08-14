//! Lele Desktop — Tauri shell for the Lele AI assistant.
//!
//! The desktop app is a thin native wrapper around the Lele gateway. On
//! launch it:
//!
//! 1. Generates/persists a shared auth token (keychain, with a random
//!    fallback so startup never fails on keychain unavailability).
//! 2. Shows a splash window while the sidecar gateway boots.
//! 3. Spawns the bundled `lele gateway --desktop` sidecar and, once it
//!    reports ready, opens the main window pointed at the gateway URL and
//!    injects the desktop session context via an initialization script.
//!
//! Closing the main window hides it to the tray; quitting goes through the
//! tray menu (which stops the sidecar).

pub mod bootstrap;
pub mod commands;
pub mod sidecar;
pub mod tray;

use std::sync::Mutex;
use std::time::Duration;

use tauri::{Manager, WebviewUrl, WebviewWindowBuilder};

use crate::sidecar::SidecarHandle;

/// Shared application state.
pub struct AppState {
    /// The running sidecar, if any.
    pub sidecar: Mutex<Option<SidecarHandle>>,
    /// Desktop auth token (from keychain or generated).
    pub token: String,
    /// Gateway URL once the sidecar is ready ("" until ready).
    pub api_url: Mutex<String>,
}

/// How long we wait for the gateway to report ready before giving up.
const READY_TIMEOUT: Duration = Duration::from_secs(30);

/// Resolve the path to the lele sidecar binary.
fn resolve_sidecar_path(app: &tauri::AppHandle) -> std::path::PathBuf {
    // 1. Env override.
    if let Ok(p) = std::env::var("LELE_SIDECAR_BIN") {
        if !p.is_empty() {
            return std::path::PathBuf::from(p);
        }
    }
    // 2. Bundled sidecar in the resource dir: scan for a file named "lele-*".
    if let Ok(resource_dir) = app.path().resource_dir() {
        let binaries = resource_dir.join("binaries");
        if let Ok(entries) = std::fs::read_dir(&binaries) {
            for entry in entries.flatten() {
                let name = entry.file_name();
                let name = name.to_string_lossy();
                if name.starts_with("lele-") {
                    return entry.path();
                }
            }
        }
    }
    // 3. Fall back to lele on PATH.
    std::path::PathBuf::from("lele")
}

/// Build and run the Tauri application.
pub fn run() {
    tauri::Builder::<tauri::Wry>::default()
        .plugin(tauri_plugin_shell::init())
        .plugin(tauri_plugin_opener::init())
        .plugin(tauri_plugin_single_instance::init(|app, _args, _cwd| {
            if let Some(w) = app.get_webview_window("main") {
                let _ = w.show();
                let _ = w.set_focus();
            }
        }))
        .setup(|app| {
            // Token: keychain first, random fallback (never fail startup).
            let token = match bootstrap::get_or_create_token() {
                Ok(t) => t,
                Err(e) => {
                    eprintln!("keychain unavailable ({e}); using ephemeral token");
                    let mut bytes = [0u8; 32];
                    rand::RngCore::fill_bytes(&mut rand::thread_rng(), &mut bytes);
                    hex::encode(bytes)
                }
            };

            app.manage(AppState {
                sidecar: Mutex::new(None),
                token: token.clone(),
                api_url: Mutex::new(String::new()),
            });

            // Splash window (bundled frontend) while the sidecar boots.
            let _splash = WebviewWindowBuilder::new(app, "splash", WebviewUrl::App("index.html".into()))
                .title("Lele Desktop")
                .inner_size(480.0, 360.0)
                .resizable(false)
                .center()
                .build()?;

            let bin_path = resolve_sidecar_path(&app.handle());
            let handle = app.handle().clone();
            std::thread::spawn(move || {
                start_backend(handle, bin_path, token);
            });

            Ok(())
        })
        .invoke_handler(tauri::generate_handler![
            commands::get_session,
            commands::backend_status,
            commands::restart_backend,
            commands::open_data_dir,
            commands::quit_app
        ])
        .on_window_event(|window, event| {
            // Hide-to-tray on close for the main window (quit via tray menu).
            if window.label() == "main" {
                if let tauri::WindowEvent::CloseRequested { api, .. } = event {
                    api.prevent_close();
                    let _ = window.hide();
                }
            }
        })
        .run(tauri::generate_context!())
        .expect("error while running Lele Desktop");
}

/// Spawn the sidecar and, once ready, open the main window on the gateway URL.
fn start_backend(app: tauri::AppHandle, bin_path: std::path::PathBuf, token: String) {
    match sidecar::spawn_sidecar(&bin_path, &token, READY_TIMEOUT) {
        Ok(sidecar) => {
            let url = sidecar.url().to_string();
            {
                let state = app.state::<AppState>();
                *state.api_url.lock().unwrap() = url.clone();
                *state.sidecar.lock().unwrap() = Some(sidecar);
            }
            let init_js = format!(
                "window.__LELE_DESKTOP__ = {{ mode: \"local\", apiUrl: \"{url}\", session: {{ token: \"{token}\", refresh_token: \"\", client_id: \"desktop-local\", device_name: \"Lele Desktop\" }} }};"
            );
            let _ = app.clone().run_on_main_thread(move || {
                match url.parse::<tauri::Url>() {
                    Ok(parsed) => {
                        let _ = WebviewWindowBuilder::new(&app, "main", WebviewUrl::External(parsed))
                            .title("Lele")
                            .inner_size(1280.0, 800.0)
                            .initialization_script(&init_js)
                            .build();
                    }
                    Err(e) => eprintln!("invalid sidecar URL: {e}"),
                }
                if let Some(splash) = app.get_webview_window("splash") {
                    let _ = splash.close();
                }
                if let Err(e) = tray::build_tray(&app) {
                    eprintln!("tray unavailable: {e}");
                }
            });
        }
        Err(e) => {
            // Phase-2 polish: proper error screen. For now keep the splash open
            // with a changed title and log the cause.
            eprintln!("sidecar failed to start: {e}");
            let _ = app.clone().run_on_main_thread(move || {
                if let Some(splash) = app.get_webview_window("splash") {
                    let _ = splash.set_title("Lele Desktop — backend failed");
                }
            });
        }
    }
}