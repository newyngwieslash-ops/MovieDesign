mod gateway;
mod opencode;
pub mod updates;

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

#[tauri::command]
fn updater_check_and_gate(
    state: tauri::State<'_, Mutex<gateway::Gateway>>,
    current_version: Option<String>,
) -> Result<updates::NativeUpdaterGateReport, String> {
    let conn = match state.lock() {
        Ok(mut gateway) => gateway.connection(),
        Err(_) => return Err("本地服务状态锁不可用".into()),
    };
    let port = conn
        .port
        .ok_or_else(|| conn.error.unwrap_or_else(|| "本地服务尚未就绪".into()))?;
    let version = current_version
        .filter(|v| !v.trim().is_empty())
        .unwrap_or_else(|| env!("CARGO_PKG_VERSION").to_string());
    let snap = updates::fetch_gateway_policy_snapshot(port, &version)?;
    Ok(updates::evaluate_policy_gate(&snap))
}

pub fn run() {
    let app = tauri::Builder::default()
        .plugin(tauri_plugin_updater::Builder::new().build())
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
            updater_check_and_gate
        ])
        .build(tauri::generate_context!())
        .expect("failed to initialize MovieDesign desktop");

    app.run(|handle, event| {
        if matches!(event, tauri::RunEvent::Exit) {
            if let Ok(mut gateway) = handle.state::<Mutex<gateway::Gateway>>().lock() {
                gateway.shutdown();
            }
        }
    });
}
