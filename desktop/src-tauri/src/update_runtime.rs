use crate::{gateway::Gateway, updates::*};
use serde::Serialize;
use serde_json::{json, Value};
use std::sync::Mutex;
use tauri::{ipc::Channel, AppHandle, Manager, State};
use tauri_plugin_updater::{Update, Updater, UpdaterExt};

#[derive(Clone)]
struct Candidate {
    id: String,
    update: Update,
    receipt: Option<String>,
    consumed: bool,
}
#[derive(Default)]
pub struct UpdateRuntime {
    candidate: Option<Candidate>,
    busy: bool,
}

// The guard serializes candidate transitions, never network I/O under the mutex.
struct Operation<'a>(&'a Mutex<UpdateRuntime>);
impl Drop for Operation<'_> {
    fn drop(&mut self) {
        if let Ok(mut s) = self.0.lock() {
            s.busy = false;
        }
    }
}
fn begin(state: &Mutex<UpdateRuntime>) -> Result<Operation<'_>, String> {
    let mut s = state.lock().map_err(|_| "更新状态锁不可用")?;
    if s.busy {
        return Err("已有更新操作进行中".into());
    }
    s.busy = true;
    Ok(Operation(state))
}
type Connection = (u16, String, &'static str);
fn connection(app: &AppHandle) -> Result<Connection, String> {
    app.state::<Mutex<Gateway>>()
        .lock()
        .map_err(|_| "本地服务状态锁不可用")?
        .update_connection()
}
async fn control(conn: &Connection, path: &str, body: Value) -> Result<Value, String> {
    let (port, token, origin) = conn.clone();
    let path = path.to_owned();
    tauri::async_runtime::spawn_blocking(move || {
        gateway_request(port, origin, &token, &path, Some(&body))
    })
    .await
    .map_err(|e| e.to_string())?
}
async fn policy(conn: &Connection, version: String) -> Result<GatewayPolicySnapshot, String> {
    let (port, _, origin) = conn.clone();
    tauri::async_runtime::spawn_blocking(move || {
        fetch_gateway_policy_snapshot(port, origin, &version)
    })
    .await
    .map_err(|e| e.to_string())?
}

pub async fn checked_candidate(
    updater: Updater,
    allow_test_loopback: bool,
) -> Result<Option<Update>, String> {
    let candidate = updater
        .check()
        .await
        .map_err(|e| format!("检查签名 manifest 失败：{e}"))?;
    if let Some(u) = &candidate {
        if !allowed_release_asset_url(u.download_url.as_str(), allow_test_loopback) {
            return Err("拒绝非本仓库 Release 资产地址".into());
        }
        if semver::Version::parse(&u.version).map_err(|e| e.to_string())?
            <= semver::Version::parse(&u.current_version).map_err(|e| e.to_string())?
        {
            return Err("候选版本未高于运行版本".into());
        }
    }
    Ok(candidate)
}

#[tauri::command]
pub async fn updater_check_and_gate(
    app: AppHandle,
    state: State<'_, Mutex<UpdateRuntime>>,
    request_id: String,
) -> Result<NativeUpdaterGateReport, String> {
    let _operation = begin(&state)?;
    state.lock().map_err(|_| "更新状态锁不可用")?.candidate = None;
    let conn = connection(&app)?;
    let version = app.package_info().version.to_string();
    let snap: GatewayPolicySnapshot = serde_json::from_value(control(&conn, "/api/updates/check", json!({"requestId":request_id,"version":version,"platform":current_policy_platform(),"locale":"zh-CN","refresh":true})).await?).map_err(|e| e.to_string())?;
    let mut report = evaluate_policy_gate(&snap);
    if !report.auto_install_allowed {
        return Ok(report);
    }
    // The only production endpoint is native-owned. Frontend and gateway metadata
    // cannot choose a manifest, key, installer bytes, or insecure transport.
    let owner = app.clone();
    let updater = app
        .updater_builder()
        .endpoints(vec![DEFAULT_MANIFEST_URL
            .parse::<tauri::Url>()
            .map_err(|e| e.to_string())?])
        .map_err(|e| e.to_string())?
        .on_before_exit(move || {
            if let Ok(mut gateway) = owner.state::<Mutex<Gateway>>().lock() {
                gateway.shutdown();
            }
            owner.cleanup_before_exit();
        })
        .build()
        .map_err(|e| e.to_string())?;
    if let Some(update) = checked_candidate(updater, false).await? {
        let id = crate::gateway::private_update_token()?;
        report.candidate_id = Some(id.clone());
        report.manifest_version = Some(update.version.clone());
        report.action = "candidate_ready".into();
        state.lock().map_err(|_| "更新状态锁不可用")?.candidate = Some(Candidate {
            id,
            update,
            receipt: None,
            consumed: false,
        });
    } else {
        report.action = "up_to_date".into();
        report.auto_install_allowed = false;
    }
    Ok(report)
}

fn current(state: &Mutex<UpdateRuntime>, id: &str) -> Result<Candidate, String> {
    state
        .lock()
        .map_err(|_| "更新状态锁不可用")?
        .candidate
        .clone()
        .filter(|c| c.id == id && !c.consumed)
        .ok_or_else(|| "候选已失效，请重新检查更新".into())
}

#[tauri::command]
pub async fn updater_decide(
    app: AppHandle,
    state: State<'_, Mutex<UpdateRuntime>>,
    candidate_id: String,
    request_id: String,
    decision: String,
) -> Result<Value, String> {
    let _operation = begin(&state)?;
    let candidate = current(&state, &candidate_id)?;
    if decision != "approve" && decision != "reject" {
        return Err("必须明确批准或拒绝".into());
    }
    let receipt = control(&connection(&app)?, "/internal/updates/decision", json!({"requestId":request_id,"candidateId":candidate.id,"version":candidate.update.version,"currentVersion":candidate.update.current_version,"decision":decision})).await?;
    let mut s = state.lock().map_err(|_| "更新状态锁不可用")?;
    let c = s.candidate.as_mut().ok_or("候选已失效")?;
    c.receipt = Some(request_id);
    c.consumed = decision == "reject";
    Ok(receipt)
}

#[derive(Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct Progress {
    stage: &'static str,
    downloaded: u64,
    total: Option<u64>,
}

