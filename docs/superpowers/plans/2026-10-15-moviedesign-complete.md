# MovieDesign 完整实现 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在已交付 M1 上完成 M2–M7，交付可配置本地/云端供应商、具备八图形编辑、AI 对话、结构化批准和完整视频创作血缘的 Windows 桌面应用。

**Architecture:** Tauri 管理 gateway/opencode 两个 sidecar；React 业务请求仅访问 gateway。Go 的 HTTP 与 MCP 共享领域服务，SQLite 保存对象、冻结请求、任务、批准、事件和幂等回执；图像编辑器独立于无限创作画布。

**Tech Stack:** Tauri 2、React 19、TypeScript、Vite、TanStack Router/Query、TipTap、Tailwind、Go（接入 MCP 前升级至兼容修复 SDK 的受支持版本，最低 1.25）、modernc.org/sqlite、golang-migrate、modelcontextprotocol/go-sdk、opencode、tauri-plugin-updater。

## Global Constraints

- 基线：`main@2f85a14`；M1 已完成，不重建仓库，不覆盖原应用及其数据。
- 工作根目录：`E:/AI_Rea_Things/MovieDesign`；原源码只读根目录：`E:/AI_Rea_Things/MinimaxDesignThings1015`。
- 原规格：`docs/spec.md`；用户已确认的适配：`docs/contracts.md`；本文新增决定见下一节。
- “前端永远不直接访问 opencode”，“所有写操作带 requestId”，“禁止自动重试”。
- “params 只放能力声明过的平铺字段；媒体内容只走 references（传 id 不传路径）”。
- “展示与生成必须用同一字符串，禁止摘要转写”；“默认整条重生成，不做局部拼接”。
- 五张核心业务表保持不变；新增能力用编号支撑表迁移，不修改已提交的 000001/000002。
- gateway 只绑 `127.0.0.1`；每请求精确校验 Origin；MCP 不能免检。
- 所有批准来自结构化卡片；agent 不得代点计费/方案/分镜批准。
- 不建设内嵌浏览器、插件系统、技能市场、团队协作、热更新。
- 本地/云端“自定义供应商”是连接配置和已知协议适配，不执行用户脚本，不是插件市场，不声称任意 API 可直接互换。
- 一次只以一个图像配置和一个视频配置作为默认执行目标；允许保存多个配置并显式切换。
- 真实付费调用必须经过应用确认卡；模拟提供商只用于测试，不能计入真实视频验收。
- 使用已安装能力和标准库优先；后续唯一新增前端业务依赖为 TipTap 所需包，编辑器测试使用 Node 内置测试器。
- Windows x64 为首发完整验收平台；其他平台没有通过矩阵就不声明支持。

---

## 0. 当前事实与本轮决定

M1 已提供 `server.Handler(origin)`、`store.Open(path)`、三方法管道、真实 WebView 冒烟。HTTP 仅有健康检查，不能把 M1 的数据库迁移视为 CRUD/投递/任务功能已经存在。

本轮用户确认：

1. BYOK：用户提供官方或自建服务凭据，费用由其供应商收取。MovieDesign 不建立充值、余额、扣款账本。
2. 使用当前 GitHub 仓库 Releases 托管签名更新产物，签名私钥由用户/CI Secret 保管。
3. 允许升级 Go，使用修复后的官方 MCP SDK；原规格 Go 1.24 的硬限制在该点被明确修订。
4. 用户要求可自定义供应商，包括本地和云端 API。因此不把 OpenAI 或火山账号写死为唯一供应商。

供应商实施边界：第一批适配 OpenAI Images 生成/编辑协议和火山方舟视频任务协议；它们是**协议模板**，不是强制账户。设置页允许名称、用途、协议、Base URL、model ID、密钥、允许的附加请求头、价格表和默认配置。LLM 使用 opencode 自身的 provider 配置能力。其他 wire protocol 在 UI 明确不支持，新增时必须带官方/本地服务文档、适配代码和契约测试，不用 URL 猜测协议。

M7 真实验收前必须配置一个实际具有 15 秒、参考图、口播/背景音乐能力的视频连接。模板支持不等于用户账户已经开通，也不等于真实生成已验收。若所选配置没有这些能力，Preflight 应阻止并显示缺项；不得裁剪/拼接或无声降级冒充 15 秒成片。

## 1. 交付结构与依赖

这是一个总计划与六份可独立执行的分计划，共 **30 个任务**。每任务内部按测试、最小实现、复测、文档、提交拆步；任务不等同于一次长时间工具调用。

