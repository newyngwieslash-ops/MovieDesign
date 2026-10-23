# MovieDesign 全规格审计与端到端验收报告（M7.5）

本文档记录 `MovieDesign`（M1–M7）对照 [原始规格](spec.md)、[已确认契约](contracts.md)、[总实施计划](superpowers/plans/2026-10-15-moviedesign-complete.md) 与 [M7 分计划](superpowers/plans/2026-10-15-m7-video-release.md) 的逐项规格映射、验证命令、实际结果、证据路径与确认偏差。

> **2026-10-23 H19/H20接续：** `80b3a23` 补齐关闭取证，首审两项Important经fix1限定复审通过；最终全量检查和生产updates initial/recovery退出0。原应用关闭超时未复现、根因仍未知。默认用户应用已准备真实图像任务，10:53核对仍待人工费用批准，旧报价已过期，真实生成仍 **UNEXECUTED**。用户已批准新增聊天billing目标绑定，H20正在实现，尚不属于已交付/已回归范围。详见[真实验收接续](handoffs/2026-10-23-acceptance-followups.md)。

> **2026-10-23 08:23 接续审计校正：** 下方M7.5的PASS为历史结果。H17诊断/启动隔离与H18测试等待条件修复通过独立审查。4096当前实际可绑定，未停止WinNAT或更改网络配置；完整标准生产更新initial/recovery、聊天initial/recovery/restart及当前源码最终全量检查均通过。一次较早的10秒正常退出超时仍未解释，不声称已修复。真实模型调用、正式签名与干净账户安装升级均为 **UNEXECUTED**。当前证据见 [恢复验收](handoffs/2026-10-23-recovery.md)。

---

## 1. 四阶交付门槛状态总览

按 M7.5 交付门槛纪律，“功能已编码”“离线测试通过”“真实生成通过”“签名发布完成”四项独立核算，绝不用本地协议 fixture 或 dry-run 冒充真实付费模型调用或真实私钥签名发布：

| 交付门槛维度 | 当前状态 | 核验结论与阻塞边界说明 |
| --- | --- | --- |
| **1. 功能已编码** | **工程交付至H19；H20实现中** | H01–H18已审查范围保留。`80b3a23`仅补关闭取证与其回归，首审CLIXML fixture及释放异常覆盖原错误两项Important均经限定复审关闭，Spec PASS / Quality PASS。用户新增要求的首轮聊天计费门禁H20尚未交付，不能以H19结果替代其验收。 |
| **2. 离线测试通过** | **H19最终源码全量/桌面PASS；H20待回归** | `80b3a23`最终三脚本对应`.runtime/h19-fix1-full-check.log`退出0；生产updates initial/recovery退出0，关闭1293/1347ms，原10秒和自有进程/端口断言保留。应用制品与H17哈希相同，历史H18 chat三阶段结果保留。10:36隔离收尾进程/监听为空、WinNAT未改；之后真实用户应用有意保留打开等费用决定。原关闭超时根因未知，不称根治；运行中的H20代码尚未使用上述结果冒充通过。 |
| **3. 真实生成通过** | **未执行（UNEXECUTED）** | 尚无经应用人类确认卡批准的真实 LLM、图像编辑或 15 秒口播配乐生成验收记录；本地 HTTP fixture、实际 opencode 进程和合成 MP4 不替代真实模型、账单或主观质量验收。 |
| **4. 签名发布完成** | **未执行（UNEXECUTED）** | 正式密钥配对、签名 NSIS/GitHub Release 和干净 Windows 账户安装升级均未执行。当前公开测试公钥会被生产发布保护拒绝；公开向量验签、受控安装 sink、升级 fixture 与 dry-run 仅证明各自工程边界。 |

---

## 2. M7.5 历史规格覆盖表（非当前接续验收结论）

