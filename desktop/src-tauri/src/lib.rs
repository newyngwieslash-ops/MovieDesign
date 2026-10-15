mod gateway;

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
        .setup(|app| {
            let data_dir = match std::env::var_os("MOVIEDESIGN_DATA_DIR") {
                Some(path) => std::path::PathBuf::from(path),
                None => app.path().app_data_dir()?,
            };
            app.manage(Mutex::new(gateway::Gateway::start(&data_dir)));
            Ok(())
        })
        .invoke_handler(tauri::generate_handler![gateway_connection])
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
