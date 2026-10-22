use base64::Engine as _;
use minisign_verify::{PublicKey, Signature};
use semver::Version;
use serde::{Deserialize, Serialize};
use serde_json::Value;
use std::io::{Read, Write};
use std::net::{SocketAddr, TcpStream};
use std::str::FromStr;
use std::time::Duration;
use tauri_plugin_updater::RemoteRelease;

pub const DEFAULT_POLICY_URL: &str =
    "https://raw.githubusercontent.com/newyngwieslash-ops/MovieDesign/main/updates/stable/policy.json";
pub const DEFAULT_MANIFEST_URL: &str =
    "https://github.com/newyngwieslash-ops/MovieDesign/releases/latest/download/latest.json";
pub const DEFAULT_RELEASES_URL: &str =
    "https://github.com/newyngwieslash-ops/MovieDesign/releases/latest";

/// Explicitly maps Rust OS identifiers (e.g. `windows`) to the recovered update policy
/// protocol platform identifiers (`win32`, `darwin`, `linux`). Never pass raw `"windows"`
/// into `manual_only.platforms` matching.
pub fn policy_platform(rust_os: &str) -> &'static str {
    match rust_os.trim().to_ascii_lowercase().as_str() {
        "windows" | "win32" => "win32",
        "macos" | "darwin" => "darwin",
        "linux" => "linux",
        _ => "unknown",
    }
}

pub fn current_policy_platform() -> &'static str {
    policy_platform(std::env::consts::OS)
}

pub fn allowed_release_asset_url(url: &str, allow_loopback: bool) -> bool {
    let Ok(url) = tauri::Url::parse(url) else {
        return false;
    };
    if !url.username().is_empty() || url.password().is_some() || url.fragment().is_some() {
        return false;
    }
    (url.scheme() == "https"
        && url.host_str() == Some("github.com")
        && url.port().is_none()
        && url
            .path()
            .starts_with("/newyngwieslash-ops/MovieDesign/releases/download/")
        && !url.path().contains("%"))
        || (cfg!(test)
            && allow_loopback
            && url.scheme() == "http"
            && matches!(url.host_str(), Some("127.0.0.1" | "localhost")))
}

#[derive(Debug, Clone, Deserialize, Serialize, PartialEq, Eq)]
#[serde(rename_all = "camelCase")]
pub struct ResolvedPolicyWire {
    pub forced: bool,
    pub target_version: Option<String>,
    pub min_supported_version: String,
    pub required_reason: Option<String>,
    pub manual_download_url: Option<String>,
    pub manual_only: bool,
    pub manual_recovery_reason: Option<String>,
}

#[derive(Debug, Clone, Deserialize, Serialize, PartialEq, Eq)]
#[serde(rename_all = "camelCase")]
pub struct GatewayPolicySnapshot {
    #[serde(flatten)]
    pub metadata: serde_json::Map<String, Value>,
    pub status: String,
    pub source: String,
    pub stale: bool,
    #[serde(default)]
    pub clock_skew: bool,
    pub policy_cache_age_ms: Option<i64>,
    pub policy_fallback_reason: Option<String>,
    pub current_version: String,
    pub platform: String,
    pub policy_url: String,
    pub manifest_url: String,
    pub releases_url: String,
    pub resolved: ResolvedPolicyWire,
    pub busy: bool,
    pub busy_reason: Option<String>,
    pub auto_update_allowed: bool,
}

#[derive(Debug, Clone, Deserialize, Serialize, PartialEq, Eq)]
#[serde(rename_all = "camelCase")]
pub struct NativeUpdaterGateReport {
    #[serde(skip_serializing_if = "Option::is_none")]
    pub candidate_id: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub policy: Option<GatewayPolicySnapshot>,
    pub platform: String,
    pub current_version: String,
    pub manifest_url: String,
    pub releases_url: String,
    pub policy_status: String,
    pub manual_only: bool,
    pub busy: bool,
    pub auto_install_allowed: bool,
    pub manifest_version: Option<String>,
    pub signature_verified: bool,
    pub action: String,
    pub reason: Option<String>,
}