| 规格章节 | 规格要求摘要 | 状态 | 验证命令 | 实际结果 | 证据路径 | 适配与偏差说明 |
| --- | --- | --- | --- | --- | --- | --- |
| **§0 参考资料与契约** | 遵循原产品对象语义、投递状态机、分镜门禁与更新策略协议；所有适配记录在 `docs/contracts.md` | **通过** | `powershell -NoProfile -File scripts/check.ps1` | 契约文档完整覆盖 M1–M7 全部接口、表结构、错误码与恢复语义 | `docs/contracts.md`、`docs/spec.md`、`analysis-docs/` | 经用户确认：采用 Go 1.25.8 + 官方修复版 MCP SDK v1.4.1；新增 `/api/projects`、`/api/assets`、`/api/providers`、`/api/pipeline/*`、`/api/updates/*` 补充面并显式标注 |
| **§1 产品定义与非目标** | 本地优先 AI 设计工作台（画布、AI 对话、生成流水线、技能系统、SQLite 版本血缘）；砍掉内嵌浏览器、插件系统、技能市场、团队协作、热更新 | **通过** | `go -C gateway test ./...`、`node --test scripts/check-*.mjs` | 五大核心能力落地；零插件脚本执行面、零内嵌浏览器、零技能市场 | `gateway/internal/`、`desktop/src/`、`agent/` | BYOK 模式：不建平台充值钱包，仅提供整数最小单位估算报价与人类确认卡 |
| **§2 技术栈与进程拓扑** | Tauri 2 + React 19/TS/Vite + Go 1.25 单 binary + 受管 `opencode` 1.19.0 + `modernc.org/sqlite` + `modelcontextprotocol/go-sdk` + `tauri-plugin-updater`；前端仅访问 gateway | **通过** | `cargo test --manifest-path desktop/src-tauri/Cargo.toml`、`powershell -NoProfile -File scripts/smoke-m1.ps1` | Rust 8/8 测试通过；Tauri 仅管理自有句柄，前端 CSP 与请求全部收敛至 `127.0.0.1:8001` | `.runtime/smoke-8dda18f538234a01a7a334dcf39a9136`、`desktop/src-tauri/src/` | 原规格 Go 1.24 经用户确认升级至 Go 1.25.8 以接入修复 GHSA-89xv-2j6f-qhc8 的官方 MCP SDK v1.4.1 |
| **§3 核心数据模型与血缘** | `assets`、`compositions`、`resources`、`versions`、`composition_members` 五张核心表结构不变；编号支撑表迁移 `000001`–`000014` | **通过** | `go -C gateway test ./internal/store ./internal/pipeline -v`、`powershell -NoProfile -File scripts/verify-lineage.ps1` | `000001_assets` 与 `000002_projects` 零改动；支撑表触发器强制分镜/Prompt/合同不可变及删除保护 | `gateway/migrations/`、`.runtime/acceptance-lineage/verify-lineage-report.json` | 支撑表按功能演进拆分为 `000003`–`000014`（共 14 个只增迁移），五核心表完全冻结 |
| **§4 Gateway HTTP 路径** | `/api/health`、`/api/upload`、`/api/upload/commit`、`/api/files/*`、`/api/generate/*`、`/api/tasks/*`、`/api/compositions`、`/api/resources`、`/api/versions`、`/api/deliver`、`/api/chat/*`、`/api/canvas/*`、`/api/events`、`/api/question*`、`/api/pipeline/*`、`/api/updates/*` | **通过** | `go -C gateway test ./internal/server -v` | 全部路由严格校验 JSON（拒绝未知字段与重复 key）、幂等 `requestId`、分页 `coverage` 与统一错误枚举 | `gateway/internal/server/` | 顶层错误码固定为 `invalid_input / not_found / waiting_user / capacity / upstream`，细分原因置于 `details.kind` |
| **§5 全部 18 项 `design_*` MCP 工具** | `design_capability_{list,get}`、`design_execute`、`design_deliver`、`design_task_{query,wait,resolve}`、`design_read`、`design_analyse_media`、`design_composition_{search,get,edit}`、`design_resource_{get,edit}`、`design_document_write`、`design_project_rules`、`design_voice_search`、`design_image_search` | **通过** | `go -C gateway test ./internal/mcp ./internal/agent -v`、`powershell -NoProfile -File scripts/smoke-desktop.ps1 -Mode release -Smoke tools` | 18 项工具与 HTTP 共享领域服务；`params` 白名单、`references` 仅传 asset ID、写工具强制 `requestId` 与 callID 重放绑定 | `gateway/internal/mcp/`、`.runtime/desktop-release-e03e70e5e4ee4cf8b8a4279a61a991c6` | 为满足全写操作幂等，MCP 写工具显式增加 `requestId` 参数并由 gateway 绑定 callID 重放 |
| **§6 任务状态机与计费门禁** | `accepted -> running -> waiting_user | completed | failed | cancelled | unknown`；区分 `billing` 与非 `billing` 等待；禁止自动重试；阶段感知取消与真实计费对账 | **通过（离线/协议） / 未执行（真实扣费）** | `go -C gateway test ./internal/tasks ./internal/providers -v`、`powershell -NoProfile -File scripts/smoke-desktop.ps1 -Mode release -Smoke interruptions` | 状态机、CAS 乐观锁、零自动重试、排队远端 `DELETE` 取消与运行中不可撤回对账、晚到结果采纳卡全部通过 | `gateway/internal/tasks/recovery_test.go`、`.runtime/desktop-release-1c51369d935b4a2798e32bb3c892622f` | 本地协议 fixture 完全验证；真实供应商账单扣费因未配置凭据保持 **UNEXECUTED** |
| **§7 三态幂等投递** | `POST /api/deliver` 返回 `completed | unknown | blocked`；同 `requestId` 并发/重放只产生一组 `Version`；同键异输入返回 `409 invalid_input` | **通过** | `go -C gateway test ./internal/delivery ./internal/pipeline -run "TestReplay|TestConcurrent|TestAcceptanceThreeInterruptionDrillsAndIdempotentDelivery" -v` | `prepare/archive/before_commit/after_commit` 四故障点 `unknown` 恢复、8 并发同键唯一 `Version`、同键异输入 `409` 全过 | `gateway/internal/delivery/service_test.go`、`gateway/internal/pipeline/acceptance_test.go` | 源支持 `path` 与持久唯一 `taskOutputKey` |
| **§8 聊天桥与 40 次熔断** | 10 秒受理宽限窗、SSE 帧回流、持久队列与撤回、原生 `question` 人类回执桥、单轮唯一 tool call ID 40 次上限熔断（拦截第 41 次） | **通过（受管 opencode + 本地模型 fixture） / 未执行（真实 LLM）** | `go -C gateway test ./internal/agent -v`、`node --test scripts/check-chat-ui.mjs` | 10 秒宽限窗、5xx/重定向/断流零重发、第 41 次工具调用在副作用前拦截并 abort、队列撤回 409 竞态恢复全过 | `gateway/internal/agent/budget_test.go`、`.runtime/desktop-release-7803cdcb21104c48b66c25435189c1a0` | 固定 `opencode` 1.19.0 真实子进程 + 合成模型流验证；真实云端 LLM 推理保持 **UNEXECUTED** |
| **§9 八图形编辑器、标注、TipTap、确认卡、任务中心** | 8 图形（矩形/椭圆/线/箭头/笔刷/文字/标注气泡/马赛克）、四层渲染、50 步双栈历史、透明 textarea、成对标注、TipTap mention、全局唯一确认卡、任务中心 | **通过** | `node --experimental-strip-types --test desktop/packages/editor/test/*.test.ts desktop/src/features/editor/annotations.test.ts desktop/src/features/canvas/canvas.test.ts`、`node --test scripts/check-task-ui.mjs` | 69 项前端/编辑器测试 + 7 项组件恢复测试 + 生产 WebView2 画布与任务中心冒烟全部通过 | `desktop/packages/editor/`、`.runtime/desktop-release-d6f0d74382664da0a1ec38cfb85980c9` | 物理跨屏自动 DPI 与物理硬件输入法候选框保持 M3 注明边界 |
| **§10 中文系统提示词与权限隔离** | 注入中文 `media-agent.md`（身份、Composition/Resource/Version 判例、协作节奏、纪律、技能消费）；禁用 `bash/read/write/edit/todowrite/webfetch/skill/external_directory` | **通过** | `go -C gateway test ./internal/agent ./internal/pipeline -run "TestBuildConfig|TestAcceptanceSecurityBoundaries" -v` | `agent/media-agent.md` 完整包含五节判例；`BuildConfig` 强制 `*: deny` 且仅放行 `design_design_*` 与 `question` | `agent/media-agent.md`、`gateway/internal/agent/config.go` | 独立无工具文本任务使用单独的 `text` agent（全工具 `deny`） |
| **§11 两个方法论技能** | `口播视频` 与 `图片标注修复` 存 `<dataDir>/skills/<name>/`，含 `SKILL.md`、`meta.yaml`、`references/*.md`，含适用范围、负向边界、门禁序列、失败回流 | **通过** | `go -C gateway test ./internal/agent ./internal/pipeline -run "TestSkills|TestAcceptanceEndToEndNarratedVideoAndLineageExport" -v` | 两个技能完整安装并通过结构校验、触发词匹配、会话采纳快照冻结与越界链接拦截 | `agent/skills/口播视频/`、`agent/skills/图片标注修复/` | 技能仅注入创作方法论，不授予工具权限或自动跳过人类门禁 |
| **§12 分镜流水线、Prompt 锁定、7 门禁 Preflight 与四路失败回流** | `Panel -> LogicalShot -> MasterTimeline`、人类回执绑定 revision、方向性失效、字节级一致 `final_video_prompt.md`、7 门禁 Preflight、四类失败回流（参数/语义/上游临时/未知） | **通过** | `go -C gateway test ./internal/pipeline -v`、`node --test scripts/check-storyboard-ui.mjs` | 方向性失效、字节级 SHA-256 锁定、7 门禁 Preflight、TOCTOU 工人复核、四类失败回流及 M3 图像编辑血缘全部通过 | `gateway/internal/pipeline/`、`.runtime/desktop-release-4fd2fc18483f4026b7dfe9c4fb4ab4ef` | 默认整条重生成，不做片段拼接或 LLM 自评自动重试 |
| **§13 校验、Origin、结构化日志与 `events` 表** | 绑定 `127.0.0.1`、全请求精确校验 `Origin`（含 `OPTIONS` 与 `/mcp`）、结构化日志落 `logs/gateway.log`、`events` 表持久化 `chat_message_send / generate_task_submit / generate_task_failed / app_update_check` | **通过** | `go -C gateway test ./internal/server ./internal/events ./internal/pipeline -run TestAcceptanceSecurityBoundaries -v` | 无 Origin、`Origin: null`、外站 Origin 在普通路由、`OPTIONS` 和 `/mcp` 均返回 `403`；无 MCP Token 返回 `401` | `gateway/internal/server/`、`gateway/internal/pipeline/acceptance_test.go` | 本机 `curl` 可伪造 `Origin` 头，故 `Origin` 校验提供浏览器 CSRF 防护，不声称提供本机恶意进程隔离 |
| **§14 里程碑提交与独立审查** | M1–M7 按序提交、每任务独立审查、每里程碑整阶段审查后合并推送 | **通过** | `git log --oneline main..codex/m7-video-release` | M1–M6 已合入 `main` 并推送；M7.1–M7.5 逐任务提交并附独立审查报告与 SHA-256 清单 | `.superpowers/sdd/m7.1-review.md`–`m7.4-review.md`、`docs/milestones.md` | 遵循严格 TDD 与读写分离审查 |
| **§15 端到端口播视频、三种打断演练、并发幂等与 `sqlite3 .dump` 血缘** | 15s 口播配乐成片与新版本修订、三种打断演练（取消/失败重试/改分镜重门禁）、并发重复 `deliver` 幂等、`sqlite3 .dump` 全链核对、签名更新策略 | **通过（离线/协议/桌面 WebView2） / 未执行（真实供应商与正式私钥签名）** | `go -C gateway test ./internal/pipeline -run TestAcceptance -v`、`powershell -NoProfile -File scripts/verify-lineage.ps1` | 端到端 15s MP4 闭环、三打断演练、8 并发幂等、`sqlite3 .dump` 多表血缘与多参考图顺序还原、跨项目/密钥零泄漏全部通过 | `.runtime/acceptance-lineage/verify-lineage-report.json`、`.runtime/desktop-release-9460b02d1bb04cd88cb85b41a1e9254a`、`.runtime/desktop-release-743fc988efec4df7974a8a50581c87a1` | 真实付费模型调用与正式私钥签名安装包保持 **UNEXECUTED** |
| **新增：本地/云端自定义供应商** | 支持 `openai-images`、`ark-video` 与 LLM（`openai-compatible`/`openai`/`anthropic`）本地/云端保存、默认切换、重启恢复、DPAPI 加密与免费探测 | **通过（配置/恢复/本地协议 fixture） / 未执行（真实远端模型）** | `go -C gateway test ./internal/providers ./internal/pipeline -run TestAcceptanceProviderSaveSwitchRestartAndLocalFixture -v` | 本地与云端图像/视频配置保存、默认切换、重启恢复、DPAPI 密钥零明文泄漏、本地 `/v1/models` 探测全部通过 | `gateway/internal/pipeline/acceptance_test.go`、`.runtime/desktop-release-269559bcca5b47139cf64ff263ec99b4` | 本地协议 fixture 仅验证 HTTP 路径，不据此声称真实本地模型或云端账户生成能力已兼容 |

