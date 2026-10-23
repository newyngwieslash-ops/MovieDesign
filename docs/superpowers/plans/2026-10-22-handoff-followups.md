# MovieDesign H01–H16 Follow-up Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在现有 M1–M7 上补齐交接指出的真实更新执行链及其他实现缺口，并分别记录真实生成与签名发布验收。

**Architecture:** HTTP/MCP 继续共用 Go 业务服务；原生 Rust 使用已安装的 tauri-plugin-updater 执行更新。更新候选在原生侧冻结，前端只展示与提交结构化人类决定，安装前由 gateway 排他准入阻止新的业务副作用。原始手工同意、下载/验签、安装意图分别记录，不把重试未知安装当成安全操作。

**Tech Stack:** 现有 React 19/Tauri 2/Go 1.25.8/SQLite；锁定 tauri-plugin-updater 2.14.0；既有 semver、minisign-verify、base64；Node 内置测试器。无新框架。

## Global Constraints

- “前端永远不直接访问 opencode”，“所有写操作带 requestId”，“禁止自动重试”。
- “所有批准来自结构化卡片；agent 不得代点计费/方案/分镜批准。”更新同样使用结构化确认形式；agent 工具面不增加安装权限。
- “gateway 只绑 127.0.0.1；每请求精确校验 Origin；MCP 不能免检。”
- “五张核心业务表保持不变”；当前迁移到000014，只有必要支撑变更才新增编号，不改旧迁移。
- “展示与生成必须用同一字符串，禁止摘要转写”，“默认整条重生成，不做局部拼接”。
- 原应用 E:/AI_Rea_Things/MinimaxDesignThings1015 只读。保留32项现有统计/换行修改标记与未提交交接文件；不用 reset/clean/批量暂存。
- 基线 main@f9432ae6dd485f51441d7f6202e127107ef1966e；新分支 codex/h01-signed-update-runtime。不重做 M1–M7。
- Windows x64；GitHub仓库仅newyngwieslash-ops/MovieDesign，策略/manifest URL使用既有契约。无正式密钥/真实模型证明时保持UNEXECUTED。
- 仅复用既有依赖/服务/确认组件；无钱包、插件、账户系统或自动生成回退。

## Task H01：真实更新执行、结构化授权与安装排他准入

**Files / ownership:**
- Modify: desktop/src-tauri/src/updates.rs、lib.rs、gateway.rs（原生候选/检查/进度/安装、受管进程退出）。
- Modify: desktop/src/features/settings/UpdateSettings.tsx、desktop/src/features/questions/ConfirmationCard.tsx（只在必要时提取/复用既有结构化决定渲染）、desktop/src/gateway.ts（仅必要连接元数据）。
- Modify: gateway/internal/server/updates.go、server.go，配套 updates_test.go / server_test.go（原请求回执、排他安装准入；不改变业务协议）。
- Create if needed: desktop/src-tauri/src/update_runtime.rs（只有为独立运行时责任而拆分，不建通用插件框架）、scripts/check-update-ui.mjs、scripts/smoke-updates.mjs。
- Modify: scripts/check.ps1、scripts/smoke-desktop.ps1（注册实际新检查）、docs/contracts.md（明确新增更新控制面）。
- Parent owns: README/docs/acceptance/milestones、本计划、handoff状态和审计账本。其他文件仅为任务必要且先报告原因。