| 顺序 | 分计划 | 任务 | 可审阅结果 |
|---|---|---|---|
| M1 | `docs/milestones.md` | 已完成 | 壳、数据库、空工作台 |
| M2 | [对象与投递](2026-10-15-m2-objects-delivery.md) | M2.1–M2.5 | 项目、文件、对象 CRUD、崩溃可恢复的幂等投递 |
| M3 | [编辑器与创作画布](2026-10-15-m3-editor-canvas.md) | M3.1–M3.5 | 八图形、历史、标注、持久创作节点 |
| M4 | [供应商与图像任务](2026-10-15-m4-generation-providers.md) | M4.1–M4.5 | 自定义连接、SSE、计费确认、真实图像生成/编辑 |
| M5 | [Agent 与聊天](2026-10-15-m5-agent-chat.md) | M5.1–M5.5 | 受控 opencode、全部 MCP 工具、聊天与问题回执 |
| M6 | [技能与分镜流水线](2026-10-15-m6-storyboard-pipeline.md) | M6.1–M6.5 | 两个技能、批准失效、Prompt 锁定、血缘闭环 |
| M7 | [视频、更新与最终验收](2026-10-15-m7-video-release.md) | M7.1–M7.5 | 真实 15 秒视频、打断演练、签名安装包与更新 |

依赖链：M2 → M3 → M4 → M5 → M6 → M7。M6 的视频合同可用测试适配器验证，但直到 M7 真实视频调用、落库与画布展示全部通过，端到端验收才完成。

## 2. 文件职责与演进位置

| 现有或新增位置 | 责任 | 禁止混入 |
|---|---|---|
| `gateway/internal/server/` | 路由、严格 JSON、Origin、错误映射 | provider 调用与第二套状态机 |
| `gateway/internal/store/` | 对象查询/事务、支撑表 | 长时间联网与等待用户 |
| `gateway/internal/workspace/` | 工作区受管路径、文件归档、指纹 | 任意系统文件读写 |
| `gateway/internal/delivery/` | prepare/archive/commit/replay | 调用模型重新生成 |
| `gateway/internal/events/` | 持久事件与 SSE 回放 | 到内存后才补写数据库 |
| `gateway/internal/providers/` | 配置、能力、报价、图像/视频 wire 适配 | UI 默认价格、动态脚本执行 |
| `gateway/internal/tasks/` | 冻结输入、CAS、worker、等待语义 | 以客户端断线代表取消 |
| `gateway/internal/questions/` | 唯一人类回执入口、批准绑定 | 文本自动授权 |
| `gateway/internal/mcp/` | 官方 SDK、工具 schema、调用域限制 | MCP 专用投递实现 |
| `gateway/internal/agent/` | opencode HTTP/SSE、会话和调用次数 | 自建第二个 LLM 推理框架 |
| `gateway/internal/pipeline/` | 分镜 revision、批准、编译、Preflight | 单靠 prompt 自律的门禁 |
| `desktop/packages/editor/src/` | 无 React 的 Shape/Tool/Renderer/History | 项目节点、远端任务状态 |
| `desktop/src/features/` | projects/assets/editor/canvas/providers/tasks/questions/chat/pipeline | 供应商密钥与权限决策 |
| `desktop/src-tauri/src/` | sidecar 生命周期、签名更新原生调用 | 业务 API 旁路 |
| `agent/` | 中文系统提示词、两个技能、opencode 模板 | 原应用凭据和私有服务端点 |
| `scripts/` | 可重复构建、离线和真实桌面验收 | 未检查目标的递归删除 |

文件路径均相对工作根目录，都是确定路径，不使用 glob 作为实现文件清单。子计划的每个 Files 段进一步列出落点。

## 3. 共享数据与接口规则

### 3.1 项目隔离

继续使用一个应用数据库；受管资产路径为 `projects/<project-id>/files/<asset-id>/<name>`，相对 `<dataDir>/workspace`。新增 `asset_projects(asset_id,project_id)` 记录所有权。业务请求通过 `project_id` 查询参数确定项目，服务端不依赖“全局当前项目”变量；工具上下文绑定会话项目，不接受 agent 越权切换。核心 assets 表的 path 唯一语义保持成立。

`GET/POST /api/projects` 与 `GET/POST /api/assets` 是完成用户验收所需的补充路径，必须在 M2.1 的适配表标为新增面；不伪称原产品原样端点。已有 `/api/compositions`、`/api/resources`、`/api/versions` GET 用 `id` 或 offset/limit；POST 用显式 `action`，新增/修改/删除按 schema 拒绝未知字段。

### 3.2 幂等与错误

幂等主键为 `(project_id, operation, request_id)`；输入指纹来自严格解析后的完整 JSON，字段顺序稳定、数组顺序保留、prompt 字符串不 trim。相同键不同输入返回 HTTP409、`error.code=invalid_input`。幂等键从一次用户意图或工具调用稳定产生；重试请求复用原值，新编辑后的请求换新值。