---

## 3. 专项验收详情与证据核对

### 3.1 本地与云端供应商配置、切换、重启恢复与真实调用边界
- **验证命令**：`go -C gateway test ./internal/pipeline -run TestAcceptanceProviderSaveSwitchRestartAndLocalFixture -v`
- **实际结果**：
  - 成功创建本地图像（`local-img`, `openai-images`）、云端图像（`cloud-img`, `openai-images`）、本地视频（`local-vid`, `ark-video`）、云端视频（`cloud-vid`, `ark-video`）四套配置，密钥通过 Windows DPAPI 加密落盘，SQLite 仅保存不可变 `credential_version` 指针。
  - 默认执行目标从 `local-img`/`local-vid` 切换为 `cloud-img`/`cloud-vid`，关闭并重新打开 SQLite 与 DPAPI 存储后，4 套配置、`has_key=true` 状态及切换后的默认目标完整恢复，`List()` 序列化字节中零密钥原文泄漏。
  - 对 `local-img` 执行 `action: "probe"` 仅发起 1 次只读 `GET /v1/models` 请求并返回 `local_valid=true, remote_tested=true, models_listed=true, model_found=true`。
- **诚实边界**：该检查仅确认本地/云端配置持久化与 HTTP 协议路径，**不声称**真实本地推理服务或云端付费模型已经过生成质量验收；在未配置真实 API 凭据时，绝不将失败调用静默回退为 mock。

