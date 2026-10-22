use serde::Serialize;
use serde_json::{json, Value};
use std::{
    fs,
    io::{BufRead, BufReader, Write},
    path::Path,
    process::{Child, ChildStdin, Command, Stdio},
    sync::mpsc::{self, Receiver},
    thread,
    time::Duration,
};

pub fn private_update_token() -> Result<String, String> {
    let mut bytes = [0u8; 32];
    #[cfg(windows)]
    {
        #[link(name = "bcrypt")]
        extern "system" {
            fn BCryptGenRandom(
                handle: *mut std::ffi::c_void,
                buffer: *mut u8,
                len: u32,
                flags: u32,
            ) -> i32;
        }
        if unsafe { BCryptGenRandom(std::ptr::null_mut(), bytes.as_mut_ptr(), 32, 2) } != 0 {
            return Err("无法生成私有更新令牌".into());
        }
    }
    #[cfg(not(windows))]
    {
        use std::io::Read;
        fs::File::open("/dev/urandom")
            .and_then(|mut f| f.read_exact(&mut bytes))
            .map_err(|e| e.to_string())?;
    }
    Ok(bytes.iter().map(|b| format!("{b:02x}")).collect())
}

#[derive(Clone, Serialize)]
pub struct Connection {
    pub port: Option<u16>,
    pub error: Option<String>,
}

pub struct Gateway {
    update_token: String,
    child: Option<Child>,
    stdin: Option<ChildStdin>,
    responses: Receiver<String>,
    connection: Connection,
    agent: Option<crate::opencode::Opencode>,
}

impl Gateway {
    pub fn start(data_dir: &Path) -> Self {
        let (sender, responses) = mpsc::channel();
        let mut gateway = Self {
            update_token: String::new(),
            child: None,
            stdin: None,
            responses,
            connection: Connection {
                port: None,
                error: None,
            },
            agent: None,
        };
        let result = (|| -> Result<(), String> {
            fs::create_dir_all(data_dir.join("logs")).map_err(|e| e.to_string())?;
            // externalBin is installed beside the application executable on Windows.
            let executable = std::env::current_exe().map_err(|e| e.to_string())?;
            let sidecar = executable
                .parent()
                .ok_or("Missing executable directory")?
                .join(if cfg!(windows) {
                    "open-design-gateway.exe"
                } else {
                    "open-design-gateway"
                });
            let origin = if cfg!(debug_assertions) {
                "http://localhost:1420"
            } else {
                "http://tauri.localhost"
            };
            let mut command = Command::new(sidecar);
            gateway.update_token = private_update_token()?;
            command.env("MOVIEDESIGN_UPDATE_TOKEN", &gateway.update_token);
            // These are private launch-state inputs, never inherited from the shell.
            command.env_remove("MOVIEDESIGN_AGENT_BOOTSTRAP");
            command.env_remove("MOVIEDESIGN_AGENT_PREPARE_FAILED");
            match crate::opencode::Opencode::prepare(data_dir) {
                Ok(agent) => {
                    command.env("MOVIEDESIGN_AGENT_BOOTSTRAP", &agent.directory);
                    gateway.agent = Some(agent);
                }
                Err(_) => {
                    // Failed ACL/profile preparation forbids model startup, not the workspace.
                    command.env("MOVIEDESIGN_AGENT_PREPARE_FAILED", "1");
                }
            }
            command
                .arg("--data-dir")
                .arg(data_dir)
                .args(["--port", "8001", "--origin", origin])
                .stdin(Stdio::piped())
                .stdout(Stdio::piped())
                .stderr(Stdio::from(
                    fs::OpenOptions::new()
                        .create(true)
                        .append(true)
                        .open(data_dir.join("logs/shell.log"))
                        .map_err(|e| e.to_string())?,
                ));
            #[cfg(windows)]
            {
                use std::os::windows::process::CommandExt;
                command.creation_flags(0x08000000); // CREATE_NO_WINDOW
            }
            let mut child = command
                .spawn()
                .map_err(|e| format!("启动本地服务失败：{e}"))?;
            gateway.stdin = child.stdin.take();
            let stdout = child.stdout.take().ok_or("Missing sidecar stdout")?;
            gateway.child = Some(child);
            thread::spawn(move || {
                let mut reader = BufReader::new(stdout);
                loop {
                    let mut line = String::new();
                    match reader.read_line(&mut line) {
                        Ok(0) | Err(_) => break,
                        Ok(_) => {
                            if sender.send(line).is_err() {
                                break;
                            }
                        }
                    }
                }
            });
            let info = gateway.request(1, "gateway.info")?;
            if info.get("port").and_then(Value::as_u64) != Some(8001) {
                return Err("Gateway returned a port incompatible with the desktop CSP".into());
            }
            let health = gateway.request(2, "gateway.health")?;
            if health.get("status").and_then(Value::as_str) != Some("ok") {
                return Err("本地服务尚未就绪".into());
            }
            gateway.connection.port = Some(8001);
            if let Some(agent) = gateway.agent.as_mut() {
                let _ = agent.start();
            }
            Ok(())
        })();
        if let Err(error) = result {
            if let Err(error) = gateway.shutdown() {
                eprintln!("owned_startup_cleanup_failed: {error}");
            }
            gateway.connection.error = Some(format!(
                "{error}。日志：{}",
                data_dir.join("logs").display()
            ));
        }
        gateway
    }

