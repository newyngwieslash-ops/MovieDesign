# M7 视频、更新与最终交付 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 接入可配置本地/云端视频任务API，完成真实15秒口播配乐视频、打断恢复、完整血缘和签名更新交付。

**Architecture:** 视频使用M4任务/报价/确认和M6合同，adapter只负责供应商wire。更新由Rust插件验签，gateway提供策略与活动状态；GitHub Releases托管本应用产物。

**Tech Stack:** 现有Go/React/Tauri、已知视频任务协议、ffprobe、tauri-plugin-updater、GitHub Releases、签名静态manifest。

## Global Constraints

继承总计划。只接一个视频协议模板并允许多个配置，不建设插件框架。生成失败不得自动重试；查询上游进度不是重新生成。实际远端取消能力与费用必须如实显示。签名私钥不得进入Git、日志、测试fixture。

### Task M7.1：自定义视频连接、能力与真实任务协议

**Files:**
- Create: `gateway/internal/providers/video.go`, `gateway/internal/providers/video_test.go`, `gateway/internal/providers/video_capability.go`。
- Modify: `gateway/internal/providers/config.go`, `gateway/internal/providers/capabilities.go`, `gateway/internal/tasks/worker.go`, `gateway/internal/server/generate.go`, `desktop/src/features/providers/ProviderSettings.tsx`, `docs/contracts.md`。

**Interfaces:** 协议模板`ark-video`实现Submit/Get/Cancel或Delete；BaseURL/model/key由配置提供。内部`VideoJob{UpstreamID,Status,Progress,OutputURL,Problem}`，任务表保留供应商原始状态与已脱敏错误。映射到MovieDesign状态时不能把排队当已完成，不能把查询超时当生成失败。

- [x] 根据官方API文档保存脱敏请求/响应fixture，用httptest验证文本与参考图有序映射、model/duration/aspect/audio、taskID、queued/running/succeeded/failed、鉴权错误、限流、未知字段、取消竞态及过期下载URL。
- [x] `go -C gateway test ./internal/providers -run TestVideo -v`；尚未支持协议/字段的情况明确失败，不以通用http透传绕过能力白名单。
- [x] 模型能力与价格必须由所配连接声明并核实，adapter不能给所有模型统一写死15秒或audio=true。设置页展示参考图、最大时长、画幅、音频、可取消阶段、报价来源；不支持的选项在Preflight被拒绝。
- [x] 参考资产先从ID解析受管文件，再按该官方协议允许的上传/dataURL/受控URL形式发送；不能发送本机路径供云端读取。若供应商要求上传服务，上传状态与ID持久化、只发送用户明确引用的资产，并把该能力写入协议契约测试。
- [x] 创建请求的已受理taskID立即持久化；GET轮询可使用有上限退避和Retry-After，POST创建绝不自动重发。任务无ID且受理未知进入unknown；有ID按状态查询恢复。完成后立即下载到受管文件，不依赖供应商短期URL作为永久资产。
- [x] `Cancel`只在官方支持状态调用。远端无法停止时本地cancelled仍保留upstreamID和对账，UI明确可能继续计费；晚到结果不能自动交付。提交 `feat(M7.1): adapt configurable video task providers`。

M7.1 在 `9d03cd7` 通过独立规格/质量审查（见 `.superpowers/sdd/m7.1-review.md`）。

### Task M7.2：15秒口播配乐与生成产物闭环

**Files:**
- Create: `gateway/internal/pipeline/video_test.go`, `scripts/smoke-video.mjs`。
- Modify: `gateway/internal/pipeline/preflight.go`, `gateway/internal/providers/video_capability.go`, `gateway/internal/tasks/worker.go`, `desktop/src/features/canvas/CanvasNode.tsx`, `agent/skills/口播视频/references/preflight.md`。

**Interfaces:** 口播视频合同明确目标15秒、参考图、说话内容与音乐意图。模型提供单次整条视频及音频；不隐式接第三家TTS/音乐模型，不做片段拼接。模型不具备所需能力时在付费前返回可操作缺项。