**Interfaces / exact expectations:**
- Existing command updater_check_and_gate 必须调用真实 updater.check()，返回实际候选版本/状态；运行版本取app.package_info().version，忽略/拒绝前端伪造版本。
- 保存原生 Update 对象及版本、URL、签名、候选身份；安装入口只接受候选身份+人类决定回执+requestId，不接受前端指定包URL、公钥、包bytes或allow_loopback。
- 复用现有结构化确认卡形式；确认绑定当前候选和安装/退出影响。批准前不下载/安装，拒绝、旧候选、旧回执和同键异输入均拒绝；不以 window.confirm 或普通聊天替代。全局更新不得制造假业务项目以满足FK。
- 若需新增应用级更新决定/准入端点，在docs/contracts说明移植补充；使用现有write_requests的_app作用域或最小必要受管记录，保持幂等/原请求恢复。不能让模型或原生question回答伪造更新批准。
- 独立 download 与 install：download走官方插件并执行签名验证；安装前再次核对策略/候选和gateway安全状态，并领取排他准入。准入与业务请求活动登记共享同步边界，不能留下新任务进入的TOCTOU窗口；未成功领取时零安装调用。
- 准入期间拒绝新的业务写入/MCP副作用，状态读取和该原请求查询/必要清理继续可用；失败安全释放，不自动重装；开始安装前持久化原意图，应用重启不会自动重复安装。
- 保持本仓库Release来源限制；不能直接信任任意objects.githubusercontent.com地址或loopback生产覆盖。GitHub真实CDN跳转按照插件/签名/源metadata边界验证，具体实现按锁定SDK，不自创更新协议。
- 保留插件原始签名协议。现有额外version trusted-comment规则要与锁定官方CLI产物核对，不能靠手写测试向量声称支持生产包。H03负责正式材料，H01不得生成假生产签名。
- Progress用现有Tauri Channel/事件，错误可显示；网络请求不持有同步Mutex或SQLite事务；安装退出前先让自有 gateway 完成聊天/任务清理，再请求自有 agent 清理资源并给进程退出宽限，超时仅回收本应用持有的子进程句柄。此处恢复已确认 M1 的“优雅关闭优先、有界自有进程兜底”准则；不能把进程消失或强制终止宣称为正常退出。

**步骤与验证：**
- [x] 追踪现有UI→Rust命令→策略→插件路径及现有确认/原请求模式；读取本机锁定crate源码和官方文档。保存根因证据，不先做随机改动。
- [x] 写实际组件行为RED：渲染UpdateSettings，点击“检查”只触发检查；候选出现后必须通过明确确认卡；未确认、旧候选、storage失败零安装；批准后同候选只提交一次，进度/错误更新。复用scripts/check-storyboard-ui.mjs的实际TSX加载方法，不检查源码字符串当成功。
- [x] 写Rust/Go行为RED：真实本地HTTP updater fixture走插件check/download；有效测试签名下载通过，坏签名/版本错配/非本仓库候选拒绝，安装sink为明确测试注入且计数；busy/manual_only/策略失败/准入竞态零install调用；领取后其他业务POST/MCP被拒绝、释放后恢复。

```rust
// 行为边界示例：安装调用必须在最终准入之后，测试验证计数而非只验证报告字段。
// 在现有SDK fixture中串联check/download与测试安装sink，断言顺序：
// human_receipt_saved -> downloaded_and_verified -> exclusive_admission -> install_started。
assert_eq!(install_calls.load(std::sync::atomic::Ordering::SeqCst), 0); // busy/拒绝分支
```

- [x] 跑新增用例并记录真实业务失败；编译错误不作为复现通过。按共享根因实现上面接口，再跑同一用例GREEN。
- [x] 当前范围与整仓检查：最终源码 `1d0923b` 的 `scripts/check.ps1` 退出 0，覆盖本项命令及后续修复。源代码有变更才重跑；各次结果按实际提交保留。
- [ ] 当前生产构建和受控WebView验证检查/卡片/进度/拒绝/原请求恢复。安装测试只针对受控测试包/测试安装sink，不触碰真实用户已安装应用；正式NSIS安装另列H13。
- [x] 记录结果、源/制品对应和UNEXECUTED边界；`d3d2f6e`、`e2e8282` 已提交并推送。独立首审四项 Important 经限定复审全部关闭，工程范围 Approved；本地插件安装 sink 不等于真实安装。当前生产初始安全 UI 通过，但完整恢复冒烟因 Windows 保留 4096 失败，未执行部分保留 UNEXECUTED。

官方参考：https://v2.tauri.app/plugin/updater/；本机Cargo.lock锁2.14.0。官方支持check、download和install分离，不能因builder可创建就宣称运行时链路完成。

## 后续任务边界（按交接H01–H16，不重复原里程碑）