pub fn evaluate_policy_gate(snap: &GatewayPolicySnapshot) -> NativeUpdaterGateReport {
    let platform = policy_platform(&snap.platform).to_string();
    let releases_url = snap
        .resolved
        .manual_download_url
        .clone()
        .filter(|u| !u.trim().is_empty())
        .unwrap_or_else(|| {
            if snap.releases_url.trim().is_empty() {
                DEFAULT_RELEASES_URL.to_string()
            } else {
                snap.releases_url.clone()
            }
        });
    let manifest_url = if snap.manifest_url.trim().is_empty() {
        DEFAULT_MANIFEST_URL.to_string()
    } else {
        snap.manifest_url.clone()
    };

    if snap.status != "ready" {
        return NativeUpdaterGateReport {
            candidate_id: None,
            policy: Some(snap.clone()),
            platform,
            current_version: snap.current_version.clone(),
            manifest_url,
            releases_url,
            policy_status: snap.status.clone(),
            manual_only: false,
            busy: snap.busy,
            auto_install_allowed: false,
            manifest_version: None,
            signature_verified: false,
            action: "manual_fallback".into(),
            reason: Some(format!(
                "更新策略未就绪（{}），未伪造服务端 manual_only 规则，仅保留手动 Releases 入口",
                snap.policy_fallback_reason
                    .as_deref()
                    .unwrap_or("policy_unavailable")
            )),
        };
    }

    if snap.resolved.manual_only {
        return NativeUpdaterGateReport {
            candidate_id: None,
            policy: Some(snap.clone()),
            platform,
            current_version: snap.current_version.clone(),
            manifest_url,
            releases_url,
            policy_status: snap.status.clone(),
            manual_only: true,
            busy: snap.busy,
            auto_install_allowed: false,
            manifest_version: None,
            signature_verified: false,
            action: "manual_only_blocked".into(),
            reason: snap
                .resolved
                .required_reason
                .clone()
                .or_else(|| snap.resolved.manual_recovery_reason.clone())
                .or_else(|| Some("命中 manual_only 规则，已阻止自动下载与安装".into())),
        };
    }

    if snap.busy || !snap.auto_update_allowed {
        return NativeUpdaterGateReport {
            candidate_id: None,
            policy: Some(snap.clone()),
            platform,
            current_version: snap.current_version.clone(),
            manifest_url,
            releases_url,
            policy_status: snap.status.clone(),
            manual_only: false,
            busy: true,
            auto_install_allowed: false,
            manifest_version: None,
            signature_verified: false,
            action: "busy_refused".into(),
            reason: snap
                .busy_reason
                .clone()
                .or_else(|| Some("存在活动生成任务或未完成操作，拒绝安装更新或重启".into())),
        };
    }

    NativeUpdaterGateReport {
        candidate_id: None,
        policy: Some(snap.clone()),
        platform,
        current_version: snap.current_version.clone(),
        manifest_url,
        releases_url,
        policy_status: snap.status.clone(),
        manual_only: false,
        busy: false,
        auto_install_allowed: true,
        manifest_version: None,
        signature_verified: false,
        action: "ready_for_signed_update".into(),
        reason: snap.resolved.required_reason.clone(),
    }
}

fn decode_base64_utf8(input: &str) -> Result<String, String> {
    let bytes = base64::engine::general_purpose::STANDARD
        .decode(input.trim())
        .map_err(|e| format!("base64 解码失败: {e}"))?;
    String::from_utf8(bytes).map_err(|e| format!("签名 UTF-8 格式无效: {e}"))
}

fn verify_minisign_and_version(
    package_bytes: &[u8],
    release_signature_base64: &str,
    pubkey_base64: &str,
    announced_version: &Version,
) -> Result<(), String> {
    let pubkey_str = decode_base64_utf8(pubkey_base64)?;
    let public_key = PublicKey::decode(&pubkey_str).map_err(|e| format!("无效的更新公钥: {e}"))?;
    let sig_str = decode_base64_utf8(release_signature_base64)?;
    let signature = Signature::decode(&sig_str).map_err(|e| format!("安装包签名格式损坏: {e}"))?;

    public_key
        .verify(package_bytes, &signature, true)
        .map_err(|e| format!("安装包签名校验失败: {e}"))?;

    let signed_ver_raw = signature
        .trusted_comment()
        .split('\t')
        .find_map(|field| field.strip_prefix("version:"))
        .ok_or_else(|| "安装包签名缺少受信任的 version 字段".to_string())?;
    let signed_ver = Version::from_str(signed_ver_raw.trim_start_matches('v'))
        .map_err(|e| format!("签名中的版本号无效: {e}"))?;
    if &signed_ver != announced_version {
        return Err(format!(
            "签名绑定版本 ({signed_ver}) 与 manifest 声明版本 ({announced_version}) 不一致"
        ));
    }
    Ok(())
}

