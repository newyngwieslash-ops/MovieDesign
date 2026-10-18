# M5 Agent、MCP 与聊天 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [x]`) syntax for tracking.

**Goal:** 让受限 opencode 通过 gateway 工具完成真实对话、提问、生成与投递，前端支持引用、排队和流式输出。

**Architecture:** Tauri 持有两个 sidecar；gateway 是会话、权限、事件及人类回执边界。MCP 只包装已有 Go 服务，opencode 的 HTTP session 服务不直接暴露给前端。

**Tech Stack:** Go ≥1.25 与修复版官方 MCP SDK、锁定版本的 opencode、React 19、TipTap、已有 SQLite/SSE/确认卡。

## Global Constraints

继承总计划。禁止原生 bash/read/write/edit/todowrite 及其他未明确允许的工具；只允许 design 工具和原生 question。每轮最多40次工具调用，不能用 steps 配置冒充调用计数。用户配置 LLM 的本地/云端 API，不写死账号或模型。

### Task M5.1：工具链、opencode 配置与 sidecar 生命周期

**Files:**
- Modify: `gateway/go.mod`, `gateway/go.sum`, `desktop/src-tauri/src/lib.rs`, `desktop/src-tauri/src/gateway.rs`, `desktop/src-tauri/tauri.conf.json`, `desktop/src/features/providers/ProviderSettings.tsx`。
- Create: `desktop/src-tauri/src/opencode.rs`, `agent/opencode.json`, `gateway/internal/agent/config.go`, `gateway/internal/agent/config_test.go`, `scripts/build-opencode.ps1`, `docs/third-party.md`。

**Interfaces:** Tauri 在 gateway 就绪后启动独立 opencode serve，固定127.0.0.1:4096；gateway 代理所有 session/config/auth 请求。`BuildConfig(LLMConfig,MCPConnection) ([]byte,error)` 输出隔离配置。LLMConfig 区分内置 provider 与受 opencode 支持的自定义兼容协议，包含 baseURL/modelID/write-only key；不允许任意 npm 包名或插件路径。

- [x] 先测试配置：默认拒绝工具、配置外未知工具拒绝、key 不出现在 API GET/log、项目内恶意 opencode 配置不被合并、端口被占用立即失败、退出只关闭自身子进程。
- [x] 升级项目 Go 工具链，锁定满足官方公告修复要求的 SDK（至少 v1.4.1，核对 go.mod）；运行 `go -C gateway test ./...`。读取锁定 opencode 的配置 schema 和 `/doc` OpenAPI，保存用于测试的最小脱敏样本，不能假设当前 npm shim 就是可分发 exe。
- [x] 构建脚本从官方发行下载指定版本/架构的二进制并校验可获得的官方摘要；保存版本、来源、摘要、许可证清单。不修改用户全局 opencode；Tauri externalBin 包含本应用专用 binary，配置/会话/日志使用应用数据目录。
- [x] MCP remote 配置包含精确 Origin 和随机连接令牌；令牌只给受管 opencode。对原生 question 允许，工具总权限默认 deny，其余仅按实际 MCP tools/list 返回的前缀名称逐项 allow。禁用项目配置合并、自动技能发现和插件加载；技能由 gateway 注入。
- [x] opencode serve 使用官方 `OPENCODE_SERVER_PASSWORD` HTTP Basic 认证，每次启动生成随机密码，经受保护的原生启动配置交给 gateway；前端、API GET 和日志均不得获得密码。只允许 gateway 的认证客户端调用 session/config/question API，不为前端开启4096的CORS。测试无认证请求被拒绝、gateway认证请求成功，并验证工具权限关闭后原生HTTP管理接口仍受认证保护。依据锁定版本的官方 Server 文档核对变量与认证行为。
- [x] 设置页新增 LLM 配置与免费探测，只有已支持的 opencode provider 协议才能保存。改配置影响新会话/新轮次，不更换已在执行任务的模型；必要重启先排空会话。启动错误在 UI 可读，agent 未就绪不冒充正常。
- [x] 运行 `cargo test --manifest-path desktop/src-tauri/Cargo.toml` 和 `./scripts/check.ps1`，真实启动/关闭验证两个子进程；提交 `feat(M5.1): host isolated opencode with configurable providers`。