- [x] H02 工程：e111430 修复 Go SemVer 与半开区间，独立审查 Approved；8273eb7 补发布/Tag/Cargo/Tauri/npm/PE 版本保护。原生版本来自 H01，完整安装版本验收仍 UNEXECUTED。
- [x] H03 工程：8273eb7 正式公钥/私钥输入保护、签名产物协议、CI pnpm 对齐；31 组检查及实际公开向量验签通过，独立审查 Approved。正式密钥配对、clean runner、签名包发布仍 UNEXECUTED，不以工程勾选冒充发布完成。
- [x] H05：按已确认5xx-only计划修正重试漂移。e60b89b；公开 Evaluate 的十例 RED/GREEN、包测试和 vet 通过；独立审查 Spec compliant / Approved。原产品更广网络重试作为有意适配记录。
- [x] H06：移除 Git 撤回，精确自有路径/进程身份，优先正常退出、错误非零；17 组回归通过。63c16f8、bc92da8；首审退出竞态已修，限定复审 Approved。真实原生生命周期另记 UNEXECUTED。
- [x] H04：c1ac3ae 以实际帧/时间基替代固定500ms；CFR/VFR、空音轨、证据幂等、探测输出上限检查通过，独立审查 Approved。精确技术证据写现有 events；真实模型成片仍 UNEXECUTED。
- [x] H07：用实际修复结果更新四阶验收/文档和计划勾选，保留历史记录；当前运行/发布未完成项目仍不虚勾。
- [ ] H08–H12：用户在安全渠道配置真实可用连接后，经费用卡验证真实模型/技能/图像/整条视频/打断/血缘；否则只对应项UNEXECUTED，继续其余工程。
- [ ] H13：匹配正式签名材料后验证干净账户首次安装/升级与真实更新策略，才发布Release。
- [ ] H14：物理 DPI/IME/焦点验收仍 UNEXECUTED。
- [x] H15 离线工程：`18a8710`、`9bc10b9` 修复权威恢复提示；`a5fdaa1`、`da2599d`、`3cc5e0f` 实现安全流式及恢复/凭据边界，任务审查问题全部关闭。真实模型首 token 体验仍 UNEXECUTED；现有 Vite 包警告待测量启动成本后再决定，不先做框架重构。
- [x] H16 工程审查/回归：整分支首审 1 Important、1 Minor，在 `1d0923b` 同波修复后唯一限定复审全部 ADDRESSED，Spec PASS / Quality Approved；34 项针对性 UI、15 项 Rust（1 既有忽略）、最终整仓检查与生产构建通过。已达到目标版本可明确结束本地更新恢复提示，原安装审计保留；纯文本流不再反复取消任务 GET。
- [x] H16所选生产恢复门槛：2026-10-23实际4096可用，原标准updates initial/recovery、chat initial/recovery/restart通过；WinNAT未改。保留此前单次正常退出超时原因未定的限制，不称根治。真实模型和签名安装升级仍另列UNEXECUTED；当前证据见[恢复验收](../../handoffs/2026-10-23-recovery.md)。

### H15 离线接续边界（2026-10-22）

- H15a：复用现有权威任务状态，修复已完成任务仍显示恢复、旧失败横幅和图像/视频混用文案；保留原批准审计及未知请求，代码与必要 smoke 断言同步后独立审查。
- H15b：先用延迟本地 SSE 证明整包缓冲的影响，沿实际 relay→opencode→持久聊天事件修复安全增量；完整验证前不得执行工具，不得因断流或重启产生第二个供应商 POST。必要支持迁移另编号，旧迁移/五核心表不改。真实模型首 token 与实际流式体验仍须单列 UNEXECUTED。

后续每个任务在分派前追加实际base、文件责任、具体回归命令和报告路径；当前先执行H01，不等待再次确认。

### H19：关闭超时取证与真实验收接续（2026-10-23）

基线 `eb4f2c3`。用户要求先排查此前原因未明的正常关闭超时，再接续 H08–H14；保留原失败记录，不能用后续成功认定根因已修复。

