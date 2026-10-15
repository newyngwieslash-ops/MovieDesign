# Codex 提示词：用 Tauri + Go + opencode 复现 MiniMax Design

> 用法：把本文档整份作为初始提示词喂给 Codex（或任何 coding agent）。文档本身就是完整的工程规格——基于对 MiniMax Design v4.0.0 的八篇逆向分析文档（见 `analysis-docs/`）浓缩而成。
> 使用前替换：`[PROJECT_ROOT]`（你的工作目录）、`[MODEL_API_KEY]`（你要接入的第一家模型 API）。

---

## 提示词正文（从这里开始复制）

你要从零实现一个名为 **OpenDesign** 的桌面 AI 设计应用——它是 MiniMax Design 的开源复现，但技术栈换为 Tauri 2 + Go + opencode。本规格基于对该产品的完整逆向分析，所有协议、数据模型、状态机都是实证规格，请**严格遵守契约，不要自创协议**。

## 0. 参考资料（必读）

工作目录 `[PROJECT_ROOT]` 下的 `analysis-docs/` 是原始产品的逆向分析文档（8 篇），遇到设计疑问时按此顺序查阅：
- `01-架构与技术栈.md` —— 四层架构与 IPC 面
- `02-系统实现篇-gateway与agent.md` —— gateway API/agent 配置/对象模型/技能体系
- `04-画布编辑器专题.md` —— 编辑器实现规格（可直接翻译成代码）
- `05-剧本到视频流水线专题.md` —— 分镜流水线/门禁/任务状态机
- `06/07` —— 复现路线图与协议补遗
**重要**：参考文档描述的是原产品（Electron/TS 栈）。你复现时遵循其**协议语义与数据模型**，但按本提示词的技术栈落地。

## 1. 产品定义

一个本地优先的 AI 设计工作台，核心能力：
1. **画布**：图像标注编辑 + 无限创作画布（媒体节点、生成结果、文档节点）
2. **AI 对话**：一个 media-agent 与用户协作创作（写剧本→做分镜→调模型生成视频/图像/音乐）
3. **生成流水线**：剧本→分镜→Prompt 编译→模型调用→产物入库→画布呈现，全程有人类确认门禁
4. **技能系统**：markdown 编写的方法论技能，agent 按需加载
5. **本地对象模型**：所有创作内容进入 SQLite，带版本血缘

非目标（明确砍掉）：内嵌浏览器、插件系统、技能市场、团队协作、热更新。MVP 只接 **1 家视频模型 + 1 家图像模型**（走官方公开 API）。

## 2. 技术栈（强制）

| 层 | 选型 | 说明 |
| --- | --- | --- |
| 桌面壳 | **Tauri 2**（Rust） | 单窗口为主：主窗口 + 登录窗口 |
| 前端 | **React 19 + TypeScript + Vite** | TanStack Router/Query、TipTap（聊天富文本）、Tailwind |
| 本地服务 | **Go 1.24**（单 binary） | 相当于原产品的 gateway：HTTP API + SQLite + SSE + 子进程管理 |
| Agent 宿主 | **opencode**（开源，作为 sidecar 二进制） | 自带 MCP server 能力；我们给它注入自定义 system prompt + 自定义 MCP 工具 |
| 数据库 | **SQLite**（Go 侧用 modernc.org/sqlite 纯 Go 驱动，免 CGO） | 对象模型 + 任务表 |
| MCP 工具 | **Go 内嵌 MCP server**（modelcontextprotocol/go-sdk） | 向 opencode 暴露 design_* 工具 |
| 更新 | tauri-plugin-updater | 参考：更新策略协议见 §9 |

进程拓扑（启动顺序）：
```
Tauri main (Rust)
  ├─ spawn → open-design-gateway.exe (Go, 监听 127.0.0.1:8001)
  ├─ spawn → opencode.exe (server 模式, MCP 端口 :4096)
  │           └─ 连接我们的 design MCP server（gateway 内嵌）
  └─ 主窗口加载前端 → 前端只与 gateway 通信（REST + SSE）
```
Rust 壳与 Go gateway 之间用 stdin/stdout JSON-RPC 传三个信息：gateway 实际端口、健康状态、关闭信号。**前端永远不直接访问 opencode**，一律走 gateway。

