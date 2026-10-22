mod gateway;
mod opencode;
mod update_runtime;
pub mod updates;
use update_runtime::{
    updater_check_and_gate, updater_decide, updater_discard, updater_install, updater_policy,
    updater_receipt,
};

use std::sync::Mutex;
use tauri::Manager;

#[tauri::command]
fn gateway_connection(state: tauri::State<'_, Mutex<gateway::Gateway>>) -> gateway::Connection {
    match state.lock() {
        Ok(mut gateway) => gateway.connection(),
        Err(_) => gateway::Connection {
            port: None,
            error: Some("本地服务状态锁不可用".into()),
        },
    }
}

pub fn run() {
    let app = tauri::Builder::default()
        .plugin(tauri_plugin_updater::Builder::new().build())
        .manage(Mutex::new(update_runtime::UpdateRuntime::default()))
        .setup(|app| {
            let data_dir = match std::env::var_os("MOVIEDESIGN_DATA_DIR") {
                Some(path) => std::path::PathBuf::from(path),
                None => app.path().app_data_dir()?,
            };
            app.manage(Mutex::new(gateway::Gateway::start(&data_dir)));
            Ok(())
        })
        .invoke_handler(tauri::generate_handler![
            gateway_connection,
            updater_check_and_gate,
            updater_decide,
            updater_install,
            updater_receipt,
            updater_policy,
            updater_discard
        ])
        .build(tauri::generate_context!())
        .expect("failed to initialize MovieDesign desktop");

    app.run(|handle, event| {
        if matches!(event, tauri::RunEvent::Exit) {
            if let Ok(mut gateway) = handle.state::<Mutex<gateway::Gateway>>().lock() {
                if let Err(error) = gateway.shutdown() {
                    eprintln!("owned_shutdown_failed: {error}");
                }
            }
        }
    });
}
