//! System tray: window visibility, backend control, quit.

use tauri::{
    menu::{MenuBuilder, MenuItemBuilder},
    tray::TrayIconBuilder,
    Manager,
};

/// Build the tray icon with its menu. Best-effort: callers log and continue
/// if the platform has no tray support.
pub fn build_tray(app: &tauri::AppHandle) -> tauri::Result<()> {
    let show = MenuItemBuilder::with_id("show", "Show Lele").build(app)?;
    let restart = MenuItemBuilder::with_id("restart", "Restart Backend").build(app)?;
    let data = MenuItemBuilder::with_id("data", "Open Data Folder").build(app)?;
    let quit = MenuItemBuilder::with_id("quit", "Quit").build(app)?;

    let menu = MenuBuilder::new(app)
        .items(&[&show, &restart, &data, &quit])
        .build()?;

    let icon = app
        .default_window_icon()
        .cloned()
        .ok_or_else(|| tauri::Error::Anyhow(anyhow::anyhow!("default window icon missing")))?;

    TrayIconBuilder::with_id("main-tray")
        .icon(icon)
        .tooltip("Lele")
        .menu(&menu)
        .on_menu_event(|app, event| match event.id().as_ref() {
            "show" => {
                if let Some(w) = app.get_webview_window("main") {
                    let _ = w.show();
                    let _ = w.set_focus();
                }
            }
            "restart" => {
                let app = app.clone();
                std::thread::spawn(move || {
                    match crate::commands::do_restart(&app) {
                        Ok(url) => {
                            if let Some(w) = app.get_webview_window("main") {
                                if let Ok(parsed) = url.parse::<tauri::Url>() {
                                    let _ = w.navigate(parsed);
                                }
                            }
                        }
                        Err(e) => eprintln!("backend restart failed: {e}"),
                    }
                });
            }
            "data" => crate::commands::do_open_data_dir(),
            "quit" => crate::commands::do_quit(app),
            _ => {}
        })
        .build(app)?;

    Ok(())
}