## 3. 数据模型（SQLite，照此建表，不许多改）

核心三表 + 支撑表（参考原产品的 ADR 式注释风格，迁移文件用 golang-migrate 编号管理）：

```sql
-- 001_assets.sql：资产库（复用原产品的三层指纹设计）
CREATE TABLE assets (
  id TEXT PRIMARY KEY,             -- UUID
  path TEXT NOT NULL UNIQUE,       -- 工作区相对路径
  status TEXT NOT NULL DEFAULT 'active',  -- active|missing
  size INTEGER NOT NULL, mtime_ms INTEGER NOT NULL,
  dev_id INTEGER, inode INTEGER,   -- 文件系统指纹（重命名/移动跟踪）
  quick_hash TEXT,                 -- xxh3 前 4MB
  full_hash TEXT,                  -- xxh3 全文件，惰性计算
  width INTEGER, height INTEGER, duration_ms INTEGER, mime_type TEXT,
  name TEXT NOT NULL, metadata TEXT,  -- JSON: prompt/seed 等生成参数
  created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
);

-- 002_projects.sql：项目语义模型（这是系统的灵魂）
CREATE TABLE compositions (
  id TEXT PRIMARY KEY,             -- 创作对象（如"宣传片"），有稳定身份
  project_id TEXT NOT NULL,
  type_id TEXT NOT NULL,           -- 分类（如"视频"），label 项目内唯一
  name TEXT NOT NULL,
  attributes TEXT                  -- 对象自身设定（不随版本变）
);
CREATE TABLE resources (
  id TEXT PRIMARY KEY,             -- 一份创作内容的持续身份
  composition_id TEXT NOT NULL,
  description TEXT
);
CREATE TABLE versions (
  id TEXT PRIMARY KEY,
  resource_id TEXT NOT NULL,
  asset_id TEXT REFERENCES assets(id),  -- 指向具体文件
  source_version_id TEXT,               -- 血缘：从哪个版本派生
  origin TEXT NOT NULL DEFAULT 'agent', -- agent|user
  created_at INTEGER NOT NULL
);
CREATE TABLE composition_members (      -- 成员角色化放置
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  composition_id TEXT NOT NULL,
  resource_id TEXT NOT NULL,
  role TEXT NOT NULL                     -- 如"主视频"、"封面"；用户可读文字
);
```

**语义规则（写进代码注释与 agent 提示词）**：
- Composition = 有独立内容范围和身份的创作对象，是决策/修改/复用的单位
- 同一内容的修订 = 同一 Resource 下新 Version；独立内容 = 新 Resource
- "共同出现"不合并对象身份；成员 role 说明用途，不替代对象身份

## 4. Gateway API（Go 实现，最小完备集）

MVP 端点集（前缀 `/api/`，JSON，环回监听）。列出的是**契约**——路径、语义、状态码含义照抄原产品：

```
健康/会话   GET  /api/health              → {status, idle, safe_to_suspend}
文件       POST /api/upload              → multipart，返回 {path}
           POST /api/upload/commit      → 两阶段提交 {path}→{ok, relative}
           GET/POST /api/files/{list,content,delete,mkdir,rename,copy}
生成       POST /api/generate/{image,video,text}
             body: {capability_id, prompt, params, references[], output}
             → 202 {task_id}   (异步，事件走 SSE)
任务       GET  /api/tasks/:id          → 任务视图（见 §6 状态机）
           POST /api/tasks/:id/resolve  → {action:"retry"|"cancel", expected_revision}
对象模型   GET/POST /api/compositions /api/resources /api/versions
           POST /api/deliver            → 幂等投递（见 §7）
聊天       POST /api/chat/sessions/:id/messages   → SSE 流（见 §8）
画布       GET/POST /api/canvas/{nodes,commands,content}
事件       GET  /api/events             → 全局 SSE（任务进度、聊天帧）
确认       POST /api/question           → agent 向用户提问（结构化确认卡）
```

**通用规则**：
- 所有写操作带 `requestId`（幂等键）；unknown 结果用同 requestId 重试，**绝不重新生成**
- 错误格式统一 `{error:{code, message, details?}}`；code 枚举：`invalid_input / not_found / waiting_user / capacity / upstream`
- SSE 心跳 15s；断线重连靠 `Last-Event-Id`
- 分页用 `offset/limit`，响应带 `coverage:{total, next_offset, exhausted}`