### 3.2 15 秒口播配乐视频端到端闭环与版本血缘
- **验证命令**：
  - `go -C gateway test ./internal/pipeline -run "TestVideoNarrated15sEndToEndDeliveryAndRevisionLineage|TestAcceptanceEndToEndNarratedVideoAndLineageExport" -v`
  - `powershell -NoProfile -File scripts/smoke-desktop.ps1 -Mode release -Smoke video`
- **实际结果**：
  - 新建项目上传两张有序参考图（`HeroProductImage`, `DetailTextureImage`），输入“用这张图做个15秒口播视频配轻快音乐”精确匹配 `口播视频` 技能。
  - 依次通过 `intent`、`script`、`storyboard`（15000ms，`15s人声口播+背景配乐`）三道人类确认卡门禁，编译字节级一致的 `final_video_prompt.md` 与 `PromptContract`（`video:seedance`，`duration: 15`, `size: "1280x720"`, `aspect_ratio: "16:9"`, `generate_audio: true`, `audio_mode: "narration+music"`）。
  - 7 门禁 Preflight 返回 `stage: "video_generation_ready", passed: true`；人类批准计费确认卡后提交至 `ark-video` 协议端点，校验 15 秒带音轨 MP4 容器（`1280x720`, `duration_ms=15000`, `audioTracks=1`）并落库为画布可播放节点 `v1`。
  - 对成片或关键帧执行修订投递产生同一 `Resource` 下的 `v2`（`mode: "new_version"`, `baseVersionId: v1`），画布支持 `v1/v2` 切换播放且完整保留上游分镜、Prompt 与参考图血缘。