- [x] 追踪实际窗口退出、gateway drain/dispose、自有 agent 回收和 Tauri 收尾；用现有原生 stderr 与关闭时间取证区分阶段。没有复现证据不修改应用生命周期。
- [x] 在既有 `scripts/smoke-desktop.ps1` 最小补入每次启动独立 stderr、关闭请求/等待返回时间、超时自有子进程身份快照和清理结果。保留原 `WaitForExit(10000)` 及全部进程/端口断言；采集收尾有界，不等后代管道 EOF，不记录环境或凭据。
- [x] `scripts/check-desktop-shutdown.ps1` 从真实脚本加载对应函数，先 RED 后 GREEN 验证超时仍失败、证据先落盘、多次启动不串日志、采集等待有界；注册到现有 `scripts/check.ps1`。全量发现 CLIXML fixture 问题与独立审查发现释放异常覆盖原错误，两项 fix1 已限定复审通过。
- [x] 最终源码全量检查退出0；生产 updates initial/recovery 退出0，正常关闭1293/1347ms，自有进程/端口收尾通过。未复现原超时，不以诊断增强冒充应用修复。审查通过后代码提交 `80b3a23`；文档/推送另记交付结果。
- [ ] H08–H14 按既有人工卡片和真实证据继续；默认用户目录只读核对发现已有 LLM/图像配置、没有视频配置或真实项目。配置存在不代表远程推理通过；不读密钥、不重复填已有配置、不自动批准费用。

实现责任仅上述三个脚本；父级负责文档/Git，任务记录置于 `.superpowers/sdd/2026-10-22-handoff-followups/`。应用源码、端口、三方法 JSON-RPC、供应商协议和核心表均不变。真实调用必须先满足应用内人类授权；正式签名、安装升级和物理设备未执行项继续记 `UNEXECUTED`。

### H20：已批准的聊天计费门禁（2026-10-23）

H08 独立调用链审查发现普通聊天首轮无费用卡。用户已明确批准扩展既有 `/api/question`：billing 可绑定 `chat_message_id` 或 `task_id`（互斥），聊天批准绑定冻结模型、本轮调用次数和总估算；没有人工回执不发 LLM 请求。不把普通 choice 转换为 billing，不伪造生成 task/output。

- [ ] 最小新增聊天支撑绑定，复用现有 questions/receipts/quotes；旧迁移及五核心表不改，新支撑变更从000016编号。
- [ ] Chat.Send 幂等创建原消息和计费意图；worker 和 Relay 两处校验原人工回执，Relay 原子限制请求次数及上下文/输出预算；保留 unknown 防重发和原生40工具上限。
- [ ] 实际 ConfirmationCard 区分聊天/生成目标，正确续报价、拒绝与原请求恢复；原生成费用批准行为保持。
- [ ] 真实服务/Relay与实际组件 RED/GREEN，固定 opencode + 本地合成上游的已批准首轮/工具后续验证；独立审查、最终全量/构建/桌面回归后提交推送。
- [ ] 用户在应用聊天费用卡上批准后再进行真实 H08；图像、视频、真实项目血缘、正式签名安装和物理设备仍按 H09–H14 分别取证。

H20 单一实现 worker 负责必要 Go/确认卡及回归；父级独占文档/Git和真实应用。当前常规用户应用有 H09 待批准图像任务，不得由 worker 操作其数据或占用生产8001/4096。实现要求和状态保存在本计划 workspace 的 `h20-chat-billing-brief.md`/报告/账本。既有自定义供应商、BYOK、无钱包范围不变。

### H17：4096 阻塞与桌面恢复接续（22:14）

- [x] 从 f8b19ff 增量接续，实际复现无监听但4096受系统4042–4141保留、bind10013；不换端口或修改系统配置。b992eee 区分真实绑定错误，e0400aa 修复独立审查发现的配置后台探测缺口，限定复审通过；当前全量检查与生产构建退出0。
- [x] 当前生产运行确认 port_permission_denied、DPAPI临时交换已删除、无启动成功证据；初始更新安全检查通过。保留标准恢复冒烟退出1和自有进程清理证据，不标整体PASS。
- [x] 2026-10-23用户明确授权临时中断WinNAT；复核时4096已可绑定，所以未停止服务或发起UAC。标准updates/chat实际桌面恢复已通过，详见当天证据。原待授权/阻塞是历史状态，未执行的系统中断不虚勾为已做。

详细文件责任、测试和系统动作边界见 [H17计划](2026-10-22-port-recovery.md)，结果见 [接续记录](../../handoffs/2026-10-22-followups.md)。真实调用、正式签名与安装升级仍 UNEXECUTED。