/// Verifies a static update manifest (`tauri_plugin_updater::RemoteRelease`) and package bytes
/// using the plugin's minisign signature + signed-version protocol after enforcing the policy gate.
pub fn verify_gated_manifest_and_package(
    snap: &GatewayPolicySnapshot,
    pubkey_base64: &str,
    target: &str,
    manifest_json: Value,
    package_bytes: &[u8],
    allow_loopback_asset: bool,
) -> Result<NativeUpdaterGateReport, String> {
    let mut report = evaluate_policy_gate(snap);
    if !report.auto_install_allowed {
        return Err(report
            .reason
            .unwrap_or_else(|| "更新策略门禁阻止了自动下载与安装".into()));
    }

    let current_ver = Version::from_str(snap.current_version.trim().trim_start_matches('v'))
        .map_err(|e| format!("当前版本号无效: {e}"))?;
    let release: RemoteRelease = serde_json::from_value(manifest_json)
        .map_err(|e| format!("解析签名更新 manifest 失败: {e}"))?;

    if release.version <= current_ver {
        return Err("当前版本已是最新，无需升级".into());
    }

    let download_url = release
        .download_url(target)
        .map_err(|e| format!("manifest 缺少目标平台 {target} 的下载地址: {e}"))?;
    let signature = release
        .signature(target)
        .map_err(|e| format!("manifest 缺少目标平台 {target} 的签名: {e}"))?;

    if !allowed_release_asset_url(download_url.as_str(), allow_loopback_asset) {
        return Err(format!("拒绝非本仓库 Release 资产地址: {download_url}"));
    }

    verify_minisign_and_version(package_bytes, signature, pubkey_base64, &release.version)?;

    report.manifest_version = Some(release.version.to_string());
    report.signature_verified = true;
    report.action = "verified_ready_to_install".into();
    Ok(report)
}

// HTTP/1.0 plus Connection: close avoids chunked framing on this private loopback
// channel. No bearer token is ever returned to the WebView or persisted to disk.
pub fn gateway_request(
    port: u16,
    origin: &str,
    token: &str,
    path: &str,
    body: Option<&Value>,
) -> Result<Value, String> {
    let addr = SocketAddr::from(([127, 0, 0, 1], port));
    let mut stream =
        TcpStream::connect_timeout(&addr, Duration::from_secs(3)).map_err(|e| e.to_string())?;
    stream
        .set_read_timeout(Some(Duration::from_secs(40)))
        .map_err(|e| e.to_string())?;
    stream
        .set_write_timeout(Some(Duration::from_secs(3)))
        .map_err(|e| e.to_string())?;
    let payload = body.map(Value::to_string).unwrap_or_default();
    let method = if body.is_some() { "POST" } else { "GET" };
    let req = format!("{method} {path} HTTP/1.0\r\nHost: 127.0.0.1:{port}\r\nOrigin: {origin}\r\nX-MovieDesign-Update: {token}\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{payload}", payload.len());
    stream
        .write_all(req.as_bytes())
        .map_err(|e| e.to_string())?;
    let mut raw = String::new();
    stream
        .take(1024 * 1024)
        .read_to_string(&mut raw)
        .map_err(|e| e.to_string())?;
    let (headers, body) = raw.split_once("\r\n\r\n").ok_or("本地更新控制响应不完整")?;
    let data: Value =
        serde_json::from_str(body).map_err(|e| format!("本地更新响应格式错误: {e}"))?;
    if headers
        .lines()
        .next()
        .and_then(|s| s.split_whitespace().nth(1))
        != Some("200")
    {
        return Err(data["error"]["message"]
            .as_str()
            .unwrap_or("本地更新控制拒绝请求")
            .into());
    }
    Ok(data)
}

pub fn fetch_gateway_policy_snapshot(
    port: u16,
    origin: &str,
    current_version: &str,
) -> Result<GatewayPolicySnapshot, String> {
    let version = Version::parse(current_version).map_err(|e| e.to_string())?;
    let path = format!(
        "/api/updates/policy?version={version}&platform={}&locale=zh-CN",
        current_policy_platform()
    );
    serde_json::from_value(gateway_request(port, origin, "", &path, None)?)
        .map_err(|e| e.to_string())
}