### 3.3 三种打断演练与幂等投递恢复
- **验证命令**：
  - `go -C gateway test ./internal/pipeline -run TestAcceptanceThreeInterruptionDrillsAndIdempotentDelivery -v`
  - `go -C gateway test ./internal/tasks -run TestRecovery -v`
  - `powershell -NoProfile -File scripts/smoke-desktop.ps1 -Mode release -Smoke interruptions`
- **实际结果**：
  1. **生成中取消**：`queued` 阶段取消调用 1 次远端 `DELETE` 并记录 `TASK_CANCELLED_QUEUED` / `charge_status: "remote_cancelled_queued"`（重复取消幂等零额外 `DELETE`）；`running` 阶段取消不发 `DELETE`，记录 `RUNNING_UNCANCELLABLE` / `charge_status: "local_cancelled_remote_may_charge"`，后台只读轮询在远端完成后弹出 `delivery` 采纳确认卡而不自动写入画布。
  2. **失败后用户重试**：上游返回 `HTTP 429`（`PROVIDER_RATE_LIMITED`）时 attempt 1 立即进入 `failed` 且零自动重试（`postCalls == 1`）；人工发起 `Resolve(action: "retry")` 生成新计费确认卡，人工批准后 attempt 2 执行并成功完成（`postCalls == 2`）。
  3. **分镜批准后改分镜重新门禁**：批准分镜 `rev 1` 并编译合同后，修改分镜产生 `rev 2`，同一事务内使分镜 `rev 1` 批准与未提交合同失效（`valid=0`, `status='invalidated'`），同时保留上游 `intent` 与 `script` 的有效批准（`valid=1`）；未重新批准分镜 `rev 2` 前 Preflight 明确拒绝执行。
  4. **幂等投递与 `unknown` 崩溃重放**：8 个并发 goroutine 使用相同 `requestId` 与相同输入调用 `Deliver` 仅产生 1 条 `Version` 记录；同 `requestId` 异输入返回 `ErrRequestConflict`（HTTP `409 invalid_input`）；模拟 `deliveries` 处于 `stage='archived', status='unknown', receipt=NULL` 崩溃状态后，用原 `requestId` 与原输入重放成功恢复并落盘原预留 `VersionID`。