- [x] 测试不支持audio、最大时长不足15秒、参考图限制、空音轨、文件格式错误、超大小、下载断流、重复投递。技术检查只核实文件和客观参数，不做LLM自评分或自动重生成。
- [x] `go -C gateway test ./internal/pipeline ./internal/providers ./internal/tasks -run 'TestVideo|TestPreflight|TestDownload' -v`。
- [ ] 通过真实provider完成一条获用户批准的15秒合同；记录实际容器时长、视频帧率/时间基、音轨存在。时长容差以编码时间基和一帧为准记录，不把显著短片标成15秒。是否口播正确、音乐合意由用户反馈与验收，不由agent自评后反复收费。
- [x] 用现有delivery将产物落指定Composition/role，生成Resource新Version、task_outputs→version映射及源分镜/Prompt依赖。画布video使用受保护gateway内容读取，重启后仍可播放；引用资产可继续做新版本编辑，历史不覆盖。
- [x] 运行真实WebView smoke-video并保存脱敏任务ID、版本ID、截图/本地结果位置；凭据或账号能力缺失时保持“真实验收未执行”，独立离线任务继续推进。提交 `feat(M7.2): deliver approved narrated video with lineage`。

M7.2 在 `0d4b166` 通过独立规格/质量审查（见 `.superpowers/sdd/m7.2-review.md`）；生产 WebView2 三阶段 `smoke-video` 通过（`.runtime/desktop-release-9460b02d1bb04cd88cb85b41a1e9254a`）；真实付费视频供应商调用因未配置凭据保持未执行。

### Task M7.3：任务中心、打断恢复和健康生命周期

**Files:**
- Create: `gateway/internal/tasks/recovery_test.go`, `scripts/smoke-interruptions.mjs`。
- Modify: `gateway/internal/tasks/service.go`, `gateway/internal/tasks/worker.go`, `gateway/internal/server/server.go`, `gateway/internal/control/stdio.go`, `desktop/src/features/tasks/TaskCenter.tsx`, `desktop/src-tauri/src/gateway.rs`, `desktop/src-tauri/src/opencode.rs`。

**Interfaces:** 任务中心读取同一TaskView，展示phase/percent/problem.upstream/actions、provider真实取消能力、估算/实际可得费用和版本链接。`idle/safe_to_suspend`来自同一活动状态快照，覆盖提交、归档、投递、聊天轮次和待恢复状态。

- [x] 演练accepted/计费等待/queued/active/下载/投递阶段取消；生成失败后手动重试；关窗口/杀gateway后重启。provider收到提交次数必须和已批准attempt数量一致，不因重启增加。
- [x] 测试retry的revision CAS竞态，旧attempt晚到成功不得覆盖新attempt；unknown仅查询/对账；重复cancel返回原结果；被取消的计费卡不得后来启动任务。
- [x] 不让长轮询持有SQLite事务；关闭流程先停止新请求/新任务领取，持久化活动阶段，再关闭HTTP与进程。受管子进程强制超时只针对自身句柄，不按进程名全局kill。远端正在运行的任务记录保留到下次恢复。
- [x] 更新/退出前展示活动任务及影响；需要用户决策仍使用确认卡。安全挂起定义是本地副作用已落盘且可恢复，不宣称远端任务不计费。生成期间不自动安装更新或重启。
- [x] `go -C gateway test ./internal/tasks ./internal/server ./internal/control -v`与smoke-interruptions、Rust生命周期测试通过，提交 `feat(M7.3): recover interrupted jobs without duplicate submission`。

M7.3 在 `6837dac` 通过独立规格/质量审查（见 `.superpowers/sdd/m7.3-review.md`）；生产 WebView2 三阶段 `smoke-interruptions` 通过（`.runtime/desktop-release-1c51369d935b4a2798e32bb3c892622f`）。

### Task M7.4：签名更新、manual_only 策略与24小时缓存

**Files:**
- Create: `gateway/internal/updates/policy.go`, `gateway/internal/updates/policy_test.go`, `gateway/internal/server/updates.go`, `desktop/src-tauri/src/updates.rs`, `desktop/src/features/settings/UpdateSettings.tsx`, `scripts/release.ps1`, `docs/releasing.md`, `.github/workflows/release.yml`。
- Create: `updates/stable/policy.json`（仅发布策略，不含密钥）。
- Modify: `desktop/src-tauri/Cargo.toml`, `desktop/src-tauri/Cargo.lock`, `desktop/src-tauri/src/lib.rs`, `desktop/src-tauri/tauri.conf.json`, `desktop/src-tauri/capabilities/default.json`, `gateway/internal/server/server.go`, `desktop/src/App.tsx`, `docs/contracts.md`。