// Both production and the controlled installation sink use this exact ordering.
// Update.download and Update.install perform the official signature/version checks.
async fn download_then_install<F, Fut, I>(
    update: &Update,
    progress: impl Fn(Progress),
    admit: F,
    install: I,
) -> Result<(), String>
where
    F: FnOnce() -> Fut,
    Fut: std::future::Future<Output = Result<(), String>>,
    I: FnOnce(&Update, &[u8]) -> Result<(), String>,
{
    let mut downloaded = 0;
    let bytes = update
        .download(
            |n, total| {
                downloaded += n as u64;
                progress(Progress {
                    stage: "downloading",
                    downloaded,
                    total,
                });
            },
            || {},
        )
        .await
        .map_err(|e| format!("下载或官方签名验证失败：{e}"))?;
    progress(Progress {
        stage: "verified",
        downloaded,
        total: Some(downloaded),
    });
    admit().await?;
    progress(Progress {
        stage: "install_started",
        downloaded,
        total: Some(downloaded),
    });
    install(update, &bytes)
}

#[tauri::command]
pub async fn updater_install(
    app: AppHandle,
    state: State<'_, Mutex<UpdateRuntime>>,
    candidate_id: String,
    receipt_id: String,
    request_id: String,
    on_progress: Channel<Progress>,
) -> Result<Value, String> {
    let _operation = begin(&state)?;
    let candidate = current(&state, &candidate_id)?;
    if candidate.receipt.as_deref() != Some(&receipt_id)
        || candidate.update.current_version != app.package_info().version.to_string()
    {
        return Err("批准回执或运行版本不匹配".into());
    }
    state
        .lock()
        .map_err(|_| "更新状态锁不可用")?
        .candidate
        .as_mut()
        .ok_or("候选已失效")?
        .consumed = true;
    let conn = connection(&app)?;
    let gate = evaluate_policy_gate(&policy(&conn, app.package_info().version.to_string()).await?);
    if !gate.auto_install_allowed {
        return Err(gate.reason.unwrap_or_else(|| "当前策略禁止安装".into()));
    }
    let result = download_then_install(&candidate.update, |p| { let _ = on_progress.send(p); }, || async {
        let gate = evaluate_policy_gate(&policy(&conn, app.package_info().version.to_string()).await?);
        if !gate.auto_install_allowed { return Err(gate.reason.unwrap_or_else(|| "最终策略禁止安装".into())); }
        control(&conn, "/internal/updates/admit", json!({"requestId":request_id,"candidateId":candidate.id,"receiptId":receipt_id,"version":candidate.update.version,"currentVersion":candidate.update.current_version})).await?;
        Ok(())
    }, |update, bytes| update.install(bytes).map_err(|e| e.to_string())).await;
    if let Err(error) = result {
        // A timeout may have committed the intent. Query/release its reservation,
        // but never repeat installation or replace the original identity.
        let _ = control(
            &conn,
            "/internal/updates/release",
            json!({"requestId":format!("release-{request_id}"),"receiptId":request_id}),
        )
        .await;
        return Err(error);
    }
    Ok(json!({"status":"install_started","requestId":request_id}))
}

#[tauri::command]
pub async fn updater_policy(app: AppHandle) -> Result<Value, String> {
    let conn = connection(&app)?;
    let mut snapshot =
        serde_json::to_value(policy(&conn, app.package_info().version.to_string()).await?)
            .map_err(|e| e.to_string())?;
    let history = control(
        &conn,
        "/internal/updates/history",
        json!({"requestId":"history"}),
    )
    .await?;
    snapshot["updateHistory"] = history["items"].clone();
    Ok(snapshot)
}

#[tauri::command]
pub async fn updater_receipt(
    app: AppHandle,
    receipt_id: String,
    decision_id: Option<String>,
    request_id: String,
) -> Result<Value, String> {
    control(&connection(&app)?, "/internal/updates/receipt", json!({"requestId":request_id,"receiptId":receipt_id,"decisionReceiptId":decision_id.unwrap_or_default()})).await
}

#[tauri::command]
pub async fn updater_discard(
    app: AppHandle,
    state: State<'_, Mutex<UpdateRuntime>>,
    candidate_id: String,
    receipt_id: String,
    decision_id: String,
    request_id: String,
) -> Result<(), String> {
    let _operation = begin(&state)?;
    let receipt = control(
        &connection(&app)?,
        "/internal/updates/receipt",
        json!({"requestId":request_id,"receiptId":receipt_id,"decisionReceiptId":decision_id}),
    )
    .await?;
    if receipt["candidateId"].as_str() != Some(&candidate_id)
        || !matches!(receipt["status"].as_str(), Some("approve" | "reject"))
    {
        return Err("已存在安装意图或未知结果，保留原请求并核对安装状态".into());
    }
    let mut runtime = state.lock().map_err(|_| "更新状态锁不可用")?;
    if runtime
        .candidate
        .as_ref()
        .is_some_and(|c| c.id == candidate_id)
    {
        runtime.candidate = None;
    }
    Ok(())
}
