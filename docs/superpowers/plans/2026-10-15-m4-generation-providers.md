# M4 自定义供应商与图像生成 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 实现本地/云端供应商设置、持久任务与 SSE、唯一计费确认卡，以及真实图像生成和标注编辑落库。

**Architecture:** 配置和 protocol adapter 分离，默认选择是一条配置引用；能力白名单在 gateway。所有提交先冻结任务，批准后才调用 provider，事件与状态在同一事务保存。

**Tech Stack:** Go net/http、SQLite、Windows DPAPI、JSON Schema/validator、React/TanStack、现有确认卡与画布边界。

## Global Constraints

继承总计划。“禁止自动重试”；“计费确认属于用户”；输出先声明后生成。客户端超时不是任务失败，未知上游提交不能盲目重发。BYOK 不建立钱包。

## 文件与接口分工

providers/config 管连接，capabilities 管参数白名单，pricing 管估算，images 管已知 wire；tasks 管状态/attempt，questions 管批准，events 管回放。TaskCenter 读取服务器 actions，不能从 status 自己猜按钮权限。

### Task M4.1：可配置本地/云端供应商与凭据

**Files:**
- Create: `gateway/internal/providers/config.go`, `gateway/internal/providers/secrets_windows.go`, `gateway/internal/server/providers.go`。
- Create: `gateway/internal/providers/config_test.go`, `desktop/src/features/providers/ProviderSettings.tsx`。
- Create: `gateway/migrations/000006_tasks_events.up.sql`, `gateway/migrations/000006_tasks_events.down.sql`, `gateway/migrations/000007_providers_questions.up.sql`, `gateway/migrations/000007_providers_questions.down.sql`。
- Modify: `desktop/src/App.tsx`, `desktop/src/gateway.ts`, `docs/contracts.md`。

**Interfaces:** `/api/providers` GET 返回脱敏配置，POST create/update/disable/select/probe；`Config{ID,Name,Kind,Protocol,BaseURL,ModelID string; Enabled bool}`，追加 explicit capability/pricing/headers 对象；`SecretStore.Put(id,value),Get(id),Delete(id)`；`Resolve(id)(FrozenProvider,error)` 返回任务冻结快照。GET 永不返回密钥。

- [x] 表驱动检查：保存本地端点、保存云端 HTTPS、无支持协议拒绝、拒绝本应用8001/opencode4096/metadata地址、header注入拒绝、GET/log无密钥、停用配置不破坏旧任务快照。

```go
func TestProviderURLs(t *testing.T) {
    for _, tc := range []struct{raw string; allowed bool}{
        {"http://127.0.0.1:11434/v1",true},
        {"https://api.example.com/v1",true},
        {"http://127.0.0.1:8001",false},
        {"http://127.0.0.1:4096",false},
        {"http://169.254.169.254",false},
        {"file:///C:/secrets",false},
    } { if (ValidateBaseURL(tc.raw)==nil)!=tc.allowed { t.Fatal(tc.raw) } }
}
```

- [x] 运行 `go -C gateway test ./internal/providers -run 'TestProvider|TestSecrets' -v`；不存在设置/校验时应失败。
- [x] SQLite 配置不存明文凭据；Windows DPAPI 加密文件以临时文件+rename 更新。只允许定义的额外 header 名，不允许 Host/Origin/Content-Length/Connection 和 CR/LF。DNS解析、redirect和产物下载同样验证目标，不能通过URL跳转访问禁止地址；用户明确配置的本地模型端口允许访问。
- [x] 设置 UI 支持增删改、默认用途、可见 Base URL/model/protocol、write-only key、价格/能力编辑、免费连接测试。没有免费探测API的协议显示“配置校验通过，远端未测试”，不能偷偷调用生成接口。JSON 明确区分 capability 声明与 probe 已验证事实。
- [x] 将新端点与字段加入适配表；保存重启、切换默认配置、错误密钥展示均验证，提交 `feat(M4.1): configure local and cloud providers`。

### Task M4.2：任务、attempt、事件持久化与 SSE