JSON 与 multipart（字段 requestId）统一要求幂等；MCP 的写工具也显式加入 requestId，这是相对原 execute 的移植补充。opencode 工具 transport 重放必须复用 callID→requestId 映射，不能再随机生成。

错误码仅：`invalid_input / not_found / waiting_user / capacity / upstream`。使用 `details.kind` 表达更细原因，不扩展顶层枚举。常规映射 400/404/409/429/502；受理生成为 202。deliver 的三态是业务回执，不和 HTTP 错误枚举混为一体。

### 3.3 支撑迁移编号

| 迁移 | 表/责任 |
|---|---|
| 000003_workspace_requests | projects、composition_types、asset_projects、write_requests、file_operations |
| 000004_delivery_journal | deliveries、delivery_sources、预分配 ID/归档阶段/最终 receipt |
| 000005_canvas | canvas_documents、canvas_commands |
| 000006_tasks_events | tasks、task_attempts、task_outputs、generation_inputs、events |
| 000007_providers_questions | provider_configs、capabilities、price_quotes、questions、question_receipts |
| 000008_agent_sessions | chat_sessions、chat_messages、chat_turns、agent_tool_calls、project_rules |
| 000009_pipeline | pipelines、storyboard_revisions、approvals、prompt_contracts、version_dependencies |

每个文件均有 `.up.sql/.down.sql`，更新 `migrations/embed.go` 的既有嵌入方式即可。生产不自动执行 down。单连接数据库事务中只调用当前 `*sql.Tx`，禁止事务内回到 `db.Query` 死锁；任何联网和等待用户都必须在事务外。

### 3.4 最小内部类型（Go，放 `internal/tasks/types.go`）

```go
type Status string
const (
    Accepted Status = "accepted"
    Running Status = "running"
    WaitingUser Status = "waiting_user"
    Unknown Status = "unknown"
    Completed Status = "completed"
    Failed Status = "failed"
    Cancelled Status = "cancelled"
)
func Terminal(s Status) bool { return s == Completed || s == Failed || s == Cancelled }
func SettlesAgentWait(s Status, source string) bool {
    return Terminal(s) || s == Unknown || (s == WaitingUser && source != "billing")
}
```

任务总状态、attempt 生命周期、provider 对账状态分开。取消已经提交的远端任务不等于供应商停算：只有供应商确认终止才显示“上游已取消”；否则显示“本地已取消，上游仍可能计费”，保留 upstream ID 并只读对账，晚到文件不自动投递。

## 4. 自定义供应商的实际范围

设置页字段：`id,name,kind(image|video|llm),protocol,base_url,model_id,enabled,default_for,headers,capability,pricing`；密钥是 write-only 输入，读取仅返回 `has_key`。供应商配置写入使用 requestId；删除被任务引用的配置只停用，任务保留配置快照。

- 协议模板：`openai-images`、`ark-video`；LLM 由 opencode 的内置/自定义 provider 支持。
- 本地允许显式配置 `http://127.0.0.1:<model-port>`；云端默认 HTTPS。不能访问 gateway 自身端口、opencode 管理端口或云元数据地址。
- 不接受任意 JS、shell、动态代码模板；没有插件面。
- capability ID 保持 `<operation>:<name>`，使用稳定配置 ID 生成 name。参数 schema 是显式白名单，不将供应商额外字段透传。
- 报价保存币种、来源、版本、生效时间、单位和每单位价格；未知价格不伪造为 0。`estimated_credits` 作为本地显示单位，按配置的有理数换算，以整数最小单位计算，UI 同时展示原币费用和“估算”性质。不建钱包。
- 连接测试只能走免费健康/模型列表；没有此端点时只做本地校验并说明未远端验证，不能用生成试探连接。
- Windows 密钥以 DPAPI 加密保存到应用数据目录，日志、SQLite 快照、前端 GET、Git 都不出现原文。Go 可通过 Windows 系统 API 实现，不引入 CGO。

## 5. 证据优先级及校准

1. 用户已确认的新契约覆盖原产品差异；未知项先读恢复源码再记录适配。
2. 原 deliver 源为 versionId/artifactId/path；本项目按用户指定 path/taskOutputKey。taskOutputKey 是持久唯一 opaque key，与 task_id 和 output_key 关联，不是一个跨任务重复的 primary 字符串。
3. 原 canvas commandId 对应本项目 requestId，内部只有一个幂等身份，不同时维护两个可矛盾的键。
4. 原 activeItem 仅支持 version/task-output；generation-request 属于 pending/history selection，不塞入 activeItem。
5. 原 cancelInteraction 只清快照；pointercancel 按 pointerup 提交。文本 Escape 单独恢复或删除，不把通用 cancel 擅改为 rollback。
6. 原 `steps:400` 不是单轮工具调用计数。本项目按唯一 tool call ID 限制 40 次，拦截第 41 次并 abort。

只读证据：