### Task M5.2：官方 MCP 服务、工具 schema 与执行预算

**Files:**
- Create: `gateway/internal/mcp/server.go`, `gateway/internal/mcp/tools.go`, `gateway/internal/mcp/server_test.go`, `gateway/internal/agent/budget.go`, `gateway/internal/agent/budget_test.go`。
- Create: `gateway/migrations/000009_agent_sessions.up.sql`, `gateway/migrations/000009_agent_sessions.down.sql`。
- Modify: `gateway/internal/server/server.go`, `gateway/cmd/open-design-gateway/main.go`, `docs/contracts.md`。

**Interfaces:** MCP 使用官方 Streamable HTTP handler 挂载 `/mcp`，同样校验 Origin，并验证专用令牌。`ToolScope{ProjectID,SessionID,TurnID}` 由 gateway 的受管会话绑定，不能由模型任选。写工具显式要求 requestId；tool requestId 与重放记录持久化。服务注入对象、投递、任务、问题服务，不复制业务算法。

- [x] 官方 SDK client 对真实 httptest HTTP server 做 initialize/tools/list/call：错误 Origin、无令牌、未知字段、无 output、跨项目引用、第41次执行都必须在业务副作用前拒绝；tools/list 精确验证本节下方矩阵，不允许原生文件工具漏出。
- [x] 首先用锁定 opencode 录制一次包含并行工具、question、工具事件重发的本地测试轨迹，验证工具名称、MCP request ID 与 opencode tool call ID 的关联。UI 的事件重播不增加调用次数。
- [x] MVP 采用全应用单一活跃 agent 轮次，其余消息排队。当前轮次与 MCP 连接 epoch 绑定；结束时等待 opencode idle、排空工具并关闭旧 MCP transport 后再进入下一轮。用 `ponytail:` 注释说明单轮并发上限，升级路径为独立会话运行时与连接隔离，不接受模型提交的任意 session_id。
- [x] `agent_tool_calls` 唯一键 `(turn_id,call_id)`，计数与准入同事务，第41次拒绝并触发 opencode abort。对没有可验证关联的 MCP 传输请求按执行请求保守计数，禁止仅靠延迟 SSE 事后计数；将锁定版本的关联方式和重放边界写入协议适配记录。原生 question 也经预算检查后才显示，超限问题直接 reject，不生成授权回执。
- [x] `design_execute` 调 Submit 后 Wait：穿过 billing，普通 waiting_user/unknown/终态返回完整任务视图；调用上下文断开只结束等待，不取消任务。`design_task_wait` 复用同一谓词。工具错误使用结构化业务结果，不把 unknown 转成可自动重试的网络错误。
- [x] `go -C gateway test ./internal/mcp ./internal/agent -v`，提交 `feat(M5.2): expose scoped design tools with execution limits`。

预算测试最小断言（测试文件包为 agent；Admit 返回 accepted/error，已存在 callID 返回原准入结果）：

```go
func TestFortyCalls(t *testing.T) {
    b := NewBudget(40)
    for i := 0; i < 40; i++ {
        id := fmt.Sprintf("call-%d", i)
        if err := b.Admit(id); err != nil { t.Fatal(err) }
        if err := b.Admit(id); err != nil { t.Fatal("event replay counted twice") }
    }
    if err := b.Admit("call-40"); err == nil { t.Fatal("41st call admitted") }
}
```

### Task M5.3：持久聊天桥与 question 适配

**Files:**
- Create: `gateway/internal/agent/client.go`, `gateway/internal/agent/chat.go`, `gateway/internal/agent/events.go`, `gateway/internal/agent/questions.go`, `gateway/internal/agent/chat_test.go`, `gateway/internal/server/chat.go`。
- Modify: `gateway/internal/questions/service.go`, `gateway/internal/events/service.go`, `gateway/internal/server/server.go`。

**Interfaces:** 新增 GET/POST `/api/chat/sessions`、POST `/api/chat/sessions/:id/cancel`；消息 POST 按用户 §8 返回202，内容通过全局 SSE。消息包含 requestId、TipTap文档、文本、references、annotations。输出统一 `ChatMessageEnvelope{session_id,step,content_delta|tool_call|...}` 与 `ChatDonePayload{done|error}`；原始 opencode event 只在适配层存在。