**Files:**
- Create: `gateway/internal/tasks/types.go`, `gateway/internal/tasks/service.go`, `gateway/internal/tasks/worker.go`, `gateway/internal/events/service.go`, `gateway/internal/server/tasks.go`, `gateway/internal/server/events.go`。
- Test: `gateway/internal/tasks/service_test.go`, `gateway/internal/events/service_test.go`。
- Modify: `gateway/internal/server/server.go`, `gateway/internal/control/stdio.go`, `gateway/cmd/open-design-gateway/main.go`。

**Interfaces:** `Submit(ctx,projectID,Input)(TaskView,error)`、`Query(ctx,projectID,id)(TaskView,error)`、`Resolve(ctx,projectID,id,ResolveInput)(TaskView,error)`、`Wait(ctx,projectID,id)(TaskView,error)`。Input 含 requestId/capability_id/prompt/params/references/output；ResolveInput 含 requestId/action/expected_revision。TaskView 按 §6 字段，状态谓词复用总计划定义。

- [x] 写状态矩阵、乐观锁冲突、billing等待穿透、ordinary等待停止、unknown停止、重复提交返回原task、两个worker不重复submit测试：

```go
func TestWaitSemantics(t *testing.T) {
    for _, tc := range []struct{s Status; source string; settles bool}{
        {Accepted,"",false},{Running,"",false},{WaitingUser,"billing",false},
        {WaitingUser,"validation",true},{Unknown,"",true},
        {Completed,"",true},{Failed,"",true},{Cancelled,"",true},
    } { if SettlesAgentWait(tc.s,tc.source)!=tc.settles { t.Fatalf("%s/%s",tc.s,tc.source) } }
}
```

- [x] `go -C gateway test ./internal/tasks ./internal/events -v`，确认没有状态/CAS/持久回放时失败。
- [x] tasks 保存冻结输入、revision、状态、当前attempt；task_attempts保存upstream ID、提交开始/受理/结果、不可变错误；generation_inputs保存有序 asset/version 引用；task_outputs保存唯一opaque taskOutputKey及文件状态。CAS 使用 `UPDATE tasks SET status=?,revision=revision+1 WHERE id=? AND revision=?` 检查影响行数，并在同一事务更新完整任务视图与events。
- [x] events 保存单调 ID、project/session/task、type、payload、时间。SSE `id:` 对应持久ID，`Last-Event-Id` 后重放，15秒注释心跳；先订阅通知后追平持久游标避免 replay/live 缝隙。客户端慢消费者断开后靠游标追赶，不丢事件也不无限增长内存队列。MVP不剪裁events，标注增长上限与未来按保留策略迁移的 ponytail 注释。
- [x] worker 只运行已批准任务；重启时 running+upstreamID 查询上游，running无ID但submit已开始→unknown，绝不自动重提。health idle/safe_to_suspend改为真实活动计数，管道与HTTP共享读法。提交 `feat(M4.2): persist task transitions and replay events`。

### Task M4.3：能力协议、参数白名单与图像适配器

**Files:**
- Create: `gateway/internal/providers/capabilities.go`, `gateway/internal/providers/images.go`, `gateway/internal/providers/pricing.go`。
- Create: `gateway/internal/server/generate.go`, `gateway/internal/providers/images_test.go`, `gateway/internal/providers/capabilities_test.go`。
- Modify: `gateway/internal/tasks/service.go`, `gateway/internal/server/server.go`。

**Interfaces:** `Capability{ID,Operation,Protocol,ModelID,KnowledgeCard string; ParamSchema json.RawMessage; Constraints Constraints}`；providers包定义`GenerationInput{Prompt string; Params map[string]any; References []string; Count int}`，tasks.Input通过显式转换传入，providers不import tasks；`Validate(capability,GenerationInput)error`；`Estimate(input,frozenConfig)(Quote,error)`；`GenerateImage(ctx,input,frozenConfig)(ProviderResult,error)`。仅支持已知协议 `openai-images`：POST images/generations JSON、images/edits multipart。Base URL 与模型来自配置，不是硬编码官方账号。

- [x] httptest.Server 捕获 wire：模型名、prompt 原字符串、有序 image multipart、mask、参数白名单、结果 b64/url、401/429/5xx、submit断流。确认 references 中出现 path/URL 而非 asset id 时在联网前拒绝。