### 3.4 `sqlite3 .dump` 完整血缘链与导出脱敏验证
- **验证命令**：`powershell -NoProfile -File scripts/verify-lineage.ps1`
- **实际结果**：
  - 导出工具仅导出选定验收项目的数据表记录，自动剥离其他项目（`other-private-project`）、私有素材（`other-secret-asset.png`）、供应商配置表（`provider_configs`、`llm_settings`）、DPAPI 密钥与上游认证 Header。
  - 调用本机 `sqlite3.exe` 对导出库执行 `.dump`（输出至 `.runtime/acceptance-lineage/acceptance-dump.sql`）并通过多表 SQL JOIN 核实完整链路：
    - `storyboard_revisions` (`storyboard Version`)
    - `approvals` + `question_receipts` (`valid=1` 人类回执)
    - `prompt_revisions` (`prompt Version`)
    - `prompt_contracts` (`status='submitted'`, 绑定 `task_id`)
    - `tasks` + `generation_inputs` (`ordinal=0,1` 完整还原多参考图顺序)
    - `task_outputs` (`state='delivered'`, 绑定 `task_output_key` 与 `version_id`)
    - `versions` + `version_dependencies` (`v1` 与修订版 `v2` 同属一个 `Resource`，显式包含 `storyboard_source`、`prompt_contract`、`reference:0`、`reference:1` 依赖边，绝不单靠一条 `source_version_id` 链充当证据)
    - `assets` (`1280x720`, `duration_ms=15000`, `video/mp4`)