    fn request(&mut self, id: u64, method: &str) -> Result<Value, String> {
        let stdin = self.stdin.as_mut().ok_or("Gateway input unavailable")?;
        writeln!(
            stdin,
            "{}",
            json!({"jsonrpc":"2.0", "id":id, "method":method})
        )
        .map_err(|e| e.to_string())?;
        stdin.flush().map_err(|e| e.to_string())?;
        let line = self
            .responses
            .recv_timeout(Duration::from_secs(10))
            .map_err(|e| format!("本地服务握手失败（检查端口 8001 和日志）：{e}"))?;
        parse_response(&line, id)
    }

    pub fn connection(&mut self) -> Connection {
        if let Some(agent) = self.agent.as_mut() {
            agent.poll();
        }
        if let Some(child) = self.child.as_mut() {
            match child.try_wait() {
                Ok(Some(status)) => {
                    self.connection.port = None;
                    self.connection.error = Some(format!(
                        "本地服务已退出：{status}。请检查服务日志后重新启动。"
                    ));
                }
                Err(error) => {
                    self.connection.port = None;
                    self.connection.error = Some(error.to_string());
                }
                Ok(None) => {}
            }
        }
        self.connection.clone()
    }

    pub fn update_connection(&mut self) -> Result<(u16, String, &'static str), String> {
        let conn = self.connection();
        let port = conn
            .port
            .ok_or_else(|| conn.error.unwrap_or_else(|| "本地服务尚未就绪".into()))?;
        Ok((
            port,
            self.update_token.clone(),
            if cfg!(debug_assertions) {
                "http://localhost:1420"
            } else {
                "http://tauri.localhost"
            },
        ))
    }

    pub fn shutdown(&mut self) -> Result<(), String> {
        // Keep OpenCode alive while Go drains/aborts/disconnects and attempts its
        // authenticated global.dispose; only then reap the owned model process.
        if let Some(mut child) = self.child.take() {
            if let Some(mut stdin) = self.stdin.take() {
                let _ = writeln!(
                    stdin,
                    "{}",
                    json!({"jsonrpc":"2.0", "id":3, "method":"gateway.shutdown"})
                );
                let _ = stdin.flush();
                // Dropping stdin also makes the gateway exit if the shutdown frame was lost.
            }
            match crate::opencode::finish_owned(&mut child, Duration::from_secs(6)) {
                Ok(forced) => eprintln!(
                    "owned_gateway_stop={}",
                    if forced {
                        "terminated_after_shutdown_grace"
                    } else {
                        "natural_exit"
                    }
                ),
                Err(error) => {
                    self.child = Some(child);
                    return Err(error);
                }
            }
        }
        if let Some(mut agent) = self.agent.take() {
            if let Err(error) = agent.shutdown() {
                self.agent = Some(agent);
                return Err(error);
            }
        }
        self.connection.port = None;
        Ok(())
    }
}

impl Drop for Gateway {
    fn drop(&mut self) {
        if let Err(error) = self.shutdown() {
            eprintln!("owned_gateway_stop_failed: {error}");
        }
    }
}

fn parse_response(line: &str, id: u64) -> Result<Value, String> {
    let value: Value = serde_json::from_str(line).map_err(|e| e.to_string())?;
    if value.get("jsonrpc").and_then(Value::as_str) != Some("2.0")
        || value.get("id").and_then(Value::as_u64) != Some(id)
    {
        return Err("Invalid gateway JSON-RPC response".into());
    }
    if let Some(error) = value.get("error") {
        return Err(format!("Gateway rejected request: {error}"));
    }
    value
        .get("result")
        .cloned()
        .ok_or_else(|| "Missing gateway result".into())
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn shutdown_drains_gateway_while_opencode_is_alive_then_waits_for_owned_exit() {
        let dir = std::env::temp_dir().join(format!(
            "moviedesign-lifecycle-{}",
            private_update_token().unwrap()
        ));
        fs::create_dir_all(&dir).unwrap();
        let mut child = crate::opencode::tests::spawn_exit_fixture(&dir, true);
        let stdin = child.stdin.take();
        let agent = crate::opencode::Opencode::controlled_child(
            crate::opencode::tests::spawn_exit_fixture(&dir, false),
            dir.clone(),
        );
        let (_tx, responses) = mpsc::channel();
        let mut gateway = Gateway {
            update_token: String::new(),
            child: Some(child),
            stdin,
            responses,
            connection: Connection {
                port: Some(8001),
                error: None,
            },
            agent: Some(agent),
        };
        gateway.shutdown().unwrap();
        assert!(
            dir.join("gateway-drained").exists(),
            "gateway must receive its existing normal shutdown frame"
        );
        assert!(dir.join("opencode-exited").exists(),"owned OpenCode must stay alive during gateway drain and get a natural exit opportunity");
        fs::remove_dir_all(dir).unwrap();
    }
    #[test]
    fn validates_control_responses() {
        assert_eq!(
            parse_response(r#"{"jsonrpc":"2.0","id":1,"result":{"port":8001}}"#, 1).unwrap()
                ["port"],
            8001
        );
        for invalid in [
            "not-json",
            r#"{"jsonrpc":"1.0","id":1,"result":{}}"#,
            r#"{"jsonrpc":"2.0","id":2,"result":{}}"#,
            r#"{"jsonrpc":"2.0","id":1,"error":{"code":-32601}}"#,
        ] {
            assert!(parse_response(invalid, 1).is_err());
        }
    }
    #[test]
    fn shutdown_operates_only_on_owned_handles_and_is_idempotent() {
        let (_tx, rx) = mpsc::channel();
        let mut gw = Gateway {
            update_token: String::new(),
            child: None,
            stdin: None,
            responses: rx,
            connection: Connection {
                port: Some(8001),
                error: None,
            },
            agent: None,
        };
        gw.shutdown().unwrap();
        assert_eq!(gw.connection.port, None);
        gw.shutdown().unwrap();
        assert_eq!(gw.connection.port, None);
    }
}