- [x] httptest opencode 模拟：立即拒绝、9秒受理、10秒仍未答复后最终成功、10秒后报错、全局事件断线、重复delta、cancel、41次熔断。用可注入短时钟测试宽限窗，不让每个用例真实等10秒。
- [x] POST先持久化 turn/message/requestId；后台请求生命周期独立于前端请求ctx。10秒只结束等待并回202，不能取消 opencode 请求。重放同一 requestId 不再POST；受理不明保持关联记录并查询事件/消息，不自动产生第二条用户消息。
- [x] SSE以锁定 `/doc` 的真实事件字段映射，记录上游 session/message/part/call ID，去重后持久化本地 events；只有上游对应轮次完成/失败或显式取消才发一次终态。工具运行、question等待不等于chatdone；断流先恢复事件与会话状态，不凭断流伪造done。
- [x] 原生 question 映射本地持久卡，用户回执转为 `POST /question/{id}/reply` 的 `{answers:string[][]}`；拒绝走原生 reject。保存本地questionID与上游ID映射及回执发送状态，回执未知时查询上游，不无限重发。agent 的提问是请求授权，不是授权本身。
- [x] cancel通过显式 session abort，队列未发送消息可撤销；不会隐式取消已经独立受理的生成任务，UI提供任务取消入口。新增路径、ChatDone字段形状及事件名先对照逆向文档固定到 docs/contracts。
- [x] `go -C gateway test ./internal/agent ./internal/questions ./internal/events -v`；提交 `feat(M5.3): bridge chat acceptance events and human questions`。

### Task M5.4：TipTap、资产引用、消息队列与流式 UI

**Files:**
- Create: `desktop/src/features/chat/ChatPanel.tsx`, `desktop/src/features/chat/AssetMention.ts`, `desktop/src/features/chat/AssistantMessage.tsx`, `desktop/src/features/chat/messages.ts`, `desktop/src/features/chat/messages.test.ts`, `scripts/smoke-chat.mjs`。
- Modify: `desktop/package.json`, `desktop/pnpm-lock.yaml`, `desktop/src/App.tsx`, `desktop/src/events.ts`, `desktop/src/features/editor/ImageEditor.tsx`。

**Interfaces:** mention attrs.path严格为 `asset:<id>`，遍历整个 TipTap JSON收集唯一有序引用；发送包括完整doc及标注，不从渲染HTML反推。消息队列状态 queued/sending/accepted/done/error/cancelled 是UI发送状态，不能覆盖生成任务状态机。

- [ ] 用Node内置测试器验证嵌套引用、重复引用、恶意HTML、重放delta、不同session混合事件、终态后迟到事件、排队消息编辑生成新requestId；发送中unknown重试仍用原内容和requestId。
- [ ] 最小遍历函数落 `messages.ts`；其输入不依赖编辑器实例，能直接测试：

```ts
type DocNode = { type?: string; attrs?: Record<string, unknown>; content?: DocNode[] };
export function assetReferences(root: DocNode): string[] {
  const result = new Set<string>();
  function visit(node: DocNode) {
    const path = node.attrs?.path;
    if (node.type === "mention" && typeof path === "string" && path.startsWith("asset:") && path.length > 6)
      result.add(path.slice(6));
    for (const child of node.content ?? []) visit(child);
  }
  visit(root);
  return [...result];
}
```

- [ ] gateway再次验证每个ID及项目归属，UI不承担安全校验。TipTap自定义mention提供键盘选择、删除和资产缩略图。标注配对数据随消息发送，发送后的不可变消息与当前编辑草稿分开。
- [ ] 单一SSE事件reducer按session/step与事件ID归并；工具卡/文本分别显示，禁止重复append。排队可编辑/撤回，处于发送中不复用修改后的body。确认卡复用M4组件；IME组合态不触发Enter发送或Delete编辑器快捷键。
- [ ] `pnpm --dir desktop build`、`node --experimental-strip-types --test desktop/src/features/chat/messages.test.ts`；WebView验证忙时排队、引用、流式输出、用户取消、键盘确认，提交 `feat(M5.4): compose referenced chat and render streamed turns`。