#[cfg(test)]
pub(crate) mod tests {
    use super::*;
    use serde_json::json;

    // Public key and signed payload vector (`version:2.0.0`, payload `b"test"`)
    // matching tauri-plugin-updater's public minisign format. Never contains a private key.
    pub(crate) const PUBLIC_KEY_RAW: &str = "untrusted comment: minisign public key: A49EF4746FA6E6D7\nRWTX5qZvdPSepDGWg2bz8n6ArQnBMwgQK1geVosorcbLzcunD+KJ+cYe";
    pub(crate) const SIGNATURE_RAW: &str = "untrusted comment: signature from tauri secret key\nRUTX5qZvdPSepPF2zsjvP/L/TQtBLRnWRvaq+4omK1UEugtQp/USrGiI1v762sDy3id3aor7bRC9ETyeot6lMHHrWoZ5hjUM1Qc=\ntrusted comment: timestamp:1791649051\tfile:payload\tversion:2.0.0\nDDq2CLOEl5cQmC2eJRHlnZPVViOZsu1CjpPKQoei4A/XBfkDqGO0B+QMrcrHgEQ5qV3mne75tdFWhJpqUXh9AA==";

    fn encode_b64(raw: &str) -> String {
        base64::engine::general_purpose::STANDARD.encode(raw)
    }

    fn sample_snapshot() -> GatewayPolicySnapshot {
        GatewayPolicySnapshot {
            metadata: serde_json::Map::new(),
            status: "ready".into(),
            source: "cache".into(),
            stale: false,
            clock_skew: false,
            policy_cache_age_ms: Some(1200),
            policy_fallback_reason: None,
            current_version: "0.1.0".into(),
            platform: "windows".into(),
            policy_url: DEFAULT_POLICY_URL.into(),
            manifest_url: DEFAULT_MANIFEST_URL.into(),
            releases_url: DEFAULT_RELEASES_URL.into(),
            resolved: ResolvedPolicyWire {
                forced: false,
                target_version: None,
                min_supported_version: "0.1.0".into(),
                required_reason: None,
                manual_download_url: Some(DEFAULT_RELEASES_URL.into()),
                manual_only: false,
                manual_recovery_reason: None,
            },
            busy: false,
            busy_reason: None,
            auto_update_allowed: true,
        }
    }

    #[test]
    fn windows_maps_to_win32_and_never_misjudges_windows_literal() {
        assert_eq!(policy_platform("windows"), "win32");
        assert_eq!(policy_platform("WINDOWS"), "win32");
        assert_eq!(policy_platform("win32"), "win32");
        assert_eq!(policy_platform("macos"), "darwin");
        assert_eq!(policy_platform("darwin"), "darwin");
        assert_eq!(policy_platform("linux"), "linux");
        assert_eq!(policy_platform("android"), "unknown");
    }

    #[test]
    fn policy_gate_blocks_manual_only_failed_policy_and_busy_tasks() {
        // 1. Ready + idle + not manual_only -> allowed
        let ready = sample_snapshot();
        let report_ready = evaluate_policy_gate(&ready);
        assert!(report_ready.auto_install_allowed);
        assert_eq!(report_ready.platform, "win32");
        assert_eq!(report_ready.action, "ready_for_signed_update");

        // 2. manual_only hit -> blocked, points to GitHub Releases
        let mut manual = sample_snapshot();
        manual.resolved.forced = true;
        manual.resolved.manual_only = true;
        manual.resolved.required_reason = Some("请手动下载签名安装包".into());
        manual.auto_update_allowed = false;
        let report_manual = evaluate_policy_gate(&manual);
        assert!(!report_manual.auto_install_allowed);
        assert!(report_manual.manual_only);
        assert_eq!(report_manual.action, "manual_only_blocked");
        assert_eq!(report_manual.releases_url, DEFAULT_RELEASES_URL);

        // 3. Policy fetch failed without LKG -> blocked, does NOT fabricate manual_only
        let mut failed = sample_snapshot();
        failed.status = "failed".into();
        failed.source = "none".into();
        failed.policy_fallback_reason = Some("network_error".into());
        failed.auto_update_allowed = false;
        let report_failed = evaluate_policy_gate(&failed);
        assert!(!report_failed.auto_install_allowed);
        assert!(!report_failed.manual_only);
        assert_eq!(report_failed.action, "manual_fallback");

        // 4. Busy gateway (active task/turn) -> refuses install
        let mut busy = sample_snapshot();
        busy.busy = true;
        busy.busy_reason = Some("生成任务运行中，禁止重启".into());
        busy.auto_update_allowed = false;
        let report_busy = evaluate_policy_gate(&busy);
        assert!(!report_busy.auto_install_allowed);
        assert!(report_busy.busy);
        assert_eq!(report_busy.action, "busy_refused");
    }