**Interfaces:** `GET /api/updates/policy`返回计算后的manual_only/reason/cache状态；写操作check记录requestId与app_update_check事件。Rust updater保持插件原始签名协议，策略是其前置门禁，不改写manifest签名校验。GitHub仓库限定为newyngwieslash-ops/MovieDesign。

托管落点固定：策略为`https://raw.githubusercontent.com/newyngwieslash-ops/MovieDesign/main/updates/stable/policy.json`；更新manifest为`https://github.com/newyngwieslash-ops/MovieDesign/releases/latest/download/latest.json`；manifest内安装包指向具体版本Release资产。策略可以独立于新安装包提交调整；这两个地址在M7创建对应产物后才成为可用服务。

- [x] 按恢复源码parseCdnPolicy保留必填`schema_version:1/enabled/min_supported_version`，可选`force_reason:{zh,en}`、`manual_download_url`、`updated_at`及`manual_only:{platforms,min_version_inclusive,max_version_exclusive,reason:{zh,en}}`。依据`extracted/out/main/chunks/update-policy-fetcher-CYQPhXS_.js:79`保存wire fixture，不重新命名。测试命中平台与版本、未命中、空规则、损坏JSON、24小时边界、网络断开、签名错误、忙时拒绝安装。
- [x] 策略HTTPS请求3秒超时，总尝试次数最多3次，仅5xx可重试，延迟为250ms×2^n、上限1000ms；不把生成禁止自动重试规则扩展到只读更新查询。缓存`{policy,cachedAt,metadata:{region,channel,platform}}`，原子写入；age≤24小时可用于启动，后台刷新；过期先刷新，失败保留并应用元数据匹配的LKG策略，同时展示已过期事实。元数据不匹配弃用，未来时间戳/时钟回拨按过期处理并记录这项适配。无可用LKG按原语义返回status=failed/source=none；桌面自动安装要求策略ready，失败仅保留手动入口，不伪造服务端manual_only规则。region在MVP固定global、channel固定stable，保留两字段避免未来串缓存。

```go
func CacheFresh(now, fetched time.Time) bool {
    age := now.Sub(fetched)
    return age >= 0 && age <= 24*time.Hour
}
```

- [x] `manualOnlyRuleMatches`按`index-WiZKxTAT.js:2158`迁移：需要合法manual_download_url、非空受支持platforms、合法max_version_exclusive且≤min_supported_version，min若存在必须<max及min_supported_version；匹配当前平台与半开版本区间；enabled=false无强制规则。min_supported_version是阈值，不冒充实际待安装版本，实际版本取签名manifest。非法manual规则不命中，非法URL告警丢弃；完整policy解析失败才使用LKG。manual_only命中阻止自动下载/安装，手动入口到本仓库Releases；安装仍校验签名。Rust平台名显式映射到原协议win32，不拿windows字符串误判。
- [x] Release脚本构建gateway/opencode/media工具及Tauri安装包，生成插件要求的签名和静态manifest、SHA256清单、版本/许可证说明。签名私钥从本机环境或GitHub Secrets读，公钥随应用；无签名配置明确失败，禁止发布假签名或未签名自动更新。
- [x] 历史离线模拟迁移验证数据/版本/批准/幂等回执保留；策略与验签辅助函数测试、`go -C gateway test ./internal/updates -v`、Rust测试及 Release dry-run 通过，提交 `feat(M7.4): gate signed updates with cached release policy`。这些检查不等于真实安装升级。
- [ ] 真实原生检查→下载→验签→安装调用链及忙碌排他门禁（2026-10-22 交接 H01 接续中）；生产构建、实际 WebView 与独立审查通过后再勾选。
- [ ] 在干净 Windows 账户从旧签名版本实际安装升级至新签名版本，验证数据/版本/批准/幂等回执保留，坏签名拒绝、manual_only 零下载、任务期间零安装。正式材料与环境未验证，**UNEXECUTED**。

