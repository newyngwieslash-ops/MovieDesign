# MovieDesign 签名发布与更新策略指南 (`releasing.md`)

## 1. 首发受支持平台与托管落点

- **首发完整验收平台**：Windows x64（Rust target `x86_64-pc-windows-msvc`，更新策略平台名显式映射为 `win32`）。其他平台未通过完整验收矩阵前不声明支持。
- **GitHub 仓库限定**：`newyngwieslash-ops/MovieDesign`。
- **更新策略地址（可独立于安装包提交调整）**：
  - `https://raw.githubusercontent.com/newyngwieslash-ops/MovieDesign/main/updates/stable/policy.json`
- **静态签名更新 Manifest 地址**：
  - `https://github.com/newyngwieslash-ops/MovieDesign/releases/latest/download/latest.json`
- **手动下载入口（GitHub Releases）**：
  - `https://github.com/newyngwieslash-ops/MovieDesign/releases/latest`

---

## 2. 签名密钥管理与安全铁律

1. **私钥绝对隔离**：
   - `TAURI_SIGNING_PRIVATE_KEY`（及可选密码 `TAURI_SIGNING_PRIVATE_KEY_PASSWORD`）仅保存在发布者本机环境变量或 GitHub Actions Repository Secrets 中。
   - **严禁**将任何签名私钥写入 Git 仓库、日志文件、SQLite 快照或测试 fixture。
2. **公钥随应用发布**：
   - 验证公钥配置在 `desktop/src-tauri/tauri.conf.json` 的 `plugins.updater.pubkey` 字段中，由 `tauri-plugin-updater` 在安装前执行 minisign 签名与受信任注释中的 `version:<semver>` 绑定校验。
3. **无私钥即报错阻断**：
   - 运行 `./scripts/release.ps1`（非 `-DryRun`）时，若未配置 `TAURI_SIGNING_PRIVATE_KEY` 或 `TAURI_SIGNING_PRIVATE_KEY_PATH`，脚本立即报错退出，**绝不生成假签名或未签名的自动更新包**。

---

## 3. 更新策略 (`updates/stable/policy.json`) 与 24 小时缓存契约

`gateway/internal/updates/policy.go` 按恢复源码 `parseCdnPolicy`（`update-policy-fetcher-CYQPhXS_.js:79`）与 `manualOnlyRuleMatches` / `resolveUpdatePolicy`（`index-WiZKxTAT.js:2158`）实现：

1. **必填与可选字段（保留原字段名）**：
   - 必填：`schema_version: 1`、`enabled: boolean`、`min_supported_version: "<semver>"`。
   - 可选：`force_reason: {zh, en}`、`manual_download_url`（仅接受合法 HTTP/HTTPS URL；非法 URL 记录告警并忽略该字段，不导致整份策略解析失败）、`updated_at`、`manual_only: {platforms, min_version_inclusive, max_version_exclusive, reason: {zh, en}}`。
2. **`manual_only` 匹配规则**：
   - 仅当 `enabled == true` 且 `current_version < min_supported_version` 时评估强制更新与 `manual_only`。
   - 需要非空合法 `manual_download_url`、`platforms` 非空且全部属于 `{"win32", "darwin", "linux"}`（Rust `windows` 显式映射为 `win32`）、合法 `max_version_exclusive <= min_supported_version`；若存在 `min_version_inclusive`，必须满足 `min_version_inclusive < max_version_exclusive` 且 `min_version_inclusive < min_supported_version`。
   - 匹配半开区间 `[min_version_inclusive, max_version_exclusive)`。
   - `min_supported_version` 是最低支持版本阈值，不冒充实际待安装版本；实际待安装版本取自签名 `latest.json` manifest。
   - 命中 `manual_only` 时阻止自动下载与自动安装，UI 与原生门禁仅保留前往本仓库 GitHub Releases 的手动下载入口；手动下载的安装包同样经过签名校验。
3. **HTTPS 拉取、重试与 24 小时原子缓存 (`update-policy-cache.json`)**：
   - 请求超时 `3s`，最多尝试 `3` 次；仅对 HTTP `5xx` 重试，退避延迟为 `250ms * 2^(n-1)`，上限 `1000ms`。网络/超时、响应体读取失败、`4xx`、超大响应与 `invalid_schema` 均不重试，仍可使用匹配的 LKG 缓存。这是 MovieDesign 已确认计划对原产品网络重试范围的收窄，H05 于 2026-10-22 修复代码与文档漂移。
   - 缓存结构为 `{policy, cachedAt, metadata: {region: "global", channel: "stable", platform: "win32"}}`，使用临时文件 + `Sync` + `Rename` 原子写入。
   - `CacheFresh(now, fetched)`：`age := now.Sub(fetched); return age >= 0 && age <= 24*time.Hour`。
   - `age <= 24h` 时可直接用于启动并在后台刷新；过期或时钟回拨（`age < 0` 未来时间戳按过期处理并标记 `clockSkew: true`）时先刷新远端，刷新失败则保留并应用元数据匹配的 LKG（Last Known Good）缓存策略，同时向 UI 暴露 `stale: true` 与 `policyFallbackReason`。
   - 元数据不匹配的缓存直接弃用；若远端拉取失败且无可用 LKG 缓存，返回 `status: "failed", source: "none"`，桌面禁用自动安装、仅保留手动 Releases 入口，绝不伪造服务端 `manual_only` 规则。