    #[test]
    fn signature_verification_enforces_minisign_signed_version_and_repo_boundary() {
        let pubkey_b64 = encode_b64(PUBLIC_KEY_RAW);
        let valid_sig_b64 = encode_b64(SIGNATURE_RAW);
        let valid_manifest = json!({
            "version": "2.0.0",
            "notes": "Signed release 2.0.0",
            "pub_date": "2026-10-15T00:00:00Z",
            "platforms": {
                "windows-x86_64": {
                    "url": "https://github.com/newyngwieslash-ops/MovieDesign/releases/download/v2.0.0/MovieDesign_2.0.0_x64-setup.exe",
                    "signature": valid_sig_b64
                }
            }
        });

        let snap = sample_snapshot();
        let ok_report = verify_gated_manifest_and_package(
            &snap,
            &pubkey_b64,
            "windows-x86_64",
            valid_manifest.clone(),
            b"test",
            false,
        )
        .expect("valid signed package should pass verification");
        assert!(ok_report.signature_verified);
        assert_eq!(ok_report.manifest_version.as_deref(), Some("2.0.0"));

        // Tampered package bytes must fail signature verification.
        let err_tampered = verify_gated_manifest_and_package(
            &snap,
            &pubkey_b64,
            "windows-x86_64",
            valid_manifest.clone(),
            b"tampered-installer-bytes",
            false,
        )
        .expect_err("tampered bytes must be rejected");
        assert!(err_tampered.contains("签名校验失败"));

        // Corrupted signature in manifest must fail.
        let bad_sig_manifest = json!({
            "version": "2.0.0",
            "platforms": {
                "windows-x86_64": {
                    "url": "https://github.com/newyngwieslash-ops/MovieDesign/releases/download/v2.0.0/MovieDesign_2.0.0_x64-setup.exe",
                    "signature": "corrupted-base64-signature"
                }
            }
        });
        assert!(verify_gated_manifest_and_package(
            &snap,
            &pubkey_b64,
            "windows-x86_64",
            bad_sig_manifest,
            b"test",
            false,
        )
        .is_err());

        // Announced version mismatch against signed trusted comment version must fail.
        let version_mismatch_manifest = json!({
            "version": "2.0.1",
            "platforms": {
                "windows-x86_64": {
                    "url": "https://github.com/newyngwieslash-ops/MovieDesign/releases/download/v2.0.1/MovieDesign_2.0.1_x64-setup.exe",
                    "signature": valid_sig_b64
                }
            }
        });
        let err_mismatch = verify_gated_manifest_and_package(
            &snap,
            &pubkey_b64,
            "windows-x86_64",
            version_mismatch_manifest,
            b"test",
            false,
        )
        .expect_err("version mismatch must fail");
        assert!(err_mismatch.contains("不一致"));

        // Non-MovieDesign repository download URL must be rejected before install.
        let foreign_url_manifest = json!({
            "version": "2.0.0",
            "platforms": {
                "windows-x86_64": {
                    "url": "https://evil.example.com/MovieDesign_2.0.0_x64-setup.exe",
                    "signature": valid_sig_b64
                }
            }
        });
        let err_foreign = verify_gated_manifest_and_package(
            &snap,
            &pubkey_b64,
            "windows-x86_64",
            foreign_url_manifest,
            b"test",
            false,
        )
        .expect_err("foreign URL must be rejected");
        assert!(err_foreign.contains("拒绝非本仓库 Release 资产地址"));

        // Busy gateway must refuse verification/install before touching package.
        let mut busy_snap = sample_snapshot();
        busy_snap.busy = true;
        busy_snap.auto_update_allowed = false;
        assert!(verify_gated_manifest_and_package(
            &busy_snap,
            &pubkey_b64,
            "windows-x86_64",
            valid_manifest,
            b"test",
            false,
        )
        .is_err());
    }
}
