use serde::Deserialize;
use std::{
    fs,
    net::TcpListener,
    path::{Path, PathBuf},
    process::{Child, Command, Stdio},
    time::{Duration, Instant, SystemTime, UNIX_EPOCH},
};

fn claim_port(port: u16) -> Result<TcpListener, String> {
    TcpListener::bind((std::net::Ipv4Addr::LOCALHOST, port)).map_err(|_| "port_conflict".into())
}

#[derive(Deserialize)]
struct Bootstrap {
    password: String,
    config: String,
    key: String,
}
pub struct Opencode {
    child: Option<Child>,
    pub directory: PathBuf,
}

fn hidden(command: &mut Command) {
    #[cfg(windows)]
    {
        use std::os::windows::process::CommandExt;
        command.creation_flags(0x08000000);
    }
}
fn config_value(value: &str) -> String {
    let json = serde_json::to_string(value).expect("string serializes");
    json[1..json.len() - 1].to_string()
}

// Only Child handles returned by our own spawn reach this bounded reaper. A
// successful dispose is resource cleanup, not proof that the process has exited.
pub(crate) fn finish_owned(child: &mut Child, grace: Duration) -> Result<bool, String> {
    let deadline = Instant::now() + grace;
    loop {
        if child.try_wait().map_err(|e| e.to_string())?.is_some() {
            return Ok(false);
        }
        if Instant::now() >= deadline {
            break;
        }
        std::thread::sleep(Duration::from_millis(10));
    }
    if let Err(e) = child.kill() {
        if child.try_wait().map_err(|e| e.to_string())?.is_some() {
            return Ok(false);
        }
        return Err(format!("owned termination failed: {e}"));
    }
    let deadline = Instant::now() + Duration::from_millis(500);
    loop {
        if child.try_wait().map_err(|e| e.to_string())?.is_some() {
            return Ok(true);
        }
        if Instant::now() >= deadline {
            return Err("owned termination could not be confirmed within 500ms".into());
        }
        std::thread::sleep(Duration::from_millis(10));
    }
}
impl Opencode {
    #[cfg(test)]
    pub(crate) fn controlled_child(child: Child, directory: PathBuf) -> Self {
        Self {
            child: Some(child),
            directory,
        }
    }
    pub fn prepare(data_dir: &Path) -> Result<Self, String> {
        let directory = data_dir.join("agent-host").join(format!(
            "launch-{}-{}",
            std::process::id(),
            SystemTime::now()
                .duration_since(UNIX_EPOCH)
                .map_err(|_| "clock unavailable")?
                .as_nanos()
        ));
        fs::create_dir_all(&directory).map_err(|_| "private agent profile unavailable")?;
        // Restrict the fresh profile to the actual Windows identity, before writing any bootstrap.
        let system = PathBuf::from(std::env::var_os("SystemRoot").ok_or("SystemRoot unavailable")?)
            .join("System32");
        let mut identity = Command::new(system.join("whoami.exe"));
        hidden(&mut identity);
        let user = identity.output().map_err(|_| "identity unavailable")?;
        if !user.status.success() {
            return Err("identity unavailable".into());
        }
        let user = String::from_utf8(user.stdout).map_err(|_| "identity unavailable")?;
        let mut acl = Command::new(system.join("icacls.exe"));
        hidden(&mut acl);
        let result = acl
            .arg(directory.parent().ok_or("profile parent unavailable")?)
            .args([
                "/inheritance:r",
                "/grant:r",
                &format!("{}:(OI)(CI)F", user.trim()),
            ])
            .stdout(Stdio::null())
            .stderr(Stdio::null())
            .status()
            .map_err(|_| "private profile ACL unavailable")?;
        if !result.success() {
            return Err("private profile ACL unavailable".into());
        }
        Ok(Self {
            child: None,
            directory,
        })
    }
    pub fn start(&mut self) -> Result<(), String> {
        let result = self.launch();
        if let Err(ref error) = result {
            let _ = fs::write(
                self.directory.join("error.txt"),
                if error == "port_conflict" {
                    "port_conflict"
                } else {
                    "start_failed"
                },
            );
        }
        // This per-launch encrypted exchange is consumed, never retained after startup.
        let _ = fs::remove_file(self.directory.join("launch.dpapi"));
        result
    }
    fn launch(&mut self) -> Result<(), String> {
        let reservation = claim_port(4096)?;
        let mut plaintext = unprotect(
            &fs::read(self.directory.join("launch.dpapi")).map_err(|_| "bootstrap unavailable")?,
        )?;
        let parsed = serde_json::from_slice::<Bootstrap>(&plaintext);
        plaintext.fill(0);
        let mut boot = parsed.map_err(|_| "bootstrap invalid")?;
        if boot.password.len() != 64 {
            return Err("bootstrap invalid".into());
        }
        let executable = std::env::current_exe().map_err(|_| "executable unavailable")?;
        let sidecar = executable
            .parent()
            .ok_or("executable directory unavailable")?
            .join("moviedesign-opencode.exe");
        let mut command = Command::new(sidecar);
        hidden(&mut command);
        command
            .args(["serve", "--hostname", "127.0.0.1", "--port", "4096"])
            .env_clear()
            .stdin(Stdio::null())
            .stdout(Stdio::null())
            .stderr(Stdio::null());
        let system = std::env::var_os("SystemRoot").ok_or("SystemRoot unavailable")?;
        command
            .env("SystemRoot", &system)
            .env("WINDIR", &system)
            .env("npm_config_offline", "true")
            .env("PATH", PathBuf::from(&system).join("System32"));
        for (key, part) in [
            ("USERPROFILE", "home"),
            ("HOME", "home"),
            ("APPDATA", "roaming"),
            ("LOCALAPPDATA", "local"),
            ("ProgramData", "managed"),
            ("XDG_CONFIG_HOME", "config"),
            ("XDG_DATA_HOME", "data"),
            ("XDG_STATE_HOME", "state"),
            ("XDG_CACHE_HOME", "cache"),
            ("TEMP", "tmp"),
            ("TMP", "tmp"),
        ] {
            let dir = if key == "XDG_DATA_HOME" || key == "XDG_STATE_HOME" {
                self.directory
                    .parent()
                    .ok_or("profile parent unavailable")?
                    .join(part)
            } else {
                self.directory.join(part)
            };
            fs::create_dir_all(&dir).map_err(|_| "profile unavailable")?;
            command.env(key, dir);
        }
        let cwd = self
            .directory
            .parent()
            .ok_or("profile parent unavailable")?
            .join("work");
        fs::create_dir_all(&cwd).map_err(|_| "profile unavailable")?;
        command.current_dir(cwd);
        for flag in [
            "OPENCODE_DISABLE_PROJECT_CONFIG",
            "OPENCODE_DISABLE_AUTOUPDATE",
            "OPENCODE_DISABLE_MODELS_FETCH",
            "OPENCODE_DISABLE_AUTOCOMPACT",
            "OPENCODE_EXPERIMENTAL_DISABLE_FILEWATCHER",
            "OPENCODE_PURE",
            "OPENCODE_DISABLE_DEFAULT_PLUGINS",
            "OPENCODE_DISABLE_EXTERNAL_SKILLS",
            "OPENCODE_DISABLE_CLAUDE_CODE",
            "OPENCODE_DISABLE_LSP_DOWNLOAD",
            "OPENCODE_DISABLE_EMBEDDED_WEB_UI",
        ] {
            command.env(flag, "true");
        }
        command
            .env("OPENCODE_SERVER_PASSWORD", &boot.password)
            .env("OPENCODE_SERVER_USERNAME", "opencode")
            .env("OPENCODE_CONFIG_CONTENT", &boot.config)
            .env("MOVIEDESIGN_LLM_KEY", config_value(&boot.key))
            .env(
                "MOVIEDESIGN_TRUNCATE_GLOB",
                config_value(
                    &self
                        .directory
                        .parent()
                        .ok_or("profile parent unavailable")?
                        .join("data")
                        .join("opencode")
                        .join("tool-output")
                        .join("*")
                        .to_string_lossy(),
                ),
            );
        drop(reservation); // The child must bind itself; authentication prevents attaching across this unavoidable bind gap.
        let spawned = command.spawn();
        boot.password.clear();
        boot.key.clear();
        boot.config.clear();
        self.child = Some(spawned.map_err(|_| "start_failed")?);
        Ok(())
    }
    pub fn poll(&mut self) {
        if let Some(child) = self.child.as_mut() {
            if !matches!(child.try_wait(), Ok(None)) {
                let _ = fs::write(self.directory.join("error.txt"), "exited");
            }
        }
    }
    pub fn shutdown(&mut self) -> Result<(), String> {
        if let Some(mut child) = self.child.take() {
            match finish_owned(&mut child, Duration::from_secs(1)) {
                Ok(forced) => eprintln!(
                    "owned_opencode_stop={}",
                    if forced {
                        "terminated_after_cleanup_grace"
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
        let _ = fs::remove_file(self.directory.join("launch.dpapi"));
        Ok(())
    }
}
impl Drop for Opencode {
    fn drop(&mut self) {
        if let Err(error) = self.shutdown() {
            eprintln!("owned_opencode_stop_failed: {error}");
        }
    }
}

#[cfg(windows)]
fn unprotect(ciphertext: &[u8]) -> Result<Vec<u8>, String> {
    #[repr(C)]
    struct Blob {
        size: u32,
        data: *mut u8,
    }
    #[link(name = "crypt32")]
    unsafe extern "system" {
        fn CryptUnprotectData(
            input: *const Blob,
            description: *mut *mut u16,
            entropy: *const Blob,
            reserved: *mut std::ffi::c_void,
            prompt: *mut std::ffi::c_void,
            flags: u32,
            output: *mut Blob,
        ) -> i32;
    }
    #[link(name = "kernel32")]
    unsafe extern "system" {
        fn LocalFree(memory: *mut std::ffi::c_void) -> *mut std::ffi::c_void;
    }
    let input = Blob {
        size: u32::try_from(ciphertext.len()).map_err(|_| "bootstrap too large")?,
        data: ciphertext.as_ptr() as *mut u8,
    };
    let mut output = Blob {
        size: 0,
        data: std::ptr::null_mut(),
    };
    unsafe {
        if CryptUnprotectData(
            &input,
            std::ptr::null_mut(),
            std::ptr::null(),
            std::ptr::null_mut(),
            std::ptr::null_mut(),
            1,
            &mut output,
        ) == 0
        {
            return Err("bootstrap decryption failed".into());
        };
        let bytes = std::slice::from_raw_parts_mut(output.data, output.size as usize);
        let result = bytes.to_vec();
        bytes.fill(0);
        LocalFree(output.data.cast());
        Ok(result)
    }
}
#[cfg(not(windows))]
fn unprotect(_: &[u8]) -> Result<Vec<u8>, String> {
    Err("Windows DPAPI required".into())
}

#[cfg(test)]
pub(crate) mod tests {
    use super::*;
    #[test]
    #[ignore]
    fn owned_exit_fixture() {
        let dir = PathBuf::from(
            std::env::var_os("MOVIEDESIGN_LIFECYCLE_FIXTURE").expect("explicit fixture directory"),
        );
        if std::env::var_os("MOVIEDESIGN_FIXTURE_GATEWAY").is_some() {
            use std::io::BufRead;
            let mut line = String::new();
            std::io::stdin().lock().read_line(&mut line).unwrap();
            assert!(line.contains("gateway.shutdown"));
            fs::write(dir.join("gateway-drained"), b"drained").unwrap();
            return;
        }
        let deadline = std::time::Instant::now() + std::time::Duration::from_secs(2);
        while !dir.join("gateway-drained").exists() && std::time::Instant::now() < deadline {
            std::thread::sleep(std::time::Duration::from_millis(10));
        }
        if dir.join("gateway-drained").exists() {
            fs::write(dir.join("opencode-exited"), b"natural exit after gateway").unwrap();
        }
    }
    pub(crate) fn spawn_exit_fixture(dir: &Path, gateway: bool) -> Child {
        let mut command = Command::new(std::env::current_exe().unwrap());
        hidden(&mut command);
        command
            .args([
                "--ignored",
                "--exact",
                "opencode::tests::owned_exit_fixture",
            ])
            .env("MOVIEDESIGN_LIFECYCLE_FIXTURE", dir)
            .stdin(Stdio::piped())
            .stdout(Stdio::null())
            .stderr(Stdio::null());
        if gateway {
            command.env("MOVIEDESIGN_FIXTURE_GATEWAY", "1");
        } else {
            command.env_remove("MOVIEDESIGN_FIXTURE_GATEWAY");
        }
        command.spawn().unwrap()
    }
    #[test]
    fn owned_fallback_waits_for_deadline_and_confirms_only_its_child() {
        let dir = std::env::temp_dir().join(format!(
            "moviedesign-owned-fallback-{}",
            crate::gateway::private_update_token().unwrap()
        ));
        fs::create_dir_all(&dir).unwrap();
        let mut child = spawn_exit_fixture(&dir, false);
        let started = Instant::now();
        assert!(finish_owned(&mut child, Duration::from_millis(60)).unwrap());
        assert!(started.elapsed() >= Duration::from_millis(60));
        assert!(started.elapsed() < Duration::from_secs(2));
        assert!(child.try_wait().unwrap().is_some());
        assert!(!dir.join("opencode-exited").exists());
        fs::remove_dir_all(dir).unwrap();
    }
    #[test]
    fn raw_config_interpolation_preserves_windows_paths_and_quotes() {
        for value in [r#"C:\app\tool-output\*"#, r#"synthetic-"key\value"#] {
            let expanded = format!(r#"{{"value":"{}"}}"#, config_value(value));
            assert_eq!(
                serde_json::from_str::<serde_json::Value>(&expanded).unwrap()["value"],
                value
            );
        }
    }
    #[test]
    fn occupied_agent_port_is_never_attached_or_stopped() {
        let owner = TcpListener::bind("127.0.0.1:0").unwrap();
        assert!(
            claim_port(owner.local_addr().unwrap().port()).is_err(),
            "must fail while unrelated owner holds port"
        );
        assert!(owner.local_addr().is_ok());
    }
    #[test]
    fn shutdown_cleans_bootstrap_without_name_kill() {
        let dir =
            std::env::temp_dir().join(format!("moviedesign_opencode_test_{}", std::process::id()));
        let _ = fs::create_dir_all(&dir);
        let dpapi = dir.join("launch.dpapi");
        let _ = fs::write(&dpapi, b"ephemeral");
        let mut host = Opencode {
            child: None,
            directory: dir.clone(),
        };
        host.shutdown().unwrap();
        assert!(!dpapi.exists());
        host.shutdown().unwrap();
        let _ = fs::remove_dir_all(&dir);
    }
}