### Task M5.5：工具矩阵补齐、系统提示词与 M5 验收

**Files:**
- Create: `agent/media-agent.md`, `gateway/internal/mcp/documents.go`, `gateway/internal/mcp/catalog.go`, `gateway/internal/mcp/tools_test.go`, `gateway/internal/workspace/media.go`, `gateway/internal/workspace/media_test.go`, `scripts/build-media-tools.ps1`。
- Create: `gateway/internal/agent/text.go`, `gateway/internal/agent/text_test.go`。
- Modify: `gateway/internal/mcp/tools.go`, `gateway/internal/agent/config.go`, `scripts/check.ps1`, `docs/third-party.md`, `docs/contracts.md`, `docs/milestones.md`。

| 工具 | 唯一业务实现 / 验收 |
|---|---|
| design_capability_list / get | providers能力目录；分页、schema、约束、知识卡 |
| design_execute | tasks.Submit+Wait；计费穿透、未知结束、声明output |
| design_deliver | delivery；unknown恢复不生成第二version |
| design_task_query / wait / resolve | tasks；CAS、拒绝agent伪造人类批准 |
| design_read | workspace；只读受管文档，限大小/类型 |
| design_analyse_media | 客观尺寸/时长/音轨；不做生成质量评审 |
| design_composition_search / get / edit | store；项目隔离、对象身份、分页 |
| design_resource_get / edit | store；修改内容创建Version，不能覆盖历史 |
| design_document_write | 暂存文档→delivery；同内容修订沿用Resource |
| design_project_rules | 持久项目规则；写入需显式用户要求的结构化回执 |
| design_voice_search / image_search | 真实本地素材索引或已配置供应商目录；返回真实ID与来源 |

- [ ] 每行至少一个工具级成功和拒绝用例；对生成/投递再覆盖重放。MCP与HTTP同一对象/任务产生同一结果，不允许工具内另写SQL事务或另一份状态转换。
- [ ] 使用Go图像标准库解码尺寸；视频/音频ffprobe通过明确argv、受管路径、超时启动，不走shell。构建脚本锁定分发版本及许可证，不能调用原应用私有二进制。文档读写拒绝任意路径；知识卡只读受管参考资料。
- [ ] 补齐`POST /api/generate/text`：复用tasks的202受理、冻结输入、计费卡、事件和delivery，将所配LLM通过受管opencode独立文本session产生的最终文本保存为文档Version。该session的专用配置禁用全部工具，包括question/MCP；不占用media-agent轮次队列，否则design_execute(text)同步等待会死锁。最多一个文本任务执行，其余文本任务排队，MCP作用域仍仅绑定media-agent；由main注入text执行函数避免tasks与agent互相import。测试嵌套execute(text)完成且工具调用数0，以及401、宽限窗后受理、空结果、取消、unknown不重发、文档落库；模型未提供精确计费时按配置token上限报价并明确估算。不能只注册路由返回not_implemented。
- [ ] 素材搜索初版覆盖本地图片资产和用户登记的真实供应商voice目录；没有目录返回明确空coverage与来源说明。voice目录只存供应商voice ID和描述，不引入第三个语音生成模型。需要远端搜索时只能使用已实现、具备文档与fixture的协议；不能把虚构数据或通用生成API当搜索。该本地优先范围作为相对原云搜索的适配显式登记。
- [ ] 以analysis-docs/02 §5逐条迁移中文提示词，至少保留：稳定对象身份、独立内容新Resource、同内容修订新Version、派生不等于同Resource、共同出现不合并、成员role不替代身份、跨对象复用不复制身份、源版本血缘、用户授权范围、文档与媒体分阶段、未知结果原请求重放、项目规则仅用户要求。逐条注明对应原文位置。
- [ ] 提示词要求采用选定技能直到任务完成、按可审阅成果推进、只暂停依赖缺口、真实asset引用、不得验收式自评/自动重生。规则持久化只有human回执允许；agent请求更新只能提出候选卡。
- [ ] `./scripts/check.ps1`及smoke-chat验证真实LLM→工具→billing卡→图像→版本→画布；无LLM凭据标未执行，不能通过模拟对话宣布M5完成。提交 `feat(M5.5): complete design tools and media agent contracts`，推送M5。
