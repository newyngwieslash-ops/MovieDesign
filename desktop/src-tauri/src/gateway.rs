use serde::Serialize;
use serde_json::{json, Value};
use std::{
    fs,
    io::{BufRead, BufReader, Write},
    path::Path,
    process::{Child, ChildStdin, Command, Stdio},
    sync::mpsc::{self, Receiver},
    thread,
    time::{Duration, Instant},
};

#[derive(Clone, Serialize)]
pub struct Connection {
    pub port: Option<u16>,
    pub error: Option<String>,
}

pub struct Gateway {
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
            gateway.agent = Some(crate::opencode::Opencode::prepare(data_dir)?);
            command.env(
                "MOVIEDESIGN_AGENT_BOOTSTRAP",
                &gateway.agent.as_ref().unwrap().directory,
            );
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
            gateway.shutdown();
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

    pub fn shutdown(&mut self) {
        // Stop the owned model runtime before draining the gateway. No PID/port-wide termination.
        if let Some(mut agent) = self.agent.take() {
            agent.shutdown();
        }
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
            let deadline = Instant::now() + Duration::from_secs(4);
            loop {
                match child.try_wait() {
                    Ok(Some(_)) => break,
                    _ if Instant::now() >= deadline => {
                        let _ = child.kill();
                        let _ = child.wait();
                        break;
                    }
                    _ => thread::sleep(Duration::from_millis(30)),
                }
            }
        }
        self.connection.port = None;
    }
}

impl Drop for Gateway {
    fn drop(&mut self) {
        self.shutdown();
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
}