```go
func TestUnknownParameterRejected(t *testing.T) {
    cap := Capability{ID:"image:local",Operation:"image",ParamSchema:json.RawMessage(`{"type":"object","properties":{"size":{"type":"string","enum":["1024x1024"]}},"additionalProperties":false}`)}
    in := GenerationInput{Prompt:"保持原文\n",Params:map[string]any{"undeclared":true}}
    if err := Validate(cap,in); err == nil { t.Fatal("undeclared field passed gateway") }
}
```

- [x] `go -C gateway test ./internal/providers -run 'TestImage|TestUnknownParameter|TestQuote' -v`。
- [x] capability list/get 返回 id、paramSchema、引用/尺寸/时长限制与知识卡路径。prompt 上限按用户64KB采用 UTF-8字节≤65536，同时满足所配模型更小限制；原源码64000字符差异记入适配表，Go与前端一致。count 合法范围来自能力，拒绝嵌套未声明params、缺output、虚假asset、跨项目引用。
- [x] 能力schema采用gateway拥有的受限JSON Schema子集：object/properties/required/additionalProperties=false、string/number/integer/boolean、enum、数值范围和字符串长度。配置仅可收紧适配器已知字段/约束，不能宣告任意透传字段；不支持的schema关键字在保存时拒绝，不能显示已校验而实际跳过。count变体预先声明全部output key及role，输出数量不符进入可解释问题状态，不产生无身份文件。
- [x] 新增`POST /api/generate/estimate`作为BYOK适配端点，复用输入校验与报价函数，保存带requestId的quote；返回quote_id、currency、estimated_amount、estimated_credits、expires_at及input_hash。该请求不调用生成器；确认卡绑定服务器保存的quote，不能接受前端自报金额。路径和字段作为移植新增面记录，不冒称原产品协议。
- [x] 报价输入冻结并hash；精度采用整数最小货币单位与有理数积分倍率，上调取整，不用浮点金额；quote保存source/version/expires_at。未配置报价的付费连接阻止提交并解释，不能默认为免费；本地免费需显式价格为0。图像mask由标注几何生成后作为受管资产登记，原图和mask均走有序references；编辑能力知识卡明确各位置角色，params不得塞base64/path/URL。mask使用原图像素坐标并匹配尺寸，不承诺模型严格保持mask外内容。
- [x] 供应商返回的媒体先下载/解码至确定受管目标，校验内容与大小，再生成 task_outputs；断流未知不会自动再次POST。记录真实官方/本地协议样本，提交 `feat(M4.3): validate capabilities and adapt image APIs`。

M4.3 已在 f033c6f 独立复审通过；最后一项的安全收集实现已验证，最终 task_outputs/Delivery 接线归 M4.5 完成后勾选。真实供应商调用尚未执行。

### Task M4.4：统一确认卡与计费执行门禁

**Files:**
- Create: `gateway/internal/questions/service.go`, `gateway/internal/questions/service_test.go`, `gateway/internal/server/questions.go`。
- Test: `gateway/internal/tasks/billing_test.go`（下方等待谓词测试属于tasks包）。
- Create: `desktop/src/features/questions/ConfirmationCard.tsx`, `desktop/src/events.ts`, `desktop/src/events.test.ts`。
- Modify: `gateway/internal/tasks/service.go`, `gateway/internal/tasks/worker.go`, `desktop/src/App.tsx`, `docs/contracts.md`。

**Interfaces:** `Ask(ctx,projectID,Question)(Question,error)`、`Reply(ctx,projectID,id,HumanReply)(Receipt,error)`；Question 包含 kind=billing/choice/storyboard、session_id/task_id、revision、input_hash、状态、选项；HumanReply含requestId/answers/expected_revision。新增GET /api/question及POST /api/question/:id/reply、/:id/reject，作为用户要求入口的必要回执补充。服务端channel固定human，MCP不注册approve/reply工具。

- [x] 测试批准前provider调用数0、一次批准调用1、重复回执调用仍1、旧revision/过期quote拒绝、聊天“同意”不批准、拒绝后cancelled、重连卡片恢复：