## 5. MCP 工具面（Go MCP server，向 opencode 暴露）

工具名与语义**照抄原产品**（它是验证过的最小完备集）：

| 工具 | 语义 |
| --- | --- |
| `design_capability_list` / `design_capability_get` | 列出/详查模型能力（id 格式 `<operation>:<name>` 如 `video:h3`；get 返回 paramSchema + 约束 + 知识卡路径） |
| `design_execute` | 执行生成。必填 `capabilityId + output`（声明产物进哪个 Composition、什么 role）；可带 prompt(≤64KB)/params(平铺，能力声明过的)/references(有序，传 asset id)/count(变体数)。**同步等待**：阻塞到任务终态或 waiting_user |
| `design_deliver` | 把已有文件/任务产物落库（Resource+Version+成员放置）。幂等 |
| `design_task_query` / `design_task_wait` / `design_task_resolve` | 任务查询/续等/恢复决策（retry 用，带 expected_revision 乐观锁） |
| `design_read` / `design_analyse_media` | 读文件（文档类）/媒体分析（时长分辨率等客观参数） |
| `design_composition_{search,get,edit}` / `design_resource_{get,edit}` / `design_document_write` | 对象模型操作 |
| `design_project_rules` | 项目规则读写（用户明确要求时才持久化） |
| `design_voice_search` / `design_image_search` | 素材搜索 |

**参数纪律**（写进工具 schema 描述）：params 只放能力声明过的平铺字段；媒体内容只走 references（传 id 不传路径）；agent 不得直接写文件系统——一切落库走 deliver。

## 6. 任务状态机（Go 实现，严格按此）

```
状态: accepted → running(内部 phase: pending|queued|active|paused|retrying)
      → waiting_user | completed | failed | cancelled | unknown
终态: completed | failed | cancelled
```

- `waiting_user` 两种：`problem.source=="billing"`（等用户确认扣费，execute 的等待**穿过**它继续）与非 billing（等用户决策，agent 等待**结束**）
- 任务视图字段：`{id, capability_id, status, revision, prompt, outputs[], problem?, actions[], progress:{phase,percent}, billing:{estimated_credits}}`
- `problem` 含 `upstream`（模型侧原始错误）；`actions` 是可用恢复动作列表
- **禁止自动重试**：重试必须用户发起 design_task_resolve(action=retry)
- 计费确认：estimate 接口算积分 → 前端弹结构化确认卡 → 确认后任务才进 active

## 7. 投递协议（幂等，三态语义照抄）

```
POST /api/deliver {requestId, sources:[{key, path|taskOutputKey}], 
                   output:{resources:[{key, mode:"create"|"new_version", name, baseVersionId?}],
                           composition:{name, type, placements:[{key, role}]}}}
→ {delivery:{status: completed|unknown|blocked, versions[]}}
completed = 终态；unknown = 用同 requestId+原输入重试（不重新生成）；blocked = 修正后换新 requestId
```

## 8. 聊天桥（gateway ↔ opencode）

- gateway 把用户消息 POST 给 opencode 的 session API，**10 秒宽限窗**：超时未报错即视为已受理，返回 202，后续全靠 SSE 回流
- agent 输出逐帧 `ChatMessageEnvelope{session_id, step, content_delta|tool_call|...}` 广播到 `/api/events`，前端按 session_id 过滤
- 终态帧 `ChatDonePayload{done|error}`；用户取消走显式 cancel
- **防循环熔断**：单轮工具调用上限 40 次，触发即停并上报

## 9. 前端关键组件

1. **画布编辑器**（Canvas 2D，**照 `04-画布编辑器专题.md` 直接翻译**，3,000 行规格）：
   - 8 种图形（矩形/椭圆/线/箭头/笔刷/文字/标注气泡/马赛克），Shape 基类契约：`draw/hitTest/getBounds/move/resize`
   - 命中下限 `max(8, strokeWidth)`；矩形无填充环形命中；笔刷二次贝塞尔平滑；马赛克=裁剪处理图+LRU(8) 缓存
   - 交互事务 `beginInteraction/commitInteraction/cancelInteraction`：一次手势一个快照，undo 粒度=手势
   - 四层渲染（bg/shape/preview/main），rAF 合帧，preview 层拖拽重绘 O(1)
   - 文字输入用透明 textarea 叠加（canvas 实时渲染最终效果），Escape 丢弃、blur 提交、空文本删除
   - HistoryManager 双栈快照式，容量 50