M7.4 在 `d1aa130` 通过独立规格/质量审查（见 `.superpowers/sdd/m7.4-review.md`）；`scripts/release.ps1 -DryRun` 通过（`.runtime/release-dry-run/`）；正式私钥签名构建因未注入 `TAURI_SIGNING_PRIVATE_KEY` 保持未执行。

### Task M7.5：全规格审计、端到端验收和发布

**Files:**
- Create: `docs/acceptance.md`, `scripts/verify-lineage.ps1`, `gateway/internal/pipeline/acceptance_test.go`。
- Modify: `scripts/check.ps1`, `README.md`, `docs/contracts.md`, `docs/milestones.md`, `docs/releasing.md`。

**Interfaces:** acceptance记录每项规格→测试命令→实际结果→证据路径→偏差；不以测试数量代替覆盖。导出工具只导出选定测试项目，不输出凭据、其他项目私有素材或真实上游认证header。

- [x] 全量运行 `./scripts/check.ps1`（Go测试/vet、编辑器/前端纯逻辑测试、frontend build、Rust tests、gateway smoke）及desktop/editor/chat/generation/pipeline/video/interruptions对应真实WebView脚本；所有新增检查纳入脚本，不仅写在文档。
- [ ] 本地和云端配置分别保存/切换/重启恢复；用本地协议fixture确认HTTP路径，不据此声称真实本地模型兼容。至少一个真实图像服务、一个真实视频服务及真实LLM通过完整链，记录protocol/model/能力；不得把真实调用失败的配置静默切换成mock。
- [ ] 新项目上传允许使用的参考图，输入“用这张图做个15秒口播视频配轻快音乐”，经真实agent、技能、所有结构化卡、精确Prompt和费用批准后成片；产物画布可播放，编辑修订产生新Version并保留血缘。
- [x] 三种打断演练全过：生成中取消、失败后用户重试、分镜批准后改分镜重新门禁。并发重复deliver相同requestId只有一组Version；同键异输入409；unknown崩溃后同键重放恢复。
- [x] SQL证明源分镜Version、Prompt Version、prompt_contract、generation_inputs/task、task_output、结果Version、Asset均存在且关联正确；多参考图顺序可还原。执行`sqlite3`对验收数据库`.dump`并核对，不把仅有source_version_id的一条链当完整证据。未安装sqlite3时在发布验收环境准备官方CLI，不能修改应用数据库驱动。
- [x] 安全验证：无Origin/null/外站Origin拒绝包括OPTIONS和MCP；合法Origin但无MCP令牌拒绝；路径穿越/junction/跨项目引用/未声明参数拒绝；opencode只能调用允许工具。注明curl能伪造Origin，因此此项证明浏览器CSRF防护，不证明本机恶意进程隔离。
- [ ] 检查签名安装包在干净Windows账户安装、首次启动、创建项目、关闭、升级；更新策略缓存/停更/坏签名测试均有结果。发布文档明确首发平台、配置方式、取消费用语义、备份恢复和第三方许可证。
- [x] 完成总计划§0–15覆盖表，每项标通过/未通过/未执行并附证据。没有真实凭据、签名密钥或模型能力的项目必须保持未执行，不标M7完成。全部验收通过后提交 `feat(M7.5): complete MovieDesign acceptance and release`，推送并发布本仓库签名Release。

M7.5 在 `26a30ac` 通过独立规格/质量审查（见 `.superpowers/sdd/m7.5-review.md`），整 M7（`1f13834..26a30ac`）通过独立整阶段审查（结论 `Ready to merge: Yes`，见 `.superpowers/sdd/m7-whole-review.md`）。四阶交付门槛核算：**功能已编码（PASS）**、**离线测试通过（PASS）**、**真实生成通过（UNEXECUTED — 本机未配置真实付费/可用模型凭据）**、**签名发布完成（UNEXECUTED — 未注入正式 `TAURI_SIGNING_PRIVATE_KEY`）**。

## 交付门槛

“功能已编码”“离线测试通过”“真实生成通过”“签名发布完成”分别记录。最终完整实现要求四者成立；某外部凭据缺失只阻塞对应真实调用或发布，不阻塞其余已授权工程工作。