- **证据路径**：`.runtime/acceptance-lineage/verify-lineage-report.json`、`.runtime/acceptance-lineage/acceptance-dump.sql`。

### 3.5 安全边界验证
- **验证命令**：`go -C gateway test ./internal/pipeline -run TestAcceptanceSecurityBoundaries -v`、`go -C gateway test ./internal/server ./internal/mcp ./internal/workspace -v`
- **实际结果**：
  - **Origin 与 CORS 预检防护**：启动真实 `open-design-gateway` 进程，无 `Origin`、`Origin: null`、外站 `Origin: https://evil.example` 在普通 API（`GET /api/health`, `POST /api/projects`）、`OPTIONS /api/projects` 预检请求以及 `POST /mcp` 上全部返回 `403 Forbidden`。
  - **MCP 私有令牌校验**：携带合法 `Origin: http://tauri.localhost` 但缺少精确的 `Authorization: Bearer <私有令牌>` 访问 `POST /mcp` 被拒绝（`401/403`）。实现不接受旧文档中的 `X-OpenDesign-MCP-Token` 替代头。
  - **重要安全边界声明**：本机进程（如 `curl`）可自行构造 `Origin: http://tauri.localhost` 请求头，因此 `Origin` 校验**证明的是浏览器跨站请求伪造（CSRF）防护**，不声称提供本机恶意进程隔离；`/mcp` 额外由仅在原生宿主与 gateway 间内存传递的随机 Bearer Token 保护。
  - **文件系统、跨项目与能力白名单隔离**：路径穿越（`../outside.png`、`..\outside.png`）、Windows 目录联接（`mklink /J` junction 重解析点逃逸）、跨项目引用其他项目 Asset ID、以及提交能力 schema 未声明的参数（`undeclared_flag`）全部在副作用发生前被拒绝。
  - **`opencode` 工具权限隔离**：`agent/opencode.json` 与 `agent.BuildConfig` 强制 `"*": "deny"`、`"skill": "deny"`、`"external_directory": {"*": "deny"}`，仅允许 18 项 `design_design_*` 工具与 `question`；注入 `bash` 等原生工具名在配置构建阶段即报错拒绝。

### 3.6 签名安装包、数据升级保留与更新策略门禁
- **验证命令**：
  - `go -C gateway test ./internal/updates ./internal/server -v`
  - `cargo test --manifest-path desktop/src-tauri/Cargo.toml`
  - `powershell -NoProfile -File scripts/release.ps1 -DryRun`
- **实际结果**：
  - 模拟旧测试版本（`0.0.9`）升级至新版本（`0.1.0`）：SQLite 项目、画布节点、文档/媒体 Version、人类确认回执（`valid=1`）与 `write_requests` 幂等回执 100% 完整保留。
  - 更新策略 `ParseCDNPolicy` 与 ` ManualOnlyRuleMatches` 完整覆盖平台与半开版本区间命中、未命中、空规则、损坏 JSON、24 小时缓存边界、时钟回拨过期适配、断网 LKG 兜底、minisign 坏签名拒绝、`manual_only` 阻止自动下载以及任务活动期间（`busy=true`）拒绝安装重启。
  - 正式签名安装包在干净 Windows 账户下的真实安装/升级因缺少正式 `TAURI_SIGNING_PRIVATE_KEY` 保持 **未执行（UNEXECUTED）**。