- `analysis-docs/04-画布编辑器专题.md`、`05-剧本到视频流水线专题.md`、`07-补齐篇-七类空白实证.md`。
- 原 `recovered-renderer-p0/modules/bundle.js:52975`（cancelInteraction）、`:53288`（pointercancel）。
- 原 `recovered-renderer-p3/modules/bundle.js:78048`（ItemRef）、`:78438`（document）、`:80359`（command）。
- 原 `MiniMax Design/current/resources/gateway/dist/main.js:417185`（deliver）、`:417323`（阶段恢复）、`:317899`（question reply）。

## 6. 执行与提交纪律

- 每个任务先运行针对失败用例，确认失败原因来自尚未实现的行为，再写最小实现；不能把语法错误当有效业务红灯。
- 代码片段是需要固定的契约/算法或 runnable check；关联 handler/组件按照明确的 Files、Interfaces 和步骤集成，禁止用示意成功响应替代真实行为。
- 每任务提交示例 `feat(M2.4): recover idempotent delivery`，正文列验证命令与适配点。
- 每里程碑运行更新后的 `scripts/check.ps1`、适用的真实 WebView 冒烟，完成该阶段审查后再按 M2→M7 顺序推送。
- 无真实凭据时继续离线契约、模拟故障和本地集成工作；真实调用验收栏保持“未执行”，不能写通过。
- 实施时不得再询问已确认 Q1–Q4、BYOK、Go 升级、更新托管、自定义供应商是否需要。

## 7. 全规格覆盖索引

| 用户规格 | 落地任务 |
|---|---|
| §0 资料与契约 | M2.1、各阶段适配记录 |
| §1 产品/非目标 | M2.2、M3.5、M4.1、M5.4、M6.5、M7.5 |
| §2 技术栈/进程 | M1、M4.1、M5.1–M5.2、M7.4 |
| §3 核心模型/血缘 | M2.1–M2.5、M6.4 |
| §4 全部 HTTP 路径 | M2.1–M2.5、M3.5、M4.2–M4.5、M5.3–M5.5 |
| §5 全部 design 工具 | M5.2、M5.5（工具矩阵逐个验收） |
| §6 任务/计费 | M4.2–M4.5、M7.1–M7.3 |
| §7 三态投递 | M2.4、M2.5、M7.5 |
| §8 聊天/熔断 | M5.3、M5.4 |
| §9 八图形/标注/TipTap/确认卡/任务 | M3.1–M3.5、M4.4、M5.4、M7.3 |
| §10 中文系统提示词/权限 | M5.1、M5.2、M5.5 |
| §11 两技能 | M6.1 |
| §12 分镜/Prompt/批准/失败 | M6.2–M6.5、M7.2、M7.5 |
| §13 校验/Origin/日志/events | M2.1、M2.3、M4.2、M5.2、M7.4 |
| §14 里程碑提交 | 每个分计划最后一个任务 |
| §15 真实视频、取消重试、批准失效、幂等、SQL链 | M7.5 |
| 新增：本地/云端自定义供应商 | M4.1、M4.3、M5.1、M7.1 |

## 8. 外部依赖及最终完成定义

需要用户在应用内配置实际媒体和 LLM 连接、可用模型 ID、报价和积分换算；这些值不能从逆向应用账户获取。真实验收的图像必须被所用供应商允许上传。签名公钥/HTTPS manifest/签名产物是发布前提，私钥不进入仓库。

全部离线测试通过只是一个门槛。最终完成同时要求：真实 LLM 引导确认卡、真实图像/视频调用、15 秒有口播和音乐的产物本地化、取消/失败重试/批准失效三种演练、SQL 全血缘导出、可编辑新版本、签名安装包和更新策略测试全部有证据。

## 9. 官方资料（实施时核对所锁版本）

- [opencode Server](https://opencode.ai/docs/server/)、[MCP](https://opencode.ai/docs/mcp-servers/)、[Permissions](https://opencode.ai/docs/permissions/)。
- [MCP SDK 安全修复](https://github.com/modelcontextprotocol/go-sdk/security/advisories/GHSA-89xv-2j6f-qhc8)、[v1.4.1 Go 版本要求](https://raw.githubusercontent.com/modelcontextprotocol/go-sdk/v1.4.1/go.mod)。
- [OpenAI Images 编辑协议模板](https://developers.openai.com/api/reference/resources/images/methods/edit)。
- [方舟视频任务协议模板](https://docs.volcengine.com/docs/ark/create-video-generation-task-api?lang=zh)、[取消限制](https://docs.volcengine.com/docs/ark/cancel-or-delete-video-generation-tasks-api)。
- [Tauri updater](https://v2.tauri.app/plugin/updater/)。

以上协议模板只界定适配器，不替用户选定账户。不同部署的模型能力、报价与实际取消行为都必须通过所配置连接核实。