2. **聊天面板**：TipTap 文档；自定义 mention 节点 `attrs.path="asset:<id>"`；发送前收集文档里全部 asset 引用随消息提交；消息队列（忙时排队）；流式渲染 AssistantMessage
3. **标注工作流**（编辑器上层的复用组合）：用户画矩形→配 comment→成 annotation 随消息发送；删除时矩形+气泡成对删；焦点在输入框时跳过编辑器快捷键；Delete 键先判 isComposing
4. **任务中心**：进度 phase+percent、失败原因、重试按钮（调 design_task_resolve）
5. **确认卡组件**：全局唯一授权形式——所有需要用户决策的点（计费/方案确认/分镜批准）都走它，**禁止**用聊天文本"回复 1/2/3"代替

## 10. Agent 系统提示词（media-agent，中文，作为 opencode 的 system prompt 注入）

以 `analysis-docs/02-系统实现篇` §5 的原文为蓝本改写（那份提示词是 3,500 字的领域模型判例），必须保留的结构：
1. **身份**：与用户共同创作的专业伙伴，承担构思到交付的责任
2. **项目语义**：Composition/Resource/Version 判例（"派生关系不意味着同一 Resource"等 10+ 条边界规则，照抄原文语义）
3. **协作节奏**：区分"用户已确认/授权内选择/待讨论"；按可审阅成果分步推进，每步请用户确认；无法推断的实质缺口（画幅/时长）只暂停依赖项并给推荐
4. **纪律**：不对自己生成的产物做验收式自查；质量以用户反馈为准；引用真实 asset id 不重新描述；question 工具用于方案选择；project_rules 仅用户明确要求时更新
5. **技能消费**：选用技能即采用其工作方法至任务完成
工具权限：只暴露 §5 的 design_* 工具；opencode 原生 bash/read/write/edit/todowrite **全部禁用**。

## 11. 技能系统

- 技能 = `{SKILL.md(frontmatter: name/description/trigger-words + 正文方法论), meta.yaml, references/*.md}`，存 `<dataDir>/skills/<name>/`
- gateway 提供 `/api/skills`（列出+内容）；agent 侧 design 技能以 prompt 前缀形式注入（opencode 的 agent 配置支持 skill 目录）
- **先写 2 个技能验证流水线**（结构模仿 `05` 篇读到的 anime-game-pv，简化版）：
  1. `口播视频`：意图确认→脚本确认→分镜文档→生成
  2. `图片标注修复`：接收标注→理解区域意图→execute 图像编辑
- 每个技能必须有的固定结构：**适用范围（含"不用于…"负向边界）、确认门禁序列、失败回流规则**（上游变更使下游批准失效；分镜语义变则重新过门禁，纯参数变只更新合同）

## 12. 分镜流水线（MVP 完整实现，这是产品灵魂）

实现 `05` 篇五层流水线的最小闭环：

1. **语义分镜**：agent 产出 `storyboard_review.md` 文档节点（画布上）——Panel 列表（时间点/景别/主体动作/转场依据）→ 聚合 Logical Shot（时间范围/叙事职责/起止状态）→ Master Timeline
2. **revision/Approval**：分镜文档带 `storyboard_revision` 号；用户批准 = 结构化确认卡回执绑定当前 revision；任何 Panel/Shot/Timeline 变更 → 新 revision → 旧批准失效 → `prompt_status=pending`
3. **Prompt 编译 + Preflight**：从获批分镜编译唯一 prompt 文档（`final_video_prompt.md`，与将提交的字符串字符级一致）；Preflight 检查清单（能力参数白名单/引用资产真实/时长画幅/未解析占位符）全过才允许 execute；**展示与生成必须用同一字符串，禁止摘要转写**
4. **失败回流**：生成失败 → 按问题回流（参数问题回 Prompt 编译；语义问题回分镜门禁）；**默认整条重生成，不做局部拼接**
5. 全程产物落对象模型（生成结果 = Resource 新 Version，血缘指向源分镜文档）