```go
func TestBillingIsNotASettledWait(t *testing.T) {
    if SettlesAgentWait(WaitingUser,"billing") { t.Fatal("billing wait ended early") }
    if !SettlesAgentWait(WaitingUser,"choice") { t.Fatal("ordinary decision blocked agent") }
}
```

- [x] `go -C gateway test ./internal/questions ./internal/tasks -run 'TestBilling|TestQuestion' -v`；无确认服务时费用隔离用例失败。
- [x] Reply 在一事务核对问题状态、project/session、task revision、input hash、报价版本/有效期，再写 receipt并允许worker；有效期过后重新报价/新卡，不能偷偷用旧批准。retry新增费用必须新计费卡。question source/actor由入口确定，不信任body中的user声明。
- [x] React 共用一套 ConfirmationCard，不用聊天文本代替。展示原币金额、积分估算、提供商、模型、动作影响、过期状态。所有按钮有键盘和屏幕阅读器名称；提交期间禁用重复点击。events.ts 单连接复用、按项目/会话分发、基于ID去重并保存重连游标。
- [x] 使用真实WebView验证拒绝/重复点击/重连/旧卡片；提交 `feat(M4.4): gate generation with human billing receipts`。

M4.4 b120287 独立审查通过；生产 WebView 证据早于最后几项窄修复，最终全包/专项检查覆盖后续改动，精确最终生产验收归 M4 整体验收。过期卡片拒绝入口 P3 交 M4.5 UI 收尾。

### Task M4.5：图像任务、投递、任务中心与 M4 验收

**Files:**
- Create: `desktop/src/features/tasks/TaskCenter.tsx`, `desktop/src/features/tasks/tasks.ts`, `desktop/src/features/tasks/tasks.test.ts`, `scripts/smoke-generation.mjs`。
- Modify: `gateway/internal/tasks/worker.go`, `gateway/internal/delivery/service.go`, `desktop/src/features/editor/ImageEditor.tsx`, `desktop/src/features/canvas/CanvasNode.tsx`, `desktop/src/App.tsx`, `scripts/check.ps1`, `docs/milestones.md`。

**Interfaces:** `POST /api/generate/image`→202 `{task_id}`；`GET /api/tasks/:id`；`POST /api/tasks/:id/resolve`；成功产物通过同一 delivery.Service 交付。生成请求保存output，worker不能自行猜Composition/role。

- [x] 集成用本地httptest provider执行完整approved→active→download→deliver→completed；故障测试包括provider拒绝、未知提交、下载失败可续下载、投递回执丢失、取消与完成竞态。
- [x] `go -C gateway test ./...`、`node --experimental-strip-types --test desktop/src/features/tasks/tasks.test.ts desktop/src/events.test.ts`，然后运行真实桌面smoke-generation。
- [x] 任务UI按服务器phase/percent/problem.upstream/actions展示；retry原样冻结prompt/params/references，CAS检查旧revision。对failed的显式retry新增attempt并进入新的billing等待，旧attempt不可变；这是一条明确用户恢复边，不是自动终态跳转。unknown默认只查询对账；无法证明未受理时要求用户明确了解可能重复收费后才允许新attempt。
- [x] 完成后投递使用稳定requestId（task+attempt+output），将pending节点替换成version引用一次。取消晚到结果保留upstream对账事实与文件状态，不自动投递；用户另行选择采用时再走deliver。卡片因任务取消/完成及时失效。
- [ ] 在用户配置的真实图像连接执行一次文生图、一次标注修复，记录实际provider/model/任务ID与落库版本；不把测试适配器当真实成功。全量通过后提交 `feat(M4.5): deliver image tasks into the canvas`，推送M4。

M4.5 工程项已在 91d7d31 经独立复审通过（Spec compliant / Task quality Approved），三项 R1–R3 均关闭。当前生产构建/真实 WebView fixture 初次与重启、完整检查及 14 项未缓存生成集成检查通过；完整证据在里程碑记录与本地报告。末项真实凭据文生图/标注修复仍未执行，保留未勾选。整 M4 审查、合并与推送另行记录；依据总计划§6，真实调用缺少配置不阻止后续独立 M5 工程。
