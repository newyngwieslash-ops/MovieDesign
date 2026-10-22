#![allow(dead_code)]
// Include the production modules directly: exercise private runtime sequencing
// without adding a public API or an installer injection point to the application.
#[path = "../src/gateway.rs"]
mod gateway;
#[path = "../src/opencode.rs"]
mod opencode;
#[path = "../src/updates.rs"]
mod updates;
mod update_runtime {
    include!("../src/update_runtime.rs");

    #[cfg(test)]
    mod tests {
        use super::*;
        use crate::updates::tests::{PUBLIC_KEY_RAW, SIGNATURE_RAW};
        use base64::Engine as _;
        use std::{
            io::{Read, Write},
            net::{TcpListener, TcpStream},
            sync::{
                atomic::{AtomicBool, AtomicUsize, Ordering},
                Arc,
            },
            thread,
            time::Duration,
        };

        struct Fixture {
            url: String,
            stop: Arc<AtomicBool>,
            thread: Option<thread::JoinHandle<()>>,
            hits: Arc<AtomicUsize>,
        }
        impl Fixture {
            fn new(version: &str, payload: &[u8], source: Option<&str>) -> Self {
                let listener = TcpListener::bind("127.0.0.1:0").unwrap();
                let url = format!("http://{}", listener.local_addr().unwrap());
                let manifest = json!({"version":version,"url":source.map(str::to_owned).unwrap_or_else(|| format!("{url}/package")),"signature":base64::engine::general_purpose::STANDARD.encode(SIGNATURE_RAW)}).to_string();
                let payload = payload.to_vec();
                let stop = Arc::new(AtomicBool::new(false));
                let signal = stop.clone();
                let hits = Arc::new(AtomicUsize::new(0));
                let requests = hits.clone();
                let worker = thread::spawn(move || {
                    for conn in listener.incoming() {
                        if signal.load(Ordering::SeqCst) {
                            break;
                        }
                        let mut conn = conn.unwrap();
                        conn.set_read_timeout(Some(Duration::from_secs(2))).unwrap();
                        let mut header = Vec::new();
                        let mut b = [0];
                        while !header.ends_with(b"\r\n\r\n") {
                            if conn.read(&mut b).unwrap_or(0) == 0 {
                                break;
                            }
                            header.push(b[0]);
                        }
                        requests.fetch_add(1, Ordering::SeqCst);
                        let body = if header.starts_with(b"GET /package ") {
                            &payload[..]
                        } else {
                            manifest.as_bytes()
                        };
                        write!(conn, "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n", body.len()).unwrap();
                        conn.write_all(body).unwrap();
                    }
                });
                Self {
                    url,
                    stop,
                    thread: Some(worker),
                    hits,
                }
            }
            fn updater(&self) -> Updater {
                let mut context = tauri::test::mock_context(tauri::test::noop_assets());
                context.config_mut().plugins.0.insert("updater".into(), json!({"pubkey":base64::engine::general_purpose::STANDARD.encode(PUBLIC_KEY_RAW),"requireSignedVersion":true,"dangerousInsecureTransportProtocol":true}));
                let app = tauri::test::mock_builder()
                    .plugin(tauri_plugin_updater::Builder::new().build())
                    .build(context)
                    .unwrap();
                app.updater_builder()
                    .target("windows-x86_64")
                    .no_proxy()
                    .endpoints(vec![format!("{}/manifest", self.url).parse().unwrap()])
                    .unwrap()
                    .build()
                    .unwrap()
            }
        }
        impl Drop for Fixture {
            fn drop(&mut self) {
                self.stop.store(true, Ordering::SeqCst);
                let _ = TcpStream::connect(self.url.trim_start_matches("http://"));
                self.thread.take().unwrap().join().unwrap();
            }
        }

        #[test]
        fn official_http_download_signed_version_and_controlled_install_sink() {
            tauri::async_runtime::block_on(async {
                for (version, payload, admission, expected) in [
                    ("2.0.0", &b"test"[..], true, 1),
                    ("2.0.0", &b"tampered"[..], true, 0),
                    ("3.0.0", &b"test"[..], true, 0),
                    ("2.0.0", &b"test"[..], false, 0),
                ] {
                    let fixture = Fixture::new(version, payload, None);
                    let update = checked_candidate(fixture.updater(), true)
                        .await
                        .unwrap()
                        .unwrap();
                    let calls = AtomicUsize::new(0);
                    let order = Mutex::new(vec!["human_receipt_saved"]);
                    let result = download_then_install(
                        &update,
                        |p| {
                            if p.stage == "verified" {
                                order.lock().unwrap().push("downloaded_and_verified");
                            }
                        },
                        || async {
                            if !admission {
                                return Err("busy/manual_only/policy refusal fixture".into());
                            }
                            order.lock().unwrap().push("exclusive_admission");
                            Ok(())
                        },
                        |u, bytes| {
                            u.verify(bytes).map_err(|e| e.to_string())?;
                            assert_eq!(bytes, b"test");
                            calls.fetch_add(1, Ordering::SeqCst);
                            order.lock().unwrap().push("install_started");
                            Ok(())
                        },
                    )
                    .await;
                    assert_eq!(calls.load(Ordering::SeqCst), expected);
                    assert_eq!(result.is_ok(), expected == 1);
                    assert_eq!(
                        fixture.hits.load(Ordering::SeqCst),
                        2,
                        "real plugin check and download HTTP requests"
                    );
                    if expected == 1 {
                        assert_eq!(
                            *order.lock().unwrap(),
                            [
                                "human_receipt_saved",
                                "downloaded_and_verified",
                                "exclusive_admission",
                                "install_started"
                            ]
                        );
                    }
                }
            });
        }
        #[test]
        fn native_source_boundary_rejects_cdn_foreign_repo_and_production_loopback() {
            tauri::async_runtime::block_on(async {
                for source in ["https://objects.githubusercontent.com/unrelated.exe", "https://github.com/other/Other/releases/download/v2/setup.exe", "http://127.0.0.1:8000/setup.exe", "https://github.com/newyngwieslash-ops/MovieDesign/releases/download/%2e%2e/setup.exe"] {
                let fixture=Fixture::new("2.0.0",b"test",Some(source));
                assert!(checked_candidate(fixture.updater(),false).await.is_err());
                assert_eq!(fixture.hits.load(Ordering::SeqCst),1,"rejected source never downloads");
            }
            });
        }
        #[test]
        fn candidate_transition_lock_never_allows_parallel_operations() {
            let runtime = Mutex::new(UpdateRuntime::default());
            let lease = begin(&runtime).unwrap();
            assert!(begin(&runtime).is_err());
            drop(lease);
            assert!(begin(&runtime).is_ok());
            assert!(current(&runtime, "stale-candidate").is_err());
        }
    }
}