## 13. 工程纪律

- **仓库结构**：`desktop/`（Tauri+React）`gateway/`（Go，cmd/internal/migrations）`agent/`（提示词+技能+opencode 配置）`docs/`（本规格+协议文档）
- Go gateway：所有 handler 用 zod 风格请求校验（go-playground/validator + 显式 schema）；错误一律结构化；表驱动测试覆盖任务状态机与投递幂等
- 前端：editor 独立包（无 React 依赖的核心 + React 封装层），可单测
- 安全基线：gateway 只绑 127.0.0.1；**每请求校验 Origin 头**（防浏览器 CSRF 打本地端口——原产品的已知缺口，必须修）；能力参数白名单校验放 gateway 而非前端
- 可观测：结构化日志（slog）落 `<dataDir>/logs/`；任务/生成事件写 SQLite `events` 表（埋点命名参考：`chat_message_send / generate_task_submit / generate_task_failed / app_update_check`）

## 14. 交付里程碑（按此顺序提交，每步可运行可验证）

1. **M1 骨架**：Tauri 壳拉起 Go gateway（健康端点互通）+ SQLite 迁移 + 空画布窗口
2. **M2 对象模型**：assets/compositions/resources/versions CRUD + `/api/deliver` 幂等（含 unknown 重试路径）+ 测试
3. **M3 编辑器**：Canvas 编辑器全功能（8 图形/选择/历史/文字叠加）+ 标注工作流 + 单测
4. **M4 生成链**：接 1 家图像模型（能力协议/execute/任务状态机/计费确认卡/SSE 进度）——先图像后视频
5. **M5 agent**：opencode sidecar + design_* MCP 工具 + 系统提示词 + 确认卡 + 聊天 UI（TipTap+mention）
6. **M6 流水线**：技能×2 + 分镜文档/revision/Approval + Prompt 编译 Preflight + 失败回流——端到端跑通"一句话→确认→生成→落库→画布"
7. **M7 收尾**：视频模型接入 + 任务中心 UI + 更新插件（带云控停更开关：`manual_only{平台,版本区间,reason}` + 24h 缓存 + 过期兜底）

每个里程碑完成后：跑全量测试 → 自查对照本规格逐条确认 → 提交信息注明里程碑编号与规格偏差（如有）。

## 15. 验收标准

- 端到端：新建项目→对话"用这张图做个 15 秒口播视频配轻快音乐"→agent 走完技能流程（确认卡×N）→视频生成→产物出现在画布且可继续编辑（新版本带血缘）
- 打断演练：生成中取消 / 生成失败后重试 / 分镜批准后改分镜（必须重新过门禁）——三者状态转换正确
- 幂等：同 requestId 投递两次不产生两个版本
- 安全：从浏览器直接 curl gateway 写操作被 Origin 校验拒绝
- `sqlite3 .dump` 可读出完整血缘链：分镜文档 → prompt 文档 → task → version → asset

**开始前先输出**：仓库骨架设计 + M1 的实现计划（文件清单+每个文件职责），等我确认后再写代码。

---

## 附：为什么这些契约不能改（给 Codex 的最后说明）

这些设计不是风格偏好，是原产品用工程量换来的实证结论：
- **output 先声明后生成**（§5）：防止生成产物成为孤儿文件
- **幂等投递三态**（§7）：unknown 是分布式生成的真实中间态，"重试=重放"而非"重来"
- **waiting_user 双语义**（§6）：计费确认与方案确认的等待行为不同，混用会导致计费误扣或卡死
- **字符级 Prompt 锁定**（§12）：生成失败重试的前提是"原样重放"，任何转写都会让失败不可复现
- **整条重生成**（§12）：拼接独立生成的片段必然穿帮，可编辑性留在语义层
- **确认卡唯一授权**（§9/§10）：LLM 会把客气话当授权，必须用结构化通道隔离
- **Origin 校验**（§13）：原产品缺这个，是它的真实安全缺口，不要继承

（提示词结束。参考资料在 `analysis-docs/`，遇到未覆盖的细节先查文档，再按原产品语义推断，最后才允许自行设计并在提交信息中标注。）