4. **活动任务保护**：
   - 当 Gateway 存在活动生成任务、聊天轮次或未完成的文件/投递/写操作（`!idle || !safe_to_suspend`）时，`busy: true`，拒绝自动安装更新或重启应用。

---

## 4. 本地构建、Dry-Run 与正式发布步骤

### 4.1 离线 Dry-Run 验证（无需签名私钥）

```powershell
./scripts/release.ps1 -DryRun
```

该命令会：
1. 编译 `ffprobe`/`ffmpeg`、`open-design-gateway`、`moviedesign-opencode` 及前端产物；
2. 校验 `tauri.conf.json`、`updates/stable/policy.json` 与 `docs/third-party.md`；
3. 在 `.runtime/release-dry-run/` 输出 `release-dry-run.json`、`RELEASE-NOTES.md`、`policy.json`、`third-party.md` 与 `SHA256SUMS.txt`。

### 4.2 正式签名发布（需配置签名私钥）

```powershell
$env:TAURI_SIGNING_PRIVATE_KEY = "<your-minisign-private-key>"
$env:TAURI_SIGNING_PRIVATE_KEY_PASSWORD = "<optional-password>"
./scripts/release.ps1 -Version 0.1.0
```

产出位于 `.runtime/release-v0.1.0/`：
- `MovieDesign_0.1.0_x64-setup.exe`
- `MovieDesign_0.1.0_x64-setup.exe.sig`
- `latest.json`
- `policy.json`
- `third-party.md`
- `RELEASE-NOTES.md`
- `SHA256SUMS.txt`

---

## 5. 供应商配置、取消费用语义、数据备份恢复与第三方许可证

1. **自定义供应商配置（BYOK）**：
   - 在应用左侧导航进入「供应商设置」，可配置图像（`openai-images`）、视频（`ark-video`）与 LLM（OpenCode provider）连接的名称、Base URL、Model ID、API Key（Windows DPAPI 加密存储于 `<dataDir>/credentials`）、能力参数与单价表。
2. **取消费用语义**：
   - 发送前取消（`not_submitted`）：本地取消并作废计费确认卡，实际费用为 `0`。
   - `ark-video` 排队中取消（`queued`）：调用上游 `DELETE` 成功后标记 `remote_cancelled_queued`，实际费用为 `0`。
   - `ark-video` 运行中取消（`running`）：上游不支持 `DELETE` 终止，本地标记 `local_cancelled_remote_may_charge`（本地已取消，上游仍可能计费），后台继续只读对账，若晚到成功则弹出人工确认卡决定是否采用。
3. **本地数据备份与升级恢复**：
   - 所有项目、SQLite 数据库（`<dataDir>/moviedesign.db`）、受管工作区文件（`<dataDir>/workspace/projects/...`）、DPAPI 凭据（`<dataDir>/credentials`）与更新策略缓存（`<dataDir>/update-policy-cache.json`）均保存在应用数据目录（默认 `%APPDATA%\io.github.newyngwieslash-ops.moviedesign` 或 `MOVIEDESIGN_DATA_DIR`）。
   - 备份时只需在应用空闲（`/api/health` 返回 `safe_to_suspend: true`）或关闭后完整复制 `<dataDir>` 目录；版本升级不会覆盖或清空既有数据库、版本血缘、人类批准记录与幂等回执。
4. **第三方许可证**：
   - 详见随安装包分发的 [`docs/third-party.md`](third-party.md)（打包路径 `resources/licenses/third-party.md`）。
5. **端到端验收与 SQLite 血缘导出校验**：
   - 运行 `./scripts/verify-lineage.ps1` 可执行端到端 15 秒口播视频流水线、生成单项目脱敏 SQLite 导出库并调用 `sqlite3.exe .dump` 验证从 `storyboard_revisions` 到 `assets` 的完整多表 SQL 血缘与跨项目/凭据零泄漏。
   - 完整规格覆盖表与四阶交付门槛状态（`功能已编码: PASS`、`离线测试通过: PASS`、`真实生成通过: UNEXECUTED`、`签名发布完成: UNEXECUTED`）见 [`docs/acceptance.md`](acceptance.md)。
