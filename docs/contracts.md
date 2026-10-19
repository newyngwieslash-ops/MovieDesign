# MovieDesign 契约

## M6.1 技能方法与冻结采用（已实现接口补记）

`GET /api/skills?offset=0&limit=50` 返回 `{items:[{name,description,"trigger-words",version,revision,bundle_revision,modified}],coverage:{total,next_offset,exhausted}}`；默认 limit=50，范围 1–200，严格拒绝未知/重复/非规范/负分页参数。`GET /api/skills?name=<name>` 与分页互斥，返回同样元信息及完整 `body`、`references`（受管相对名→完整正文），未知名称 404，畸形本地技能 400，保持 Origin 与只读 GET 预检。

Chat send 可选 `skill_id` 字符串：显式名称采用当前本地完整方法；空字符串持久停用；省略继承服务拥有的已采用快照（尚无采用时才按已实现唯一触发词选择）。null/client-forged skill 快照拒绝。服务在 chat.send 的 `write_requests.response_json` 与消息/队列同事务保存 SkillInfo+Body+全部 References；原 Message.content 保留原 wire。历史 `Message.skill` 是该服务快照，入队/继续/重启都注入冻结内容为原生 prompt 首个文本部分，不再读实时文件。方法不是批准证明，不增加 design 18+question 权限，text agent 仍零工具。

## M6.2 语义分镜 typed document（已实现的移植适配）

来源概念为 spec §10 / analysis-docs 05：Panel 是语义时间点，Logical Shot 是连续叙事章节，Master Timeline 是单次完整渲染计划；**下面的机器字段是 MovieDesign 新增 typed port，并非原产品公开 wire**。不固定 Panel/Shot 数量、均匀分布、独占分区、网格、厂商字数预算。Shot 重叠可表达过渡，不是独立生成片段。

既有 `design_document_write`（工具总数仍为 18 + native question）和新增 `POST /api/documents/write?project_id=...` 共用 `delivery.Service.WriteDocument(ctx,projectID,raw)`，保留完整原始 JSON 幂等身份。HTTP actor=user，MCP actor=agent，由可信服务选择。以下为 DocumentRequest 新增的可选 `typedDocument` 的完整 JSON Schema；未提供它的通用 md/txt/srt/ass create/replace/patch 行为保持原入口。

```json
{
  "$schema":"https://json-schema.org/draft/2020-12/schema",
  "type":"object","additionalProperties":false,
  "required":["kind","pipelineId","expectedRevision","storyboard"],
  "properties":{
    "kind":{"const":"storyboard"},
    "pipelineId":{"type":"string","pattern":"^[A-Za-z0-9_-]{1,128}$"},
    "expectedRevision":{"type":"integer","minimum":0},
    "storyboard":{"$ref":"#/$defs/storyboard"}
  },
  "$defs":{
    "id":{"type":"string","minLength":1,"maxLength":128},
    "text":{"type":"string","minLength":1},
    "ms":{"type":"integer","minimum":0,"maximum":9007199254740991},
    "panel":{"type":"object","additionalProperties":false,"required":["id","timeMs","framing","action","transition"],"properties":{"id":{"$ref":"#/$defs/id"},"timeMs":{"$ref":"#/$defs/ms"},"framing":{"$ref":"#/$defs/text"},"action":{"$ref":"#/$defs/text"},"transition":{"$ref":"#/$defs/text"}}},
    "shot":{"type":"object","additionalProperties":false,"required":["id","startMs","endMs","panelIds","narrativeRole","startState","endState"],"properties":{"id":{"$ref":"#/$defs/id"},"startMs":{"$ref":"#/$defs/ms"},"endMs":{"$ref":"#/$defs/ms"},"panelIds":{"type":"array","minItems":1,"uniqueItems":true,"items":{"$ref":"#/$defs/id"}},"narrativeRole":{"$ref":"#/$defs/text"},"startState":{"$ref":"#/$defs/text"},"endState":{"$ref":"#/$defs/text"}}},
    "timeline":{"type":"object","additionalProperties":false,"required":["durationMs","shotIds","audioIntent"],"properties":{"durationMs":{"type":"integer","minimum":1,"maximum":9007199254740991},"shotIds":{"type":"array","minItems":1,"uniqueItems":true,"items":{"$ref":"#/$defs/id"}},"audioIntent":{"$ref":"#/$defs/text"}}},
    "storyboard":{"type":"object","additionalProperties":false,"required":["panels","shots","timeline"],"properties":{"panels":{"type":"array","minItems":1,"items":{"$ref":"#/$defs/panel"}},"shots":{"type":"array","minItems":1,"items":{"$ref":"#/$defs/shot"}},"timeline":{"$ref":"#/$defs/timeline"}}}
  }
}
```

字段映射：framing=景别，action=主体动作，transition=转场依据；narrativeRole=叙事职责，startState/endState=起止状态；audioIntent=完整时间线音轨意图（允许明确“无”，不做音频能力或质量推断）。所有文本必须非空白，原文保留；所有时间为整数毫秒，不持久化 UI 时间字符串。Panel/Shot ID 在一份分镜中全局唯一，ID 上限 128 UTF-8 字节；每个 Panel 至少被一个 Shot 引用，每个 Shot 至少引用一个 Panel，引用时间点在该 Shot 闭区间内。Timeline 恰好引用全部 Shot 一次，Shot 满足 `0 <= startMs < endMs <= durationMs`，所有区间的并集须从 0 无缺口覆盖到 durationMs。允许不均匀时间点、共享 Panel、重叠 Shot，数组顺序也是候选语义的一部分。

typedDocument 只用于 format=md、mode=create/replace。首次 expectedRevision=0，资源 output.mode=create；后续 expectedRevision 为当前 revision，output.mode=new_version 且 baseVersionId 必须精确等于当前语义 revision 的 document_version_id。pipelineId 为整个本地数据库内的稳定标识（建议 UUID），首次绑定项目与 Resource；标题变化不创建新 Resource，也不改变项目归属。已有 output 类型/Composition/placement 校验继续执行。修改 Composition 应另走已有 Composition 编辑流程。

服务确定性渲染 `storyboard_review.md`：固定说明和一个带完整 typed JSON 的 fenced code block，文档是不可变 Version。可选 content 为手工 Markdown 候选，必须具有唯一完整 JSON 区块、通过同一结构校验且与 typedDocument.storyboard 相等；仅区块结构是语义真值，外围文字重新渲染，不从“已批准”等普通文字读取授权。未带 typedDocument 的文档即使名为 storyboard_review.md 也只是普通文档，不能创建分镜 revision 或批准。

语义指纹是按固定 Go 字段顺序编码、保留数组次序及原始文本的完整 Storyboard JSON 的 SHA-256。新意图需先通过 expectedRevision/baseVersion CAS；同语义内容不新增文件、Version 或 revision，冻结并重放原 Version 的真实交付回执。新 output key 可作为该回执的别名返回；同语义无动作不得借此改变 Composition 或新增 placement role（400），name 仍是投递描述，不是 Resource rename 命令。原 requestId 精确重放返回冻结结果，即使后面已有新 revision；同 requestId 不同原始输入是 409。新的过期 expectedRevision/baseVersion 是 409/invalid_input。

迁移 `000012_pipeline` 添加 pipelines、storyboard_revisions、approvals、prompt_contracts、version_dependencies，核心五表和旧迁移不变。`storyboard_revisions` 主键 `(pipeline_id,revision)`，存 content_json/semantic_hash/document_version_id/created_at，禁止 UPDATE/DELETE，并以 FK 保护真实 Version。pipelines.resource_id 唯一绑定原 Resource。支持表 approvals 必须引用现有 human question_receipts；M6.2 没有批准写入入口，UI 明示“未批准 · 人类批准功能待接入”。

原子边界：共享 document.write write_requests 首先冻结生成的内部 key、完整渲染内容、output、可信 actor、typedDocument；文件 Stage/归档完全在 SQL Tx 外。普通 Delivery 最终事务通过 `commitDocument` 查回该原请求计划，核对冻结 output/actor 和受管文件的既有 xxh3 full_hash，调用 `pipeline.SaveStoryboard(ctx,tx,scope,requestID,pipelineID,expectedRevision,storyboard,baseVersionID,resourceID,versionID)`。这个 supplied-Tx 形态是计划 SaveStoryboard 接口的必要集成适配：实际 Resource/Version、分镜 revision、prompt_status=pending、已有 approvals.valid=0、pending/ready prompt_contracts→invalidated、version_dependencies 和 Canvas.PlaceDelivery 一起提交。submitted 合同及已有 task/attempt 的冻结输入不更改；不在事务中调用 DB getter、WriteDocument、文件或网络。

恢复仍走原 Delivery journal，不新增独立交付管线。相同 document requestId 可补完尚未 Stage 的计划；已有 journal 的通用 Recover 会使用持久原计划与同一个提交钩子。四个 crash seam（prepare/archive/before_commit/after_commit）沿原 actor/对象身份恢复。两个已归档候选晚到的 CAS 冲突回滚全部新 SQL 对象，Delivery 回执永久 blocked，保留原 journal/归档证据；不会自动改 expectedRevision、偷偷重生成或成为永久 unknown。需要用户重新阅读后以新请求编辑；未成功引用的受管归档保留供恢复审计，本任务不做垃圾回收。

新增只读 `GET /api/pipeline/storyboard?project_id=...&versionId=...` 返回 `{pipelineId,revision,storyboard,semanticHash,documentVersionId,createdAt,currentRevision,promptStatus,approvalStatus:"unapproved"}`。查询通过 pipelines.project_id 与精确 document_version_id 连接；普通文档/跨项目版本返回 404，非法/重复/未知参数返回 400。新增 HTTP 写端返回既有 `{delivery:{status,versions,problem?}}` 三态回执；1 MiB 限制、Origin、严格大小写/未知字段/重复键/null/整数检查不变。预提交验证 400，幂等/CAS 409，超限 413，不存在 404，基础设施错误 502；晚到提交冲突通过 200 + delivery.status=blocked 表达既有 Delivery 语义。以上两个 HTTP 路径为本移植 UI 支撑扩展，不冒称原产品端点。

CanvasNode 根据选中 Version 读取真实 revision，展示 Panel→Shot→Timeline、revision/状态及上一版 panels/shots/timeline 变更分类。旧版只读；Markdown 编辑提交服务端校验后的新候选。编辑请求先保存本地 project/pipeline/version/revision/完整 body，复用现有 updatePending CAS，只能清除自己持有的记录；重复节点、未知结果、晚到响应、损坏/不可用存储不会覆盖新意图。重挂载/重启只读，用户显式重试原冻结请求。普通文档保留文本预览。交付复用原节点，保留布局。

M6.3 接续：通过 questions.Reply 的 supplied-Tx human receipt hook 绑定 revision/hash/门禁，不从 native question 或 Markdown 推断批准；ReadVersion 和 UI 的批准状态届时接真实门禁。M6.4 接续：Prompt 是单独 Resource，绑定其来源语义 revision 与版本依赖；把 audioIntent 中经人类确认的要求编译成可机读音频/口播/音乐合同能力，当前自由文本不等于机器能力验证。Preflight、真实 LLM 方法质量和视频生成仍未实现/未执行；本地合成 fixture 只证明真实 native MCP/WebView 传输与恢复。

## M3.2 媒体形状与内存缓存

继续复用 `ShapeData` 的八种原始平面字段和唯一工厂 `shapeFromData`。样式留在 `style`；Tag 保留 `x/y/anchorX/anchorY/width/height/text/maxWidth`，文字变体保留 `style.textVariant=plain|filled|outlined`。实例构造与 `toData` 深拷贝；处理底图、笔刷离屏画布和文字基线缓存只在内存，不进入形状 JSON 或后续历史。

笔刷与马赛克笔刷按原源码 50799、50956 使用相邻点中点和 `quadraticCurveTo`，形状可绘制单点。BrushTool 的 `points.length>1` 提交门槛属于后续工具实现，M3.2 不提前提供工具。马赛克矩形裁剪处理底图，笔刷复用离屏画布并以 `destination-in` 保留路径；resize 只缩放内部点间距，保留 brushSize。`MosaicCache` 的公开 `get/put/clear` 使用容量 8 的 LRU；`getProcessedSource(mode,strength)` 按 mode/strength 分键，`setSource(image,revision)` 在身份、revision、源尺寸变化时失效，`setSize(width,height,dpr)` 在视口尺寸/DPR 变化时失效。源图按逻辑尺寸处理；物理 DPR 缓冲和导出变换由后续 Renderer 管理。

原源码 52324–52361 的 blur 复制边/角，padding 为 `2*round(radius)`，在独立输出画布设置 filter 后重置；mosaic 先高质量平滑缩小，再关闭平滑放大。有限 blur padding 的边缘 alpha 不保证 255；原生 WebView 检查使用同一有限 padding 的独立实心参考，避免将理想无限边界误作原始算法。

文字使用原字号比例 padding（横向 max(4,0.45*fontSize)、纵向 max(2,0.18*fontSize)）、1.4 行高和 DOM 字体基线测量；outlined 保留原 0.5px 描边文字语义。resize 修改字号并保底 8。Tag 按字符（包括 CJK）换行，采用原二次曲线圆角、四侧指针及锚点修正；有画布尺寸的 draw helpers 会按原 `keepTagInCanvas` 优先反侧、再同侧、最后 margin=4 夹紧卡片。该算法只约束卡片；超大卡片无法完全塞入小画布，锚点修正也不额外约束在画布内，均沿用原源码 51336–51420。

两项明确的原源码适配遵循用户已确认的优先级：

- 原 Text 51540 和 Tag 51194 的命中 padding 为 4；这里按用户明确要求改为 `max(8,strokeWidth)`。原 Mosaic 笔刷命中仅 brushSize/2；这里保留较大实际半径，同时不低于 `max(8,strokeWidth)`。填充马赛克矩形及填充椭圆仍使用实际填充区域，不扩成描边命中。
- 原 Text.draw 的 filled 卡片可比 getBounds 的最小字号宽度窄（例如单个句点）。为满足“filled padding 与 bounds 一致”，filled 实际绘制宽度改为 `max(fontSize,measuredWidth)+2*padX`；原生像素回归先复现该缺口，再验证整个公布 bounds 被色块覆盖。其他文字和 Tag 绘制、布局及 resize 语义遵循原源码。

`scripts/smoke-desktop.ps1 -Mode debug -Smoke editor` 是开发用真实原生 WebView2 测试文档，需先启动现有 Vite dev server；它不代表 M3.4 生产编辑器 UI 已集成。沿用既有 CDP、隔离 data/profile、owned process 清理，输出实际像素、缓存、ctx 状态和 192×192 PNG 证据；测试全局仅存在于 test/render 文档，不注入生产入口。

## 已确认决定（2026-10-15，用户确认 Q1–Q4）

1. 用户规格明确列出的路径、字段和行为优先；原应用源码补齐未规定的语义。与原产品不同的协议标为移植适配，不声称逐字段复刻。
2. Rust↔Go 使用 JSON-RPC 2.0、UTF-8、每行一个 JSON 对象，stdout 仅用于协议；日志写文件及 stderr。
3. `question` 是 design 工具白名单的明确例外，桥接成结构化确认卡。bash/read/write/edit/todowrite 禁用，不新造 design_question。
4. M1 直接进入本地工作台。模型接入阶段配置官方服务凭据，不复用原产品登录服务，不在 M1 构建独立账号系统。

## M1 控制协议

请求含 `jsonrpc:"2.0"`、`id`、`method`，无参数。允许空对象/空数组参数；未知参数拒绝。数值或字符串 id 原样回传；合法通知不返回响应。批量请求不在本管道契约内，返回 Invalid Request。

| 方法 | 结果 | 条件 |
| --- | --- | --- |
| gateway.info | `{ "port": 8001 }` | 数据库迁移完成，HTTP 监听已建立 |
| gateway.health | `{ "status":"ok", "idle":true, "safe_to_suspend":true }` | M1 尚无生成任务，仅说明 gateway 就绪 |
| gateway.shutdown | `{ "ok":true }` | 回执后排空 HTTP 请求、关闭数据库并退出 |

示例：

```json
{"jsonrpc":"2.0","id":1,"method":"gateway.info"}
{"jsonrpc":"2.0","id":1,"result":{"port":8001}}
```

解析错误使用 JSON-RPC 标准错误码 -32700/-32600/-32601/-32602。帧上限 64 KiB。父进程 stdin 管道 EOF 同样触发正常关闭。Rust 握手超时为每请求 10 秒，退出等待最多 4 秒，之后只终止自己持有的 sidecar 子进程。此控制协议不扩展业务 API 的错误码枚举。

## HTTP 与存储

- 应用仅监听 IPv4 `127.0.0.1:8001`。冲突立即失败，不漂移，不接管已有服务。独立冒烟可通过 CLI `--port 0` 使用临时端口，桌面不使用该模式。
- `GET /api/health` 返回与控制协议一致的三字段健康对象。
- 每请求在路由之前校验 Origin。生产仅接受 `http://tauri.localhost`；开发仅接受 `http://localhost:1420`。两者不会同时开放。缺失、null、多 Origin、非匹配来源返回 HTTP 403。
- HTTP 错误格式 `{error:{code,message}}`，Origin 拒绝用 `invalid_input`；不存在路由为 404/not_found；不支持方法为 405/invalid_input。
- OPTIONS 只批准当前支持的方法和路由；不得因为启用 CORS 而跳过请求授权校验。
- M1 无业务写入 HTTP 路由；M2.2 已实现下述对象写入，统一执行 requestId 幂等。
- 五张业务表保持用户 SQL 的字段、约束和默认值。golang-migrate 自动维护 schema_migrations；SQLite 自行维护 AUTOINCREMENT 的 sqlite_sequence。
- 所有 SQLite 连接启用 foreign_keys 和 5 秒 busy_timeout。当前单连接处理本地数据库；关闭前完成正在执行的 HTTP 请求。
- 日志本地存储，不向原厂或其他第三方发送遥测。

## 后续已锁定的适配

- `/api/canvas/...`、`/api/question` 等路径遵循用户规格，而非原产品 `/api/v3/canvas`、`/api/question/request`。
- 聊天采用 §8：POST 受理响应（202）＋ `/api/events` SSE。10 秒宽限窗只停止等待受理响应，不取消 opencode 请求；后台失败仍需事件回传。
- opencode :4096 是 HTTP session/event 服务，不是 gateway 内嵌 design MCP server 的端口。
- `unknown` 不属于任务终态，但会结束 agent 的 execute/wait 等待；不得因此重新生成。依据 analysis-docs/05 §5.3。
- 不继承原配置 bash:true，不继承原技能对生成产物的自动验收/重生成行为。

## 后续实现前必须落实的缺口

2026-10-15 的完整实施拆分见[总计划](superpowers/plans/2026-10-15-moviedesign-complete.md)。以下仍需按里程碑落地，不代表已经实现。

- M2：项目与类型存储、投递幂等记录和 supporting tables；核心表不随意改变。
- M4/M7：官方图像/视频模型及计费估算来源。15 秒口播＋音乐必须由实际能力满足，不用示意结果冒充成功。
- M5：question 回执 HTTP 适配、SSE 回放边界、LLM 提供商配置；原生 question 回执为 answers 二维字符串数组，禁止把普通聊天文本当批准。
- M6：分镜→prompt→task→version→asset 完整血缘、revision 绑定批准、参数与语义变更区别；source_version_id 一列不能替代全部支撑关系。
- M7：自有更新策略地址、签名与 manual_only 的缓存兜底。M1 不接入原厂更新基础设施。

## 安全边界

Origin 校验针对浏览器 CSRF；curl 或本机程序可以伪造 Origin。M1 不宣称具有本机恶意进程隔离或令牌认证。窗口 CSP 限制 gateway 和开发 HMR，前端无 shell/文件系统执行权限。

## 完整计划阶段新增决定（2026-10-15）

用户已明确确认：

- BYOK＋费用确认；费用由所配供应商收取，不建设钱包/充值。积分仅为可配置换算的估算展示，同时显示原币费用。
- 更新产物放在本项目 GitHub Releases，使用签名安装包和独立 manual_only 策略；私钥留本机或 GitHub Secrets。
- 允许在 MCP 接入前升级 Go，使用修复后的官方 Go MCP SDK。M1 现有 Go 1.24.13 未在本轮修改；后续最低版本依据锁定修复版 SDK 确定。
- 供应商必须可自定义，覆盖本地与云端 API，包括名称、协议、Base URL、model、凭据、默认连接与能力/价格。没有确认任何一家供应商账号为唯一选项。

计划采用的工程边界（不是已实现事实，也不冒称原产品字段）：

- 首批媒体协议模板为 OpenAI Images 与方舟视频任务协议；它们允许自定义兼容端点，不表示任意服务仅改URL即可兼容。LLM使用受支持的opencode内置/自定义provider。
- 新增项目/资产、配置、会话、问题回执和流水线入口的schema，在相应任务落实时写明新增/适配依据；沿用原规格的路径与五种错误码。
- 素材搜索先覆盖真实本地图片与已登记voice目录；没有远端搜索适配时如实说明范围，不生成虚假搜索结果。
- MCP增加专用连接令牌和受管轮次绑定；所有Origin检查保留。单活跃agent轮次是MVP并发上限，不限制后台生成任务的持久恢复。
- 原来源与移植契约冲突时以用户已确认契约为准，差异逐项登记。全部真实视频/付费/签名发布验收需要实际连接和密钥，离线fixture不替代这些验收。

## M2.1 请求边界与持久幂等（移植适配）

- 构造入口为 `server.New(origin, db, workspaceRoot)`；主进程传入 `<dataDir>/workspace`。M2.1 当时仅公开 `GET /api/health`；M2.2 已增加下述对象路由。未实现的 `/api/objects` 等仍返回 404/not_found，不存在成功占位响应。
- Origin 在所有路由之前检查，拒绝请求返回 403/invalid_input。预检仅批准已实现路由的方法；`/api/health` 仅 GET，下述对象路由允许 GET、POST。共享 CORS 方法清单为 GET、POST、OPTIONS，允许请求头为 Content-Type、Last-Event-ID。其他请求头、路由、方法的预检返回 403/invalid_input。
- `DecodeJSON[T]` 限制正文为 1 MiB、只接受单个 JSON 对象、拒绝未知字段和任意层级重复键、拒绝尾随 JSON，并以 `UseNumber` 保留数值 token。请求 DTO 通过显式 `validate` 标签声明必填等规则；解析成功后统一执行 validator v10.25.0。嵌套请求必须声明具体 DTO，不能通过 map 或 any 绕过未知字段检查。媒体 multipart 不走此 JSON 入口，后续单独限制。
- 写操作作用域为 `(project_id, operation, requestId)`；`project_id` 来自查询参数，不能依赖全局当前项目。项目和请求标识使用 1–128 字符的 ASCII 字母、数字、下划线或连字符；不 trim，不接受路径片段。operation 由服务端选择，必填且最多 128 字符。此阶段提供作用域校验，项目是否存在及对象归属检查随 M2.2 业务路由落地。
- 业务入口应将解析/校验错误映射为 400/invalid_input（正文超限为 413/invalid_input），`store.ErrRequestConflict` 映射为 409/invalid_input；此阶段没有写入 HTTP 路由，因此上述状态由后续业务入口接线。
- `store.WriteRequest` 在同一 SQL 事务内登记请求、执行写入、保存完成回执；任一步失败同时回滚业务数据和请求记录。输入在存储入口统一规范化：对象键排序、数组顺序保留、数值 token 原样保留、prompt 等字符串内容不 trim。同键同输入重放持久化回执，不调用写入函数；同键不同输入返回冲突。已有未完成记录返回 `ErrRequestPending`，不重新执行；文件投递恢复由后续 journal 服务处理，此 SQL helper 不执行文件 I/O。
- migration 000003 新增 projects、composition_types、asset_projects、write_requests、file_operations 五张支撑表，沿用计划 SQL；核心五张业务表保持不变。文件投递 journal 仅建表，M2.1 不实现文件投递。

## M2.2 项目、类型、对象 CRUD（移植适配）

`GET/POST /api/projects`、`/api/assets` 为新增路径，`/api/composition-types` 为新增项目内类型管理路径。`/api/compositions`、`/api/resources`、`/api/versions` 沿用规格路径。以下请求/响应字段是本移植明确约定的 schema；全部由同一个 `store.Objects` 服务执行，直接服务调用也校验对象归属。

- 项目 GET 列表和 POST create 不需要 `project_id`，项目创建使用固定 `_app` 幂等作用域。项目 update/delete 使用 `project_id` 并要求 `id` 等于该项目；项目 GET 单项可用 `?id=...`。其他路径必须提供 `project_id`，无当前项目全局变量。重复、未知、空或畸形查询字段返回 400。
- GET 无 `id` 时返回 `{items:[...],coverage:{total,next_offset,exhausted}}`；offset 默认 0、不得为负；limit 默认 50、显式值 1–200。total 与行数据在同一读事务读取，按 id 稳定排序。GET `?project_id=...&id=...` 返回单个对象，不得与分页参数组合。对象不在该项目时返回 404/not_found。
- POST 统一为 `{requestId,action,id?,value?}`，action 仅 create/update/delete。create 不得带 id，update/delete 必须带 id；update 必须提供至少一个允许字段。delete 的 value 可省略或为空对象，两者各自保留幂等输入身份。显式空/null id、null value 被拒绝，避免重编码合并不同有效请求。
- 每个 value 按动作和对象种类严格解析；重复键、未知字段、错误大小写、无效 Unicode、尾随内容均拒绝。attributes/metadata 是领域数据袋，API 表示为 **nullable JSON-object string**，例如 `"attributes":"{\"色调\":\"蓝色\"}"`；字符串必须包含合法 JSON 对象，不接受数组、普通文本或直接嵌套对象。合法原字符串原样保存，空对象用 `"{}"`，清除用 `null`。领域袋内部键不受固定属性白名单限制。
- 成功 POST 返回创建/更新后的对象；delete 返回 `{id,deleted:true}`。所有服务生成的实体 id 使用 UUID。创建版本的 resource_id 明确指向既有 Resource；修订创建新 Version、继续使用该 Resource，独立内容先创建新 Resource。source_version_id 允许同项目其他 Resource 的版本作为派生血缘。

| 对象 | create value | update value | delete 保护 |
| --- | --- | --- | --- |
| projects | `{name}` | `{name}` | 有类型、对象、资产登记或文件 journal 时拒绝 |
| composition-types | `{label}` | `{label}` | 有引用它的 Composition 时拒绝；同项目 create 相同 label 原子复用既有 id，rename 与其他 label 冲突为 409 |
| compositions | `{type_id,name,attributes?}` | `{name?,attributes?}` | 有自有 Resource 或成员放置时拒绝 |
| resources | `{composition_id,description?}` | `{description}` | 有 Version 或成员放置时拒绝；description 可为 null |
| versions | `{resource_id,asset_id?,source_version_id?,origin?}` | 始终 409，版本不可变 | 被其他版本血缘引用时拒绝；当前没有其他依赖的叶版本可删除 |
| assets | 拒绝直接创建；真实文件登记由 M2.3 接入 | `{name?,metadata?}` | 被版本引用时拒绝，否则仅 status=missing，不删除文件或登记行 |

版本 origin 可为 agent/user，省略为 agent；asset_id/source_version_id 可省略或 null，提供时必须属于同项目，asset 必须 active。项目/类型/对象 name 或 label 为 1–256 字符；resource description 可空或 null。返回字段与对应表的 snake_case 字段一致；SQL NULL 返回 JSON null，列表始终为数组。

项目 bootstrap 示例：

```json
{"requestId":"project-create-1","action":"create","value":{"name":"宣传片"}}
```

同 Resource 新版本示例（POST `/api/versions?project_id=<project UUID>`）：

```json
{"requestId":"revision-2","action":"create","value":{"resource_id":"<existing Resource UUID>","source_version_id":"<previous Version UUID>","asset_id":"<registered Asset UUID>","origin":"user"}}
```

所有成功业务变化与 completed receipt 同一 SQL 事务提交；同键同输入精确重放回执，同键不同输入 409/invalid_input，未完成 receipt 返回 409/waiting_user。正文超限 413/invalid_input；其他验证错误 400/invalid_input；不存在或跨项目 404/not_found；引用保护与不可变版本 409/invalid_input；数据库不可用 502/upstream。共享 `DomainError{Status,Code,Message}` 供 HTTP 与未来服务调用映射，不增加五种错误码之外的新 code（capacity 供后续 429 使用）。

M2.2 **未完成文件登记**：`POST /api/assets` create 返回明确错误，须经 M2.3 `/api/upload` 与 `/api/upload/commit` 实际验证受管普通文件并采集指纹；不接受客户端伪造的 active 资产、路径或哈希。M2.2 asset delete 只标 missing，不移动或物理删除文件。

后续新增引用必须在相应事务服务增加删除保护：M2.4 delivery journal 已预分配/记录的对象引用；M3 canvas 当前版本选择；M4 task 输出/版本引用；M6 pipeline 血缘。当前这些表尚未创建，M2.2 不冒称已实现它们的引用检查，也不修改核心五表。

## M2.3 工作区文件、登记与恢复

已将用户允许的 Go 升级提前到此任务：`go 1.25.0 / toolchain go1.25.8`，CGO 仍为 0。Windows 是本阶段受测平台。`server.New(origin,db,workspaceRoot) (*Server,error)` 现在实际打开工作区；目录不存在、不是目录或不安全时失败。`Server` 实现 http.Handler 与 Close；主程序创建可信工作区目录，HTTP drain 后关闭工作区，再关闭 DB。

所有文件接口要求 `project_id`，所有写入要求 `requestId`；严格检查每个接口自己的字段、重复查询、Origin。新增 Range 预检允许头。错误保持统一五种 code；Range 错误也返回结构化 JSON。路径统一是 workspace 相对、正斜线格式 `projects/<project>/files/...`，staging 返回的路径仅供 commit。公开 list/content/rename/delete/copy/mkdir 不访问 staging/recycle。

| 接口 | 输入 | 成功输出 |
| --- | --- | --- |
| POST /api/upload | multipart 单 file 与 requestId，任意顺序，默认上限 256 MiB | `{path}` |
| POST /api/upload/commit | `{requestId,path}` | `{ok:true,relative}` |
| GET /api/files/list | type、sort、offset、limit；type=all/image/video/audio/document，sort=time/name | `{files,folders,coverage:{total,next_offset,exhausted}}` |
| GET /api/files/content | path；支持 Range | 流式文件内容，HTML/SVG attachment + sandbox |
| POST /api/files/content | `{requestId,path,content,unique?}`；content 必填但可空 | `{ok:true}` |
| POST /api/files/mkdir | `{requestId,path}` | `{ok:true}` |
| POST /api/files/rename | `{requestId,path,new_name}` | `{ok:true,new_path,new_name}` |
| POST /api/files/copy | `{requestId,paths,target}`，target 是现有目录 | `{ok:true}` |
| POST /api/files/delete | `{requestId,paths}` | `{ok:true}` |

list offset 默认0、limit默认50且1–200，文件按mtime降序或name排序；folders返回已观察到的目录。文件条目是name/path/type/folder/size/url/mtime。content POST 是用户明确的原 PUT 适配：只支持 .txt/.md/.json/.csv/.yaml/.yml 文档。unique=true 遇到已有文件拒绝；省略/false 允许替换无版本引用的文件。任何 Version 引用的路径拒绝覆盖并指向 deliver/document_write。替换先创建新物理文件和资产 ID，旧文件移动到 recycle 并标 missing；不会就地 truncate。delete 同样先检查全部路径和引用，再移动至私有回收目录，标 missing；不物理删除历史。rename 支持目录、保留文件 identity 和所有后代 asset ID，SQL 原子更新所有后代路径；目标冲突使用 `name (n).ext`，原生无覆盖发布。copy 仅支持普通文件；目标冲突拒绝，不覆盖；目录 copy 明确拒绝。

`POST /api/assets` create 已从 M2.2 的拒绝升级为 `{requestId,action:"create",value:{path}}`。共享 `workspace.Service.Register` 检查已发布的同项目普通文件，采集真实 metadata/fingerprint，再在事务中登记 assets+asset_projects；已登记同path、identity和完整内容相同才返回既有asset，不按相似内容合并独立身份。size/inode/hash/MIME 等客户端字段被拒绝。asset update/delete 继续走 Objects；待完成文件操作保护同样在其 SQL 事务内执行。新建 Version 的同事务保护避免与删除/替换竞争。

文件层 `workspace.Open(root)(*Workspace,error)`、`Close`、`OpenRegular(projectID,relative)`；业务层 `NewService(db,workspace)`、`Stage(ctx,projectID,requestID,name,reader)`、`Commit(ctx,projectID,requestID,stagedPath)(store.Asset,error)`、`Register`、`Mutate`、`List`、`OpenContent`。M2.4 可复用 Stage/Commit 的受管归档原语和 OpenRegular/Fingerprint，不能让 agent 暴露任意外部文件写入口。

`Fingerprint(file,full)` 返回真实 size/mtime_ms、Windows VolumeSerialNumber + FileIndexHigh/Low 的64位组合（inode以int64保存相同位模式；不是128位FILE_ID_INFO），quick_hash 是首4MiB的XXH3-64固定16位hex，full=true时计算全文件同算法。公开asset的full_hash按需登记，不因每次读取预览而写入；journal也使用完整XXH3-64 digest。仅同size/mtime绝不代表相同内容。MIME来自字节探测，PNG/JPEG/GIF尺寸来自图片header；未实现格式的尺寸/时长为null。

现有 file_operations 表不改：主键(project_id,request_id)，operation+canonical input_json必须一致，source是typed move数组（source/target/dev/inode/digest/asset/directory及temporary identity），target是目标路径JSON数组，receipt是精确响应。阶段 uploading→uploaded→completed，其他操作 prepared→completed；multipart内部spool完成清理后retired。每次文件动作前保存计划，完成后的资产SQL与receipt同事务；文件I/O不在SQL事务中。中断的同请求恢复原预分配identity/目标；completed先重放旧receipt，不重查已迁走的源。失败的私有临时文件仅用记录identity的句柄删除；创建后但identity落库前崩溃的未知临时文件留待人工/未来GC，绝不按路径误删。

Windows安全边界：持有os.Root打开可信根，逐级相对句柄打开项目/父目录；拒绝reparse/junction、hardlink、非disk/非普通文件，拒绝绝对路径、UNC、ADS、设备名、尾点/空格及大小写/8.3别名。持有父目录链直到创建/移动完成且禁止外部delete-sharing；待改源有DELETE权限并在同句柄重新检查identity。NtSetInformationFile(FileRenameInformation) ReplaceIfExists=false确保原子无覆盖。测试实证外部重命名被拒绝、自有句柄重命名成功。具有相同OS权限的外部进程仍可在请求之间改内容；操作会按identity/digest拒绝异常恢复，应用不把该类篡改默认为新合法资产。

并发上限：单服务互斥串行文件意图；单gateway实例持有该服务。批量在动作前验证所有输入；中途外部故障时journal保留已完成部分、同键续做，不宣称跨文件系统原子回滚。M2.4仍须在delivery_sources创建时加双向pending保护：文件rename/delete/content拒绝破坏recoverable source/archive路径，delivery登记拒绝与prepared file操作相交；同时保护pending delivery对象引用。此任务没有提前创建delivery表。

## M2.4 幂等投递与事务恢复

`POST /api/deliver?project_id=<id>` 严格接受用户 §7 正文：

```json
{"requestId":"import-1","sources":[{"key":"image","path":"projects/<id>/files/image.png"}],"output":{"resources":[{"key":"image","mode":"create","name":"主画面"}],"composition":{"name":"作品","type":{"label":"视频"},"placements":[{"key":"image","role":"主画面"}]}}}
```

来源为 `path` 或 `taskOutputKey` 二选一，1–100 个且 key 必填、唯一；资源 key 集合与来源相同，placement 只能引用这些 key。原解析器允许不同 key 指向同一路径，移植保留该行为：共享一个 Asset/归档文件、create 仍分配独立 Resource。不同路径即使字节相同也不合并。`path` 仅允许同项目 files/staging 内的受管普通文件，沿用 Windows 原生句柄检查。已登记且 identity/full digest 一致的 active 文件复用 Asset；未登记文件按预分配 Asset UUID 移至 `projects/<id>/files/<assetId><extension>`，无覆盖。

`composition.type` 为 `{id?,label?}`，至少一个非空字符串；id 必须属于同项目，同时提供时 label 必须匹配。Composition 使用同项目 name+type 精确匹配，零个则分配创建，一个则复用，多个则 blocked。沿用既有 Composition 名称/类型标签 1–256 字符限制；Resource name 为 1–512 字符，create 映射至现有 `resources.description`，不新增核心列。new_version 不修改共享 description。`mode=create` 禁止 baseVersionId；`mode=new_version` 必须提供同项目既有 baseVersionId，延续其 Resource 并写入 source_version_id，目标 Composition 必须兼容。允许从非最新版本分支；Resource+Composition+role 不重复放置。跨 Resource 派生仍由既有 CRUD source_version_id 支持，delivery create 不承载该关系，M6 补充独立生成依赖。

成功处理的业务回执为 `{delivery:{status,versions,problem?}}`，HTTP 200；状态仅 completed/unknown/blocked。versions 项明确采用移植字段 `{key,resourceId,versionId,assetId}`，按 sources 顺序；未完成和 blocked 均返回空数组，不声称部分对象成功。problem 为 `{code,message}`，code 沿用五种错误码。不可修正的输入/归档 identity 或 hash 不符返回持久 blocked，修正使用新 requestId；暂时冲突、I/O/SQL 失败返回 unknown，同键同输入重试或调用 Recover，不重新生成。JSON 解析、项目/requestId 作用域错误通过 HTTP 400；未知项目 404；同键不同完整 canonical input 返回 409/invalid_input（优先于 terminal 回执处理）。未知字段、重复字段、大小写错误和 null/空可选字符串拒绝。完整 wire input 保留可选字段 presence；解码后再修改导出字段的直接调用会被拒绝，调用方应构造新 Request。

migration 000004 仅新增支撑表 `deliveries` 与 `delivery_sources`。deliveries 保存 input_json、stage/status、receipt、首次可信 origin、plan_json（预分配 Composition/type、placements、来源完整指纹等）；delivery_sources 保存 source_key/source_path/archive_path、asset/resource/version/base_version IDs、stage 和 source_json。prepare 在一个事务冻结所有 ID，文件阶段每源归档后持久标记，最后一个事务提交全部新增 Asset+project membership、Composition/type、Resource、Version、成员与 completed receipt。任一 SQL 失败整批回滚。重启从已确定的 identity/path 续做；completed 重放先返回旧 receipt，不再检查已改名的文件。journal/receipt 引用的项目、类型、Composition、Resource、Version、baseVersion 和 Asset 均受直接 CRUD 删除保护；unknown 期间资产 metadata 变更及相交路径的文件操作被拒绝。文件 journal 的源与目标检查、delivery prepare/commit 检查均在相应 SQL 事务中，保护双向竞态和改名碰撞后生成的新目标。

内部接口为 `delivery.NewService(db,*workspace.Workspace,origin) *Service`、`Deliver(ctx,projectID,Request)(Receipt,error)`、`Recover(ctx,projectID,requestID)(Receipt,error)`。origin 只接受可信内部 user/agent，HTTP 构造为 user；首次 intent 保存后，不随重放入口改变（包括 UI 继续 agent 的 unknown）。多个服务实例共享进程级互斥锁，以支持 HTTP 与未来 MCP 实例交错；目前单 gateway 进程，多进程共享同工作区需增加数据库 lease。测试故障注入仅未导出字段，HTTP 不暴露。

新增最小 workspace 原语 `InspectDelivery`、`ArchiveDelivery`、`VerifyDelivery` 不提交 SQL；`InsertDeliveryAsset(ctx,tx,projectID,asset)` 参与调用方事务。共享 `store.CheckFilePending` 与 `CheckDeliveryPaths` 供事务内 reservation 检查。taskOutputKey 在 M4 实际 task/output 表接入前明确返回 blocked/not_found，未创建伪任务或产物。M4 须以唯一 opaque key 查真实同项目任务归属和输出，M5 通过可信 agent 实例调用同一服务；M2.5 负责工作台 UI 与完整里程碑验收。

M2.4 审查补充：composition.placements 必须提供 1–50 项，省略、null、[] 和超过 50 项均拒绝；每项 key 必须在 resources 中，但并不要求每个 Resource 都被放置（原 parseCompositionOutput/list2 事实）。直接服务构造的 nil/空 placements 同样拒绝。检查来源文件前先检查相交的 pending 文件意图，检查期间来源暂时缺失则重新检查 reservation；最终 prepare 事务的完整源/目标检查保留，临时 source gap 返回 unknown，文件操作完成后原键可续做。unknown 投递使用的 Composition name/type label 在共享 CRUD 更新事务中禁止实际改名；不影响同名 no-op、Composition attributes 更新或 completed 投递目标的普通改名与回执重放。

M2.4 二次审查补充：placements 的 key 必须唯一，公开解码和直接调用使用同一校验；50 项有效边界须对应 50 个不同 key（这些来源可以共享同一路径）。来源检查成功也可能读到尚未提交的 content 替换文件，因此 prepare 事务先检查 source reservation，再比较已登记 Asset 的 status/identity/digest；随后仍检查最终归档目标，避免把应用内合法的替换窗口固化成 blocked。

## M2.5 桌面素材工作台与恢复

继续复用 TanStack Router/Query、M1 gateway metadata/health 及 `gateway.ts` 单一 HTTP 客户端。新增 `api<T>(projectID,path,init):Promise<T>`、`newRequestID()`，失败保留服务端 error 的 code/message/status。没有新增后端接口或更改 M2.1–M2.4 schema。项目列表按 coverage 加载更多；素材、Resource、Composition、Version 按 200 项逐页读至 exhausted，查询键包含 projectID、列表、分页策略，signal 传给 fetch。切换项目重建视图、清空选择并取消旧查询，旧操作只回写原项目。

实际路径：创建/选择项目 → 选文件或拖入 → multipart upload → commit → deliver → 选中完成的对象版本。展示真实 asset name/MIME/dimensions；新版本沿用选中版本的 Resource 与 Composition/type，source_version_id 指向原版本。对象引用保留在 DOM data 属性，可见正文使用名称和版本时间，为 M3 留引用；没有 M3 编辑器或示例素材。

桌面使用已有 API，示例：

```text
POST /api/projects
{"requestId":"project-intent-1","action":"create","value":{"name":"宣传片"}}
→ {"id":"<project>","name":"宣传片","created_at":1792070000000}

POST /api/upload?project_id=<project>
Content-Type: multipart/form-data; boundary=<browser-generated>
requestId=upload-intent-1, file=<真实文件字节，原文件名 cover.png>
→ {"path":"projects/<project>/staging/<staged>/cover.png"}

POST /api/upload/commit?project_id=<project>
{"requestId":"commit-intent-1","path":"projects/<project>/staging/<staged>/cover.png"}
→ {"ok":true,"relative":"projects/<project>/files/<asset>/cover.png"}

POST /api/deliver?project_id=<project>
{"requestId":"deliver-intent-1","sources":[{"key":"file","path":"projects/<project>/files/<asset>/cover.png"}],"output":{"resources":[{"key":"file","mode":"create","name":"cover.png"}],"composition":{"name":"素材作品","type":{"label":"素材"},"placements":[{"key":"file","role":"素材"}]}}}
→ {"delivery":{"status":"completed","versions":[{"key":"file","resourceId":"<resource>","versionId":"<version>","assetId":"<asset>"}]}}
```

新版本只将 Resource 输入改成 `{"key":"file","mode":"new_version","name":"cover.png","baseVersionId":"<selected-version>"}`，Composition 使用已有 name 和 type:{id}。HTTP user origin 由服务端固定，前端不添加 origin。unknown 返回 `{"delivery":{"status":"unknown","versions":[],"problem":{"code":"upstream","message":"..."}}}`；blocked 相同结构、status=blocked。unknown 展示“结果待确认”及“重新查询/续投递”，同键同 body POST。blocked 为已知终态，清除记录并展示 problem。上传/提交网络或 HTTP 失败也保留原输入，不自动生成新意图。

每项目最多一条待导入；localStorage key 为 `moviedesign.import.v1.<projectID>`：

```json
{"schema":1,"projectID":"<project>","stage":"upload","uploadID":"<uuid>","commitID":"<uuid>","deliverID":"<uuid>","file":{"name":"cover.png","size":68,"sha256":"<full-sha256>"},"target":{"name":"cover.png","composition":{"name":"素材作品","type":{"label":"素材"},"placements":[{"key":"file","role":"素材"}]}}}
```

首次发送前保存三个 ID、完整文件 SHA-256、原名称/大小与目标配置；修订另有 target.baseVersionID。已知上传路径后保存 stage=commit、stagedPath、commitBody；已知提交路径后保存 stage=deliver、committedPath、deliveryBody。两份 Body 都是原始 JSON 字符串，回执丢失或重启后直接重发，不重建、不调用 newRequestID。localStorage 写入失败不会发送对应请求。仅 completed/blocked 回执清除记录；格式损坏保留并阻止覆盖。字节只留内存，上传阶段重启必须重选同文件；名称、大小、完整 SHA-256 相同才沿用 uploadID，服务端仍验证真实内容。超过 256 MiB 在读取前拒绝；WebCrypto 整文件缓冲上限及未来流式升级已用 ponytail 注释标明。

所有阶段写入及终态清除先同步比较 localStorage 当前记录与该续链持有的完整旧记录（包括意图 ID、stage 和 body）；初始导入只允许在当前无记录时保存。若其他重挂载实例已推进同意图或开始另一意图，旧响应立即停止续链，不回退阶段、不覆盖或删除新记录。所有不匹配出口同时验证当前持久记录并同步视图（包括 null），清理旧内存文件并提示记录已更新；空记录解除待导入状态，已推进记录允许用户再次按当前阶段续做。此次同步不发送旧请求、不更改当前存储。比较与 setItem/removeItem 之间没有异步让出点。查询取消本身不作为写操作的并发保护。

项目选择另存 `moviedesign.project`；不保存 blob、凭据或外部文件路径。缩略图用 `<img crossOrigin="anonymous">` 加载受 Origin 保护的真实 content，SVG 不内联、不用 object/embed。CSP 仅为 img-src/media-src 加入准确 `http://127.0.0.1:8001`，default/script/frame 维持限制，服务端 SVG attachment/sandbox 保留。浏览器不支持的格式不声称有像素预览。

项目选择偏好读取/写入失败只展示独立存储告警，继续挂载工作台并允许内存中的项目选择，不改变 gateway ready 或禁用项目查询。该降级只适用于选择偏好；创建项目及导入意图仍须成功持久化才能 POST。

项目创建另以应用级 `moviedesign.project-create.v1` 保存完整原始 POST JSON（requestId/action/value.name），发送前先写入；网络/5xx 等未确认结果保留，刷新/重启显示原名称及“续建项目”，重发同 body。已知 400 验证拒绝或成功回执清除，400 后可修改名称。损坏记录保留并阻止覆盖。真实 smoke 从 DOMStorage 捕获原创建 body，模拟回执丢失后刷新、点击续建，独立列表确认项目总数仍为一。

`scripts/smoke-desktop.ps1 -Mode release -Smoke m2` 使用进程独有 data dir、CDP port、WEBVIEW2_USER_DATA_FOLDER；检查真实子浏览器 command line 的 profile，重启复用该 profile。smoke 在 DOM 表单创建项目，用 DOM.setFileInputFiles 导入 PNG，经真实 UI 完成 upload/commit/deliver/revision，验证 naturalWidth、同请求 receipt、独立版本列表计数、血缘和跨项目隔离。CDP DOMStorage 捕获实际发送前冻结的完整 operation，再播回该已完成意图模拟回执丢失；先刷新再关闭/重启，按钮必须重发相同 POST body/requestId，版本数不增加。没有生产故障注入接口。证据见 milestones.md，运行数据只留 `.runtime/`。

## M3.4 图像编辑与恢复（2026-10-16）

`ImageEditor({assetId,source,onExport,onAnnotations,ref?})` 在 asset/source 改变时创建及销毁 Editor，回调经 ref 更新，不重建历史。`onExport(Blob):Promise<void>` 接收实际 PNG；`onAnnotations(Annotation[])` 及可选 `ImageEditorHandle.collectAnnotations()` 提供 `{id,assetId,rectangleId,tagId,comment,bounds}`。内部逻辑宽度为 `min(960,naturalWidth)`，高度保持图像比例；输出 bounds 乘以 natural/logical 比例，是源图像像素，与 CSS 缩放及 DPR 无关。

标注矩形为尚未提交的事务草稿；确认说明后在同一事务添加 tag。取消草稿回滚。只保留不可变 pair ID 记录，是否存在、说明、位置均来自当前 Editor 图形，因此撤销删除、修改说明及移动同时恢复元数据。记录保留至关闭编辑器，没有第二份历史，也没有扩充 flat ShapeData。任一配对 ID 删除扩展为两者；区域或标签移动在同一事务更新搭档。

EditorOptions 的三个局部策略入口：`onShapeCreate(data):boolean` 返回 true 表示 wrapper 已接管草稿；`expandDeletion(ids):Iterable<string>` 为所有删除命令扩展 ID；`beforeCommit(previousShapes)` 在同一历史快照比较前同步搭档。`removeShapesWithoutHistory(ids)` 仅做当前事务内删除；`rollbackInteraction()` 明确恢复事务开始的快照而不入历史。原 `cancelInteraction()` 仍只清除快照，不做回滚。键盘先判 isComposing 和 input/textarea/select/contenteditable 焦点；画布主动处理焦点且阻止随后 mousedown 默认动作抢走新 textarea 的焦点。

透明 textarea 只负责输入、光标和 IME；Canvas 画实际字形，字号、字体、字重、1.4 行距和逻辑坐标一致。Enter/blur 提交，Shift+Enter 换行，composition 期间不提交。已有 text Escape 回滚，新 text Escape 删除且不留历史；tag Escape 删除并提交（配对 tag 同时删除 rectangle）。空白提交删除。

编辑保存复用 AssetLibrary 中同一 `run/owns/save` 的 upload→commit→deliver(new_version) 路径：冻结 baseVersion、composition、三个 requestID 和完整文件身份，后续每个 POST 前仍持久化原始 body。新字段 `generatedPNG:true` 区分普通文件与生成图像。普通 M2 文件仍由用户重选同文件；编辑 PNG 必须先在原生 IndexedDB `moviedesign-editor-exports` / `png` 按 `projectID/uploadID` 提交 `{file:{name,size,sha256},blob}`，再写 localStorage 意图，最后才允许网络 POST。Blob 不写入 localStorage，不重新绘图替代相同请求的字节。每次恢复读取 Blob 并验证名称、大小、完整 SHA-256；损坏/丢失保留意图并阻止 POST。首次存储拒绝保留内存中冻结意图和 File，供当前窗口显式继续；未持久化成功前不能保证重启恢复。

重启仅显示待确认操作，用户点击后才恢复原字节和相同 ID/body。并发响应继续使用完整前驱记录的同步 ownership 比较。completed/blocked 后只删除该 uploadID 的临时 Blob；清理失败不把已知保存结果变成未知，已完成版本仍刷新/选中并提示本机清理失败。切换项目销毁编辑器；切换版本关闭编辑器，旧结果不选入另一已选对象。未选对象的重启恢复会选择实际完成版本。

`scripts/smoke-desktop.ps1 -Mode debug -Smoke editor-product` 运行真实产品 UI、原生 textarea/IME、图形像素、标注和 IDB/版本血缘检查，并正常关闭及重启同 profile。`-Smoke editor` 保留现有核心 Canvas fixture，`-Smoke m2` 保留普通导入回归。无新增网络协议、依赖、后端修改或 LLM 调用。

## M3.5 持久创作画布（2026-10-16）

### HTTP 与原协议映射

所有路由仍要求精确 Origin，含匿名 CORS 媒体；无豁免。`GET /api/canvas/nodes?project_id=…` 对应原 `/api/v3/canvas` document GET，返回 `{schemaVersion:1,revision,nodes,edges}`，nodes/edges 是 ID-keyed maps。此路径没有另造 POST CRUD。`POST /api/canvas/commands` 接受 `{requestId,expectedRevision,command}`；requestId 是原 commandId 的明确命名适配，原节点、补丁和回执语义保留。命令体上限 16 MiB，其他 JSON 路由仍 1 MiB。

M3 支持 `canvas.patch` 的精确 `{type,nodes:[{id,before,after}],edges:[{id,before,after}]}` 及非嵌套、非空、最多 64 子命令的 `canvas.batch`。每个 patch 最多 30,000 项，节点最多 10,000、边最多 20,000；缺字段、未知字段、重复 JSON key、危险对象 key、重复修改 ID、noop、空 patch、父环、缺失端点/非 frame 父、ID 冲突均拒绝。标题/ID/prompt 长度按 UTF-16 计算。后端完整检查 project、对象、Resource/Version 对应关系与 composition 成员关系。task-output/hidden task 引用在 M3 因实际任务不存在而拒绝，M4 必须接入真实 task lookup。pendingGeneration 与 memberSelections 的 generation-request 是客户端待处理意图，可存储；activeItem 只允许 version/task-output。

`composition`、`placeholder`、`frame` 保留原字段和旧 presentation=subject 正规化，媒体类型来自对象内容；没有 image/video/document 节点类型。其他原 content commands 在本阶段明确返回 invalid_input，未改造为替代协议。素材创建与编辑继续走 M2 upload/commit/deliver 恢复流程；用户单独选择已交付版本添加画布，因此不会引入一次 UI 操作跨对象创建/画布写入的无日志半完成事务。

`POST /api/canvas/content` 是只读批量查询，精确输入 `{compositionIds:string[]}`（最多 200，不能重复），精确顶层返回 `{items:[CompositionContent]}`。保留 ready/missing 投影、members/resources/version ordinals/currentVersionId、真实变化 revision；无任务时 taskOutputs=[]、protected=false。不是 offset 分页；对象发现仍使用既有 offset/limit+coverage API。三个 canvas 路由均 no-store。

### 事务、重放与引用

一个 SQLite writer transaction 读取当前文档、先查相同 requestId，再验证 revision/before/全图/对象引用，写 doc、effects 与 receipt。相同键输入不同返回 409 `details.kind=CANVAS_V3_COMMAND_ID_REUSED`；revision 冲突为 `CANVAS_V3_REVISION_CONFLICT`，before mismatch 为 `CANVAS_V3_BEFORE_MISMATCH`。沿用五种 error.code；canvas 子类型在 details.kind 中。命令只含 SQL，无文件 I/O/网络。

回执 `{requestId,appliedRevision,document,replayed,effects:{undo,compositionIds}}` 中重放保留原 appliedRevision/effects，但 document 是当前最新文档。不能用 M2 冻结完整 HTTP 回执代替。batch 最终只增加一次修订，undo 反向排列；patch 的 compositionIds=[] 与原 layout 效果一致。canvas_references 为当前图和每条持久 undo 保存索引，既有共享 deleteObject 检查它；移除节点仍保留可撤销对象引用，不能先删 Version 使 undo 悬空。当前 MVP 未提供清理持久命令历史 API，项目含画布日志时不允许删除；未来需要显式历史截断策略后才能回收这些引用。

### 000005 支持状态适配

000001/000002 与五张核心表保持不变。000005 新建 canvas_documents、canvas_commands、canvas_references、canvas_composition_state、canvas_resource_state、canvas_version_state。后者补齐原 Resource 当前版本、稳定 ordinal 和 Composition revision；触发器覆盖现有 CRUD/delivery，版本/素材/资源变化同步增加拥有或放置该资源的 Composition revision。Version 删除不复用已分配 ordinal；current head 指向最新持久 ordinal，画布 activeItem 不改 Resource head。

从 M2 升级时 ordinal 按 created_at,id 确定性回填（相同时间的历史真实先后无法从旧核心恢复）；后续分配持久 next_ordinal。core Resource 可直接创建而尚无 composition_members 行；content 为它投影稳定 `memberId=owned:<resourceId>`、空 role，不制造新 Resource/Version，显式视为该 Composition 拥有成员。真实 placement 仍使用 composition_members.id 字符串和 role。M3 没有 soft-delete composition lifecycle，因此缺失返回 missing；没有伪造 deleted、task、生成模型或 provider。

### 前端与恢复边界

CanvasWorkspace 与 ImageEditor/EditorHistoryManager 独立。节点 position 相对 parent frame；显示坐标累加祖先位置（原 p3 world$1:80663），父隐藏/折叠隐藏全部后代（visible:85565）；改父保留世界位置，移动 frame 保留子局部坐标。平移缩放仅本地视图，布局与版本选择走服务器 patch。可见性线性扫描上限 10k 并裁剪 DOM；没有将节点转为图像编辑器 shapes。

写前将完整 body/requestId/expectedRevision 存 localStorage `moviedesign.canvas.pending.v1.<project>`；本机记录同时冻结并校验操作 mode（normal/undo/redo），首次提交与显式续确认均按该模式归属回执 inverse，mode 不进入 HTTP body。重启只显示继续按钮，同 body/key 显式确认。旧版缺 mode 的记录仍可原样确认，但清空两种历史快捷操作；非法 mode 保留原记录并禁止提交。未知网络/5xx 保留记录，4xx 已知拒绝刷新后要求重新编辑，不自动改 body 重试。响应接受要求组件仍属于同项目且 revision 不倒退，清理记录比较完整前驱值。普通文件/PNG 导出仍由原 AssetLibrary 处理，其 IDB 字节和用户选择 revision 保护保持。

图像/video/audio 均 anonymous CORS；文本/Markdown 以 `<pre>` 原样安全显示，不执行 HTML。文本预览限 1 MiB；其他文件展示元数据。视图内图片可进入既有编辑器，保存产生同 Resource 不可变新 Version。基础属性、画框折叠/颜色、局部位置/尺寸/层级、版本切换和一步画布 undo/redo可用；持久 effects 保留服务端 undo，但 UI 历史栈本次仅一层且重启后不恢复快捷按钮。

## M4.1 自定义供应商设置（2026-10-16）

本节为用户确认的 BYOK 移植新增面，不冒称原产品账户/钱包 API。原恢复资料 `analysis-docs/01-架构与技术栈.md:286` 记录云端 token 注入，`06-复现差距分析.md:81` 记录 provider 云配置；本实现使用用户自行配置的本地/云端已知协议模板，不读取原应用凭据。

`GET /api/providers` 返回 `{items:[{config,has_key,default_for}]}`。`POST /api/providers` 使用应用级 `_app/providers/requestId`，不接受 project_id 或其他 query。必须精确 Origin；全部响应 no-store。仅有以下显式 action：

| action | body（除 action 外） | 行为 |
|---|---|---|
| create/update | requestId, config, 可选 key/remove_key | 创建/完整替换配置；update 不能改变 kind/protocol |
| select | requestId, id | 将 enabled 配置设为其 image/video 用途的唯一默认 |
| disable | requestId, id | 停用并撤销默认选择 |
| delete | requestId, id | 软删除、隐藏、停用、撤销默认，保留冻结版本 |
| probe | requestId, id | 显式免费模型列表探测或仅本地校验；原回执重放不再次联网 |

返回 `{provider?:{config,has_key,default_for},probe?:{local_valid,remote_tested,models_listed,model_found,http_status?,message},deleted?:true}`。顶层错误仍为五类；非法 schema/配置400、冲突409 invalid_input、缺配置404 not_found、存储失败502 upstream。响应不回显远端错误体、密钥或原请求。未知键、大小写变体、重复 JSON key、action 无关字段均拒绝，body 上限1MiB。

Config 精确字段：`id,name,kind,protocol,base_url,model_id,enabled,headers,capability,pricing`。支持 image/openai-images、video/ark-video；LLM 沿用 opencode 自身配置，媒体设置不另造 LLM adapter。headers 仅 `OpenAI-Organization`、`OpenAI-Project` 两个非凭据字段，限定 printable ASCII、200字符；不支持任意 header 或 Authorization 覆盖，CR/LF、Host、Origin、Content-Length、Connection 等均拒绝。凭据只能走 write-only `key`。省略 key 保留，空字符串拒绝；`remove_key:true` 移除当前绑定，不能同时给新 key。UI 从不回填占位符，保存成功清空密钥框。

capability 是声明，不是已验证事实：`max_prompt_bytes(1..65536),max_count(1..10；video=1),max_references(0..16),sizes(string[]),edit(bool),duration_seconds(int[] 1..60),audio(bool)`。image 不接受时长/audio，video 不接受 edit。此阶段不接受 paramSchema；M4.3 落地受限 schema 及模型字段白名单后再扩展，当前不会接受任意透传 JSON。价格为 null 表示未知；明确免费需要配置0。pricing 字段为 `currency(3位大写),unit(image|second),unit_amount(整数最小货币单位),minor_unit_scale(0..6),credit_numerator,credit_denominator,source,version,effective_at(Unix秒)`，无浮点金额或钱包。来源/版本必填；分母≥1；UI 同时显示单位与声明属性。

HTTP 本地连接只允许显式 localhost 或 loopback/private IP；云端 HTTPS。端口8001/4096全部阻止，metadata/link-local/unspecified/multicast、IPv4映射绕过、metadata别名、userinfo/query/fragment、点路径、编码路径等拒绝。`Endpoint(base,knownOperation)` 保留 base path。`NewClient(base,download)` 是后续 adapter 共用边界：禁代理、所有重定向、keep-alive复用导致的隐式重试；DNS全地址检查后直接 dial 数字地址，无二次解析；只有显式配置的本地主机/端口可以访问 private/loopback。download 模式允许 HTTPS 公网签名 query，禁止 API auth/组织/project/Cookie；后续下载必须使用无凭据请求。没有承诺任意内网 DNS 名或未知 wire 自动兼容。

Windows DPAPI 使用当前用户保护，`credentials/<UUID>.dpapi` 是不可变版本文件：先加密临时文件、Sync、关闭、MoveFileEx WRITE_THROUGH 原子发布，再在同一 SQLite 写事务更新 config 指针和 receipt。磁盘失败不更新SQL；SQL失败/进程在发布后中断只留下无引用密文孤儿，旧配置和旧凭据仍可用。旧版本不自动回收，未来需从真实任务引用证明无引用后才能删。`SecretStore.Put/Get/Delete` 的 Delete 不被设置删除调用。保留所有版本的磁盘增长是当前明确上限。

`Resolve(id)`/`ResolveDefault(kind)` 返回 `FrozenProvider{config,credential_version}`；`Credential(frozen)` 只在调用前解密相应版本。冻结JSON中不含明文key，配置编辑、停用、默认切换和轮换不改变已取得快照。M4.2 必须将整个FrozenProvider持久化到tasks.provider_json，不得运行时重新Resolve默认配置。000006提供tasks/attempts/outputs/generation_inputs/events；events.project_id可NULL供应用级事件，项目订阅只读指定project_id，任务事件必须有项目，由服务执行边界检查。000007提供provider_configs/provider_defaults/capabilities/price_quotes/questions/question_receipts。此阶段不创建假任务/批准/报价记录。

幂等存储复用 WriteRequest，输入是严格完整原JSON canonical后SHA-256摘要（保留遗漏/显式值/数组顺序差异），不把任何key写入SQLite。相同 requestId 改key也冲突。设置UI在首次POST前把原requestId及移除key后的完整输入、key的SHA256指纹保存到 localStorage `moviedesign.providers.pending.v1`；存储失败则不发送。未知结果跨刷新/关闭仍禁止新写入，重启不自动POST。`GET /api/providers/receipt?request_id=…` 是本次移植补充的只读应用级回执查询，严格单一request_id、精确Origin、no-store，直接读取既有write_requests已完成response，不返回input_json、密钥、密钥指纹或credential_version。404不代表未执行；用户只能重新输入与指纹匹配的原密钥，以原requestId和全部原字段显式重放。已完成回执可无需密钥直接核对并清除待确认记录。浏览器始终不持久化密钥本体。

核实的官方协议来源（2026-10-16）：[OpenAI list models](https://developers.openai.com/api/reference/resources/models/methods/list) 定义 GET `/v1/models` 和 list/data/id 响应；本实现只在自定义 API prefix 后追加 models，Bearer key及允许的组织/project头由网关发送，成功不代表生成权限。没有调用生成接口。兼容服务是否为列表请求收费取决于服务自身约定，此按钮只使用不执行推理的模型列表协议。Ark [创建视频生成任务](https://docs.volcengine.com/docs/ark/create-video-generation-task-api?lang=zh) 本次只核实官方协议入口，未找到本任务已实现的免费探测，因此 ark-video 明示“配置校验通过，远端未测试”。[Microsoft CryptProtectData](https://learn.microsoft.com/en-us/windows/win32/api/dpapi/nf-dpapi-cryptprotectdata) 当前用户保护；不设置 LOCAL_MACHINE、不使用CGO或新依赖。

M4.1 审查修正：capability 的可选 sizes/duration_seconds 在输入省略或为null时语义为空，所有JSON输出统一为 []，包含旧配置与旧回执；仅正规化输出，不改变既存请求摘要或重新生成请求输入。

## M4.2 — durable tasks, attempts and project event stream

Task generation admission is internal only until M4.3 supported capability validation; `/api/generate/image` is not registered. No current route approves billing. `tasks.Submit` freezes the complete prompt, ordered references, declared output, full `FrozenProvider` (including immutable credential identity), attempt1 and opaque declared output keys in one transaction with its `task_changed` event and idempotent receipt. Initial state is `waiting_user`, problem source `billing`, estimated credits null. There is no invented execution or percentage.

- `GET /api/tasks/:id?project_id=...` returns `{id,capability_id,status,revision,prompt,outputs,problem?,actions,progress:{phase,percent},billing:{estimated_credits}}`; output fields are `task_output_key,output_key,role,state,asset_id?,version_id?`. Public views never contain credential identity or raw executor result. Task and event query parameters accept exact allowed names only; unknown names (including encoded compound names) and duplicates reject.
- Optional `after_revision` (canonical integer >=1) with `timeout_seconds` (canonical integer 0–60, default30) observes revision changes. Future revision is invalid, terminal tasks return immediately, timeout returns current view. Disconnect cancels only this observer. This differs from internal `Wait`, which settles on completed/failed/cancelled, unknown, or nonbilling waiting_user; billing waits continue.
- `POST /api/tasks/:id/resolve?project_id=...` exact body `{requestId,action,expected_revision}`, action `cancel|retry`. Replay of identical request returns its original receipt before stale-revision checks; altered complete input conflicts. Current actions derive from persisted recoverability. Cancel is local intent, not a claim of upstream cancellation/refund; late result evidence stays in the same attempt. Unknown exposes cancel only. A definitively retryable generation failure creates another attempt and billing wait with original frozen input/provider/target. A retryable delivery failure with saved upstream result changes the existing attempt to `collection_pending`, preserving result and output identities; it never invokes generation Submit.
- `GET /api/events?project_id=...&session_id=...` (session optional) uses exact Origin, no-store SSE, `id: <durable decimal ID>`, `event: task_changed`, `data: {id,project_id,session_id,task_id,type,payload,created_at}`. Last-Event-ID omitted/0 reads from the beginning; a nonzero canonical cursor must belong to that exact project/session subscription. Foreign, future, negative, padded, signed, or repeated cursors reject. Gaps from other projects are normal; application NULL-project events are excluded. Subscribe occurs before catchup; one buffered wake and 100-row database batches bound memory. 15-second comments keep streams alive; write deadlines bound slow clients. Close cancels all owned streams. Events are retained indefinitely (disk-growth ceiling; future explicit retention migration required). Numeric durable SSE is this port's approved addition, not the original task WebSocket protocol.

`tasks.NewService(db,events,executor)` owns worker lifetime. `Run` can claim only the current approved unsent attempt using a conditional update, and commits submit-started before calling the adapter. `Executor.Submit(ctx,Attempt,checkpoint)` must persist upstream identity immediately through checkpoint; ordinary transport errors mean unknown unless proven nonacceptance with no saved ID. `Executor.Query` is strictly read-only. Startup `Recover` never calls Submit or Collect. A durable saved result takes precedence over provider identity: interrupted collection becomes failed/delivery `COLLECTION_INTERRUPTED` with explicit same-attempt retry, without Query. Without saved result, started/noID becomes unknown; savedID uses Query if installed, otherwise retains identity and unknown for later reconciliation. `Executor.Collect` consumes saved result only after explicit collection retry; collection callback errors preserve the result and expose delivery retry. Cancelling an unclaimed collection retry drains its local attempt immediately even with an original submission timestamp. An executing collector remains active until it returns; first result/error and upstream identity remain immutable and cancellation is never revived. Startup drains interrupted cancelled collection without automatic delivery or provider calls. None of these callbacks is configured in production M4.2. M4.4 must atomically connect actual human approval to `task_attempts.state='approved'`; never expose that state change through agent Resolve.

`Input.Provider` and `Input.OriginalWire` are trusted Go-only fields. M4.3 must call `ReplaySubmission(ctx,project,requestID,completePublicWire)` before mutable provider/capability resolution, then supply `OriginalWire` with resolved internal Input. The receipt compares complete canonical public wire; frozen_input_json retains internal ordered asset/version references. When called directly without OriginalWire, Input's exact original decoded JSON is retained. Do not remarshal a public DTO and thereby collapse omitted/null distinctions.

Output `source_json` is a frozen reservation containing `composition_id,type_id,type_label,name,new_type,new_composition,resource_id,base_version_id,version_id,ordinal`. IDs are allocated without fake file/version/resource rows. Existing targets are scoped and bound; pending names and rename/delete guards prevent retargeting. Ordinal is allocated once per Resource from existing history and durable reservations, and existing `canvas_resource_state.next_ordinal` advances past it so direct Version CRUD cannot steal the slot. Retry copies this whole snapshot. `store.ResolveDeliveryTarget` is the original delivery name-resolution logic extracted for shared admission. An absent or cross-project CompositionType ID returns domain `not_found`, producing an ordinary delivery `blocked` receipt; real SQL errors remain uncertain. M4.5 task-output delivery must consume the exact frozen IDs, reject collisions instead of choosing another target, insert the reserved Version ID and assign its reserved ordinal within the same delivery transaction. Existing `canvas_version_insert` first assigns the high-water ordinal and increments it; the task delivery must replace that newly inserted version's canvas_version_state.ordinal with the reserved ordinal before commit, without lowering next_ordinal. Preserve `(resource_id,ordinal)` uniqueness. Path/taskOutputKey-to-file resolver remains intentionally M4.5.

Canvas task-output refs remain `{type:'task-output',taskId,outputKey}`: a logical slot across retry; explicit Version refs pin a delivered artifact. `taskOutputs` uses original `{taskId,outputKey,ordinal,resourceId:null|id,versionId:null|id,state:generating|failed|ready}`. Delivered slots stay ready even if the task fails/cancels. Undelivered slots have no fabricated Version; reserved Resource appears only when actually present. Composition revision changes with task transitions and protected reflects nonterminal tasks. Old attempt opaque outputs and frozen refs remain deletion-protected; no GC is implied.

HTTP health and JSON-RPC receive the same `Server.Health` callback. Idle counts executing HTTP writes/files and owned task work; billing cards and idle SSE are not computation. safe_to_suspend additionally rejects approved/submitting/running/unknown/collection work, prepared files, and unknown deliveries. Shutdown disallows new work, interrupts owned workers and streams, drains admitted HTTP/file work, then closes workspace/DB. No new dependencies, migrations, provider requests, or additional service ports were introduced.

## M4.3 capability, quote and image adapter contract

Implemented porting additions: `POST /api/generate/estimate`, `POST /api/generate/image`, `POST /api/generate/mask` (all `?project_id=...`); `GET /api/v3/capabilities?operation=image&offset=0&limit=50`, `POST /api/v3/capabilities/get` with `{ids:[...]}` (1–20, deduplicated); and `GET /api/knowledge/providers/openai-images.md`. Original catalog evidence used `/api/v3/capabilities` with `{capabilities}` and repeated operation filters; this port uses short image/video/text operations, returns pagination coverage, and explicitly omits connector/plugin scopes. Batch detail HTTP and the owned card route are porting additions. Unsupported video/text catalog entries are not invented.

Image/estimate body: `{requestId,session_id?,capability_id,prompt,params?,references?,count?,output,quote_id?}`. `capability_id` is explicit `image:<provider-config-id>`. References are ordered asset-ID strings, including repeats; no path, URL, base64, internal asset metadata, provider snapshot or body project override. Omitted params/references/count normalize internally to `{}`/`[]`/1; explicit null rejects. Exact complete original JSON remains the receipt identity, including omitted versus explicit fields. Count must match every declared output resource and placement. New and replayed generation return **202 `{task_id}`**; task detail stays on `/api/tasks/:id`.

Admission replays the original task/quote receipt BEFORE resolving mutable provider configuration. It then validates schema, output/project ownership, reference media, and frozen bytes. Registered assets are read through Workspace confinement, tied to native device/inode, registered hash/size and full SHA-256. Execution rechecks ownership, pending file mutations, frozen native identity and SHA-256 before any provider POST. Reference order, roles and repeated positions survive quoting and execution. Provider code does not import tasks; `tasks.ImageExecutor` explicitly maps frozen task input.

Config capability adds optional `paramSchema`, `constraints:{max_prompt_characters,max_image_bytes,max_pixels}` and `reference_roles`. Catalog identity/model/knowledge fields are generated by gateway and cannot be supplied in saved configuration. Restricted schema supports only root object/properties/required/additionalProperties=false, primitive string/number/integer/boolean, enum, minimum/maximum, minLength/maxLength. Unknown fields/keywords, explicit null keywords, nested params, type mismatches, nonfinite numbers, invalid bounds and widened adapter constraints reject. Config can remove fields or tighten existing types/enums/bounds. Integer count belongs at the request level, never in params.

Property keywords are case-sensitive, including inside the properties map; a case variant rejects even when the standard keyword is also present. Numeric integrality, enum equality and bound comparisons use exact decimal rationals, including schema bounds. Integral spellings `1`, `1.0`, `1e0` validate equivalently and task execution preserves their original JSON number tokens. Numeric representations over 1,024 characters or exponents outside -1,024..1,024 reject before rational allocation; actual adapter ranges remain enforced.

The adapter conservatively supports standard sizes 1024x1024/1536x1024/1024x1536, quality auto/low/medium/high, background auto/opaque/transparent, moderation auto/low, output_format png/jpeg, JPEG output_compression 0..100 and safety_identifier ≤128 characters. Source/result media is fully decoded PNG/JPEG ≤20 MiB and ≤16,777,216 pixels by default; configured limits may tighten. WebP, model-specific extended sizes/qualities, input_fidelity, legacy response_format and streaming explicitly reject. This avoids adding a codec dependency or guessing model support; custom Base URL/model remains a compatibility declaration. Prompt is unchanged, ≤65,536 UTF-8 bytes AND ≤32,000 Unicode characters or tighter configured limit (the original 64,000-character source differs).

`Estimate(GenerationInput,FrozenProvider)` returns `quote_id,currency,estimated_amount,minor_unit_scale,estimated_credits,expires_at,input_hash,source,version`. Amount is integer minor units; credits use arbitrary-precision rational multiplication with upward rounding and reject int64 overflow. Pricing `effective_at` remains **Unix seconds**; quote expiration is **Unix milliseconds**, five minutes from estimation. Unknown or future pricing blocks; explicit zero is legal. Canonical hash binds project, exact prompt, all params, output, count, ordered asset identity/content/role, full provider config, credential version and adapter version. `SaveQuote` atomically persists `price_quotes.frozen_json={quote,input,provider}` plus complete public request receipt. `ReplayQuote` returns the saved estimate even when expired or configuration changed; it does not authorize execution. `QuoteBinding` is project-scoped and internal only. A supplied quote_id must be fresh and match current canonical intent at admission; absent quote still creates only billing wait. No price or credits are accepted from a client.

`tasks.Input` now freezes Count, QuoteID, Assets; Provider/OriginalWire remain trusted Go-only. `task_outputs.source_json.collection_key` is reserved in the task transaction and copied across retries, alongside existing resource/version/ordinal binding. `providers.GenerateImage` returns `ProviderResult{data:[{b64_json?|url?}]}` without downloading or creating outputs. `tasks.ImageExecutor` saves this immutable evidence, including an empty data array, before collection; it currently returns delivery/RESULT_AWAITING_DELIVERY and cannot complete outputs. 401/429 and definite non-408 4xx reject; 5xx, 408, transport/header/body interruption are unknown, never automatically retried. Sanitized bounded upstream `{error:{code,message}}` diagnostics preserve HTTP status, remove frozen credentials/organization/project and Bearer tokens, strip controls; arbitrary/malformed/oversized error bodies become a generic status. Task unknown state retains only this trusted sanitized upstream evidence.

Generation uses the same guarded transport as probes/downloads with a bounded 10-minute total/header budget; task lifecycle cancellation applies. Probe remains 20s total/15s headers and only GET models. Downloads remain 20s, unauthenticated, no redirects/proxies, DNS addresses checked before numeric-IP dialing. `CollectImages(ctx,project,ProviderResult,frozenProvider,collectionKeys)` rejects count mismatch before file creation, strictly decodes base64 or guards URL/MIME/size/media, then uses existing Workspace.Stage journals under `image-<collection_key>`. Stage persists the concrete staging path before filesystem writes. Successful upload replay validates the existing staged bytes against the original journal and does not re-download expired URLs. A download can be repeated after failure, never the generation POST. Returned staging paths are internal; no task output asset/version is fabricated.

Mask body: `{requestId,asset_id,bounds:[{x,y,width,height}]}`; bounds are M3.4 source-pixel rectangles, finite and within the source image. The service persists the complete intent, generates opaque outside/transparent inside with floor/ceil pixel coverage, and registers PNG through the existing Stage→Commit path. Result `{asset_id,asset}` is scoped managed media. A nonempty `reference_roles` fixes the complete positional contract; only image/mask, at most one mask after the first image. Mask input requires PNG alpha, same source dimensions, and a PNG first image; no unconditional “last ref is mask”. Mask guidance does not guarantee unchanged content outside its region.

Mask replay checks original intent first, then the final receipt, existing Commit input, and durable uploaded/completed Stage checkpoint before reading source media. A completed Commit can recreate a missing final mask receipt from its original asset even if the source was removed or replaced; changed original geometry still conflicts. M4.5 collection must likewise resume an existing Delivery journal BEFORE calling CollectImages again, because Delivery may already have moved its staging files.

The `generate.mask.intent` receipt atomically freezes private server-generated UUIDs as `{upload,commit}`. Only these owned child IDs can be resumed; ordinary file journals under caller-derived `mask-upload-X`/`mask-commit-X` cannot be adopted. For the unreleased legacy `{}` intent format, an existing final mask receipt remains replayable, but an incomplete intent rejects with new-requestId guidance: ownership of old derived journals is ambiguous and is never inferred. No automatic migration or regeneration of those incomplete legacy intents occurs.

The owned embedded card resides at `gateway/internal/providers/knowledge/providers/openai-images.md`, logical allowlist path `knowledge/providers/openai-images.md`; the single HTTP route reads that content, never an arbitrary filesystem path. Future design_read should map this exact root/card, not original private hub files. Existing ProviderSettings preserves config objects but has no advanced schema/reference-role authoring UI yet; M4.5 must expose those controls and wire editor annotations→mask API→ordered references. Backend mask tests are not an end-to-end editor claim.

M4.4 must bind server quote_id/input_hash/expiry to the human question receipt and atomically approve the exact frozen task revision/attempt before Run. There is no public approval bypass here. M4.5 must install Collect on the same Executor, consume saved response and reserved collection keys, bind the resulting staging paths to the pre-reserved task target/resource/version/ordinal via existing Delivery journal, and update outputs before completion. Do not resolve names again or re-POST generation on a collection error. Current native tests use httptest fixtures and test-only SQL approved state; no real provider acceptance, paid generation, billing UI or delivery/UI success is claimed.

## M4.4 structured human confirmation and billing gate

`POST /api/question?project_id=P` accepts exactly `{requestId,kind,session_id,task_id?,task_revision?,questions?}`. Ordinary `choice` and `storyboard` questions use ordered `questions:[{header,question,options:[{label,description}],multiple?,custom?}]`; answers are ordered `string[][]` containing option labels, never indexes. Choice permits explicitly enabled custom text; storyboard and billing cannot use free text for authorization. There are 1–8 prompts and at most 20 distinct options per prompt. Text is rendered as React text, never HTML or commands. These durable local kinds and state fields are a porting contract, not a native OpenCode `questionType` field.

Production image admission creates the exact frozen quote and a billing question in the same SQL transaction as task admission; an explicit existing valid quote is retained. Tasks admitted without `quote_id` therefore have a payable card. A new provider-generation attempt from `tasks.resolve retry` gets a fresh quote and card; saved-result collection retry retains the existing attempt and has no new charge. Unknown pricing is rejected. Explicit zero amounts still require the same human card.

For billing, public question creation is only explicit renewal: `{requestId,kind:"billing",session_id,task_id,task_revision}`. The server constructs price, provider/model, impacts and the sole approval label `确认生成` from the frozen task. No supplied price/quote/source/actor/status is accepted. An unexpired pending billing card cannot be replaced. After expiration, an explicit renewal creates a new quote and question ID and marks the previous pending card superseded. It never carries forward an old approval. Changed provider configuration/credential prevents renewal of the old task; create a new generation intent. GET and reconnect never reprice.

`GET /api/question?project_id=P[&session_id=S]` returns `{items:Question[]}` including durable current and historical cards. A question exposes `id,project_id,session_id,task_id?,kind,revision,task_revision?,input_hash,state,questions`, and billing-only `quote,provider,model,impacts`. Quote is the existing public M4.3 quote projection; private frozen provider/config/credential version and provider response data are omitted. Reads project task-revision mismatch as `stale` and quote expiry as `expired`, with `reason`; underlying receipts remain immutable. History is retained/listed in memory for the local project; pagination/retention belongs to a later measured need.

`POST /api/question/:id/reply?project_id=P&session_id=S` and `/reject` accept `{requestId,answers,expected_revision}`. Rejection uses `answers:[]`. For tasks without a session, `session_id` may be empty; ordinary questions require a named session. Unknown fields (including actor/channel/session body flags), null answer arrays, duplicate labels, wrong cardinality, arbitrary billing text, wrong scope, stale revision/state, or expired pricing cannot grant approval. Receipt is `{id,question_id,requestId,state,answers,revision,task_id?}`. Actor is always `human` in the receipt, set by this HTTP entry; no MCP approval/reply tool exists. This is the local app's human entry boundary, not an identity claim about Origin against a malicious local process. M5 must add its scoped runtime capability/turn-token boundary and must not register these human methods as agent tools.

Reply uses the complete original strict JSON body plus trusted route session/action as its `WriteRequest` identity. Within one SQL transaction it validates question/project/session/revision, frozen task/current attempt, input hash, registered reference metadata, quote ID/version/expiry and current provider/config/credential identity, then records question state, human receipt, task eligibility and events. All SQL uses the supplied transaction; no network/file/secret I/O occurs inside it. Native reference bytes are independently revalidated by the existing image executor before its POST. Rejection cancels a billing-wait task. An ordinary choice whose option happens to say `确认生成` does not approve billing.

The original committed reply is replayed before current mutable validation. Its exact original approved intent may be explicitly resumed after a crash between receipt commit and worker scheduling; `Run` atomically claims only the current approved, unsent attempt. Submitted/unknown/result-bearing attempts are never regenerated by replay. Startup recovery still does not submit. Dispatch registers its work before returning so shutdown waits for scheduled workers. Frozen original approval is never replaced with a newly estimated price on response loss.

The shared React card persists its exact pending body/path/key before POST, blocks writes if storage is unavailable/corrupt/mismatched, disables duplicate clicks synchronously, and preserves approved original intents for explicit unsent-work recovery. Late completion from an unmounted card cannot clear a newer mounted intent. A resolved event or GET can reconcile without another generation request. Cards are keyed to project; the existing canvas/editor are not remounted on SSE updates.

`question_asked` and `question_resolved` use the existing durable `/api/events` envelope. Non-task events validate the owning project and use nullable task_id. Frontend has one managed fetch SSE transport for the active project, then dispatches by optional session; its durable project cursor advances even for filtered-out sessions. It parses chunked UTF-8/CRLF, ignores duplicate/older IDs and wrong-project events, reconnects with `Last-Event-ID` and backoff, closes failed readers, and protects the new project's cursor from late responses from an aborted connection. Reconnect only reads: it never repeats control writes. M5 upstream resolved events without answers cannot manufacture approval; persist upstream/local question mapping and reconcile upstream send state separately.

M4.4 local HTTP/WebView fixtures establish billing gate/receipt/reconnect behavior only. Real paid provider/credential/generation acceptance remains unexecuted; M4.5 still owns final image Delivery and product generation controls.

## M4.5 image collection, shared Delivery and task UI

The installed image executor now saves the accepted provider response before collection. Collection uses the frozen private collection identities and registers its paths on the original attempt outputs. A stable `task-<private collection key>` Delivery journal owns the complete ordered batch. Existing Delivery journals are recovered before collection is attempted, because their staging files may already be archived. HTTP and future tool delivery use the same service; callers cannot select another task's journal identity or change its frozen output declaration.

Shared Delivery resolves opaque task-output keys in the owning project/current attempt, consumes the reserved Composition/Resource/Version/ordinal, and revalidates authorization in its final SQL transaction after file I/O. It updates output rows and invokes `tasks.CompleteDelivery` in that transaction, so task status/revision/events remain owned by tasks. Cancellation winning before that transaction produces no Version; completion winning makes later cancellation conflict. Saved malformed/count-invalid media reports a safe nonretryable delivery problem. Download failures offer explicit same-attempt collection retry and never resend generation. Shutdown interrupts owned network work; OpenAI Images has no implemented upstream cancel endpoint, so local cancel does not imply a provider cancellation or refund, and an already executing response/download may finish as retained evidence.

`GET /api/tasks?project_id=P[&session_id=S]&offset=0&limit=50` returns the common `{items,coverage}` page; default limit50, maximum200, canonical nonnegative offset. Unknown/repeated query names and noncanonical numbers reject. Task views additionally expose `request_id` and reserved output `resource_id,composition_id,ordinal` for matching persisted Canvas intent; no provider result URLs or credentials are exposed. TaskCenter reads all pages and the existing shared project SSE subscriber. It renders only server-advertised retry/cancel actions and safe problem/upstream evidence.

Late results of cancelled tasks require a separate server-created structured question with `kind:"delivery"`, `task_id`, `task_revision`, and `input_hash:"<task-id>:<attempt>"`. Public question creation cannot create this kind. The existing human `/api/question/:id/reply` accepts the sole label `采用已返回结果`; `/reject` declines adoption. The transaction binds the current cancelled attempt and real saved result to the human receipt, then permits only collection of that original attempt. Task status remains cancelled, even after delivery. Duplicate human receipt replay can resume pending collection but cannot resubmit generation or allocate another Version. Public `/api/tasks/:id/resolve` remains `retry|cancel`; no agent approval route exists. M5 must preserve the human question boundary.

The composer persists its complete original request before any write, optionally persists Canvas `pendingGeneration:{requestId,outputKey}` first, and requires explicit replay after uncertain results. Empty/malformed/foreign persisted intents block writes. Completed outputs replace only matching pending/task-output references through the existing Canvas CAS journal, retaining position and explicit selected versions. Editor repair passes the actual selected source Version into a fixed `new_version` target and derives its existing Composition/type; original image and source-sized registered PNG mask remain ordered references. An unversioned input requires an explicit base selection. Only connections declaring edit with exact `reference_roles:["image","mask"]` can submit this repair. Advanced provider authoring accepts the known `paramSchema`, `constraints`, and `reference_roles` fields. Expired billing cards still permit explicit rejection while approval remains disabled.

Native production tests use a local HTTP provider fixture with real WebView2, SQLite, managed files, billing buttons, editor mask pixels, shared Delivery, Canvas display and process restart. They establish the engineering path, not actual remote generation compatibility or billing: real credential-backed text-to-image and annotated repair calls remain **UNEXECUTED**.

Original generation recovery checks persisted task admission before preparing Canvas nodes, so converted, moved, selected or deleted nodes never force recreation of an admitted generation's original Canvas command. Blocked task-output Delivery journals keep both their saved source and archive paths protected until adoption/recovery; ordinary blocked file journals do not gain this reservation. Applying a reserved Version ordinal retains a prior current Version with a newer ordinal in the same transaction, while preserving forward advancement and the ordinal highwater.

The shared Delivery output validator also runs before task estimation/admission, including the 256-character Composition type-label limit. Task billing projections receive `estimated_credits` from the same authoritative quote used by the card, without advancing the bound revision. Interrupted result-body transfers remain explicit same-attempt download retries; oversized or immutable invalid media does not. Frozen target names stay reserved for undelivered current outputs that can still be retried or adopted, including submitted local cancellations. Delivered targets, unsent cancellations and definitively unrecoverable attempts permit ordinary target edits again.

## M5.1 isolated LLM host and settings (porting additions)

The native shell owns both child handles. It prepares an application-only `agent-host/launch-*` directory, restricts the parent profile ACL to the current Windows account, starts gateway on 8001, completes the existing info/health handshake, then starts the bundled OpenCode 1.19.0 on 127.0.0.1:4096. Gateway stdio remains exactly `gateway.info`, `gateway.health`, `gateway.shutdown`. Gateway startup creates a random 32-byte Basic password and encrypts `{password,config,key}` with the existing DPAPI store into `launch.dpapi`; native consumes and removes it, including on startup failure. No secret is passed in argv, renderer state, HTTP GET, or logs. Secrets exist in the two trusted processes and the model child's private environment. This is process/config isolation, not an OS sandbox against the current user.

The model child uses `env_clear`, a System32-only PATH, owned HOME/USERPROFILE, APPDATA/LOCALAPPDATA, XDG roots, ProgramData and TEMP/TMP. Session data/state and cwd persist under `agent-host`; discovery/config/cache/home/temp roots are fresh per launch. Verified v1.19.0 flags disable project config, external skills, Claude discovery, default plugins, LSP download, model refresh, updater and file watching; pure mode and `npm_config_offline=true` prevent unapproved package discovery/network installation. No actual user/global/managed OpenCode configuration or auth is read. Builtin skills supplied by the executable may still exist; the skill tool remains denied.

`agent.BuildConfig(LLMConfig,MCPConnection) ([]byte,error)` is authoritative; `agent/opencode.json` is only a documentation baseline and must never be launched alone. It sets default deny plus the native question exception, disables plan/general/explore, and constrains build/title/summary/compaction. The runtime's automatically appended truncate-directory allow is suppressed by an exact native-path deny. Host readiness authenticates health/version and inspects final `/agent` permissions, rejecting any effective non-deny exception except build/question. No MCP server/tools are registered in M5.1, so `tools_ready` is always false. The future seam accepts only URL `http://127.0.0.1:8001/mcp`, the exact app Origin, a private token, and individual `design_design_*` names with no wildcard. M5.2 must supply names from the actual tools/list/prefix mapping, update readiness to validate those exact grants, and retain deny for every other tool; current tests of this seam are builder tests, not a real MCP roundtrip.

The pinned runtime performs raw `{env:...}` substitution before `{file:...}` expansion. User URL/model/key fields reject braces; arbitrary npm/plugin/header/config fields are not accepted. Builder-owned key and truncate-path env values are JSON-string-content escaped by native before substitution. This preserves Windows backslashes and quotes without allowing a second file expansion. Model-list metadata is not imported into runtime config.

`GET /api/agent/settings` returns `{config:null|{protocol,base_url,model_id,context,output},has_key}`. `POST /api/agent/settings` accepts `{requestId,action:"save",config,key?,remove_key?}` or exactly `{requestId,action:"probe"}`. Supported protocols are `openai-compatible` (Chat Completions), `openai` (Responses), and `anthropic` (Messages), all verified bundled SDKs. Base URLs reuse media endpoint validation; model/context/output limits are typed. Omitting key preserves the immutable DPAPI version; remove_key detaches it without deleting retained versions. Migration `000008_llm_settings` creates one support row; media providers and five core business tables are unchanged. GET never exposes a key/version. Request receipts retain a canonical input digest and nonsecret result, not the secret-bearing input. `GET /api/agent/settings/receipt?request_id=ID` recovers only the original completed receipt; same ID/different intent conflicts. Browser recovery stores the existing nonsecret pending representation under `moviedesign.llm.pending.v1`; replay requires the original key fingerprint when applicable. No automatic mutation retry.

Probe is an explicit free GET of the saved OpenAI-compatible model list via the existing guarded provider transport; Anthropic performs local validation only. Neither path creates a session/message or tests generation/tool compatibility. `GET /api/agent/status` returns `{host_ready,configured,tools_ready,active,pending_changes,message}`. Saved configuration and active launch snapshot are separate. Changes, including key rotation, take effect only on the next app launch; existing model runtime is never reconfigured underneath a turn. Host/model absence does not prevent the existing M4 workspace from operating. Status reports startup/port/version/config/permission failures without upstream bodies or credentials.

All new routes use the existing exact-Origin gateway middleware, method/query/body validation and no-store responses. Renderer CSP remains gateway-only; it never calls 4096 or receives Basic credentials. OpenCode itself has built-in localhost/Tauri/opencode.ai CORS allowances in [pinned cors.ts lines 9–17](https://github.com/anomalyco/opencode/blob/v1.19.0/packages/server/src/cors.ts#L9-L17); omitting `--cors` does **not** disable these upstream defaults. The app adds no CORS origins. Actual management requests with app Origin but without the private password return 401; authenticated gateway readiness succeeds. Basic authentication independently protects native management endpoints even though model tools are denied.

Port4096 is reserved before launch; an occupied listener is neither contacted nor stopped. A short bind handoff gap remains because the official CLI binds its own socket; per-launch Basic authentication prevents attaching to a different host. Normal app exit kills/waits only the owned model child then drains gateway. Forced OS termination is not a normal shutdown hook and can leave an orphan model process; no claim of job-object crash cleanup is made. Native validation uses only synthetic credentials and local HTTP fixtures, checks restart activation/receipt recovery and both owned children exiting. Actual LLM inference, native question conversations, MCP tools, 40-call accounting and chat remain unexecuted/future M5 tasks.

M5.1 I1 failure-isolation correction: native `Opencode::prepare` errors (directory, identity, or ACL setup) leave no model owner and no model launch. The shell clears inherited `MOVIEDESIGN_AGENT_BOOTSTRAP` and `MOVIEDESIGN_AGENT_PREPARE_FAILED`, then supplies either its owned bootstrap path or the fixed nonsecret failure marker `1` to the gateway child. `StartAgentHost(dir,preparationFailed)` skips bootstrap/Host construction for preparation failure; `/api/agent/status` reads saved settings and returns a readable preparation error with host_ready/tools_ready=false and active=null, without probing4096. This private parent-to-child status handoff works even when agent-host cannot hold an error file. Existing gateway health and M4 reads remain available; ACL/auth checks are never bypassed to make the model start. No stdio RPC or renderer credential field is added.


## M5.2: official MCP, owned turns and execution budget

`/mcp` uses official Go SDK v1.4.1 Streamable HTTP with exact app Origin and a dedicated random Bearer token, while retaining SDK CrossOriginProtection/localhost protection. The token travels only through the M5.1 DPAPI launch package to the owned native host; browser code receives neither token nor management password. Body limit: 2 MiB. Initialize/tools/list do not consume tool execution budget.

Current exact catalog (12 tools), prefixed by `design_` in native OpenCode 1.19.0, e.g. `design_design_execute`. Every permission is exact-name; no native file, command or network tool is granted.

| Tools | Shared implementation and boundary |
| --- | --- |
| design_capability_list / design_capability_get | Enabled image capabilities, coverage, schema and provider knowledge card |
| design_execute | generation.Admit -> tasks.Submit -> tasks.Wait; explicit output/requestId |
| design_deliver | Existing delivery journal, recovery and three-state business receipt |
| design_task_query / design_task_wait / design_task_resolve | Project-scoped task views, unified wait predicate, requestId/revision cancel; retry requires the human application route, no card approval |
| design_composition_search / design_composition_get / design_composition_edit | Existing Objects pagination, reads and metadata create/update/delete |
| design_resource_get / design_resource_edit | Existing Objects ownership and metadata edits; content revisions use delivery to create Versions |

Metadata edit `value` is a JSON object in the published MCP schema. The existing Objects decoder still validates its action-specific nested fields strictly and retains the full accepted mutation identity for replay. MCP delivery receives its own immutable trusted `agent` service instance; HTTP retains `user`. Both share the existing delivery lock and task-completion hook. An unknown delivery resumed through either entrypoint retains the first journal's actor and version identities.

Read/document/rules/search/text/video tools remain unadvertised until M5.5/M6/M7 connect their real services. MCP uses capabilityId/quoteId while HTTP retains capability_id/quote_id. MCP accepts no project/session IDs, provider snapshot or frozen assets. Shared generation.Service.Admit preserves the existing HTTP ordered flow: early full-wire replay, null checks, capability/reference resolution, output admission, quote binding, submit. HTTP retains tasks.submit; MCP uses tasks.submit.mcp, preserving the complete public wire before field mapping and before mutable provider lookup.

agent.Turns is a gateway-controller-only API. Begin/BeginQueued binds managed project/session/nativeSession and generates turn/epoch. MCP initialize captures that epoch; an old transport cannot be rebound to another project. BeginQueued waits for the single active turn; M5.3 must persist the user message before waiting. End requires native idle, then blocks new tools, drains existing executions, disconnects native MCP/closes SDK sessions, and only then releases the turn. Startup marks old active turns interrupted, preserving their audit records and never replaying inference.

Migration 000009 adds chat_sessions/chat_messages/chat_turns/agent_tool_calls/project_rules without changing the five core tables. agent_tool_calls has unique (turn_id,call_id). Admission and count update are one transaction; call 41 is refused before business mutation and invokes abort on the owned native session. Tool, full input, requestId, admission and result are persisted. A repeated write requestId can replay business state but cannot bypass the execution budget.

The [pinned catalog implementation](https://raw.githubusercontent.com/anomalyco/opencode/v1.19.0/packages/opencode/src/mcp/catalog.ts) does not forward native toolCallId. Actual native trace: gateway/internal/mcp/testdata/opencode-1.19.0-trace.json. Every MCP tools/call therefore receives a new gateway execution ID and is conservatively counted; JSON-RPC request ID is not native toolCallId. UI tool-event replay does not execute MCP and does not increment budget. Native question has a separate request ID and tool.callID. GateQuestion admits the verified native call before publishing an ordinary choice card; replay retains the original card/admission. Over-limit questions are rejected without authorization receipts. This seam cannot create billing/storyboard approval.

Execute/wait passes through billing waiting_user and settles on ordinary waiting_user, unknown or terminal status. Unknown remains structured business output, never MCP isError/network failure. Disconnect stops waiting only, leaving the durable task intact. Native MCP timeout is explicitly 24 hours; after this wait ceiling query the original task rather than regenerate. ToolsReady requires a live observed tools/list handshake, native /mcp connected status and exact effective /agent grants. Ready does not mean inference is enabled.

**M5.3 inference gate:** no production session/message entrypoint is exposed by M5.2. Fixed 1.19.0 [LLM SDK invocation](https://raw.githubusercontent.com/anomalyco/opencode/v1.19.0/packages/opencode/src/session/llm.ts) defaults SDK retries to zero, but [processor](https://raw.githubusercontent.com/anomalyco/opencode/v1.19.0/packages/opencode/src/session/processor.ts) uses an outer [five-retry policy](https://raw.githubusercontent.com/anomalyco/opencode/v1.19.0/packages/opencode/src/session/retry.ts). Its [provider fetch](https://raw.githubusercontent.com/anomalyco/opencode/v1.19.0/packages/opencode/src/provider/provider.ts) lacks the app DNS/redirect controls. Before messages are enabled, a private guarded inference boundary must enforce frozen endpoint/credentials, DNS/address/redirect policy and one upstream submission with no resend on unknown acceptance. maxRetries=0 alone is insufficient. The native fixture accesses only owned loopback model responses: two actual model HTTP requests (tool turn and final text), no real credentials/payment, and no fault/retry/stream-loss inference acceptance claim. Real LLM inference remains UNEXECUTED.

### M5.3 durable chat and private inference boundary

All browser traffic still goes to gateway with the exact configured Origin. Public chat routes use `project_id` query scope. Every write requires `requestId`; full original canonical wire input, including document/references/annotations and optional-field presence, is the receipt identity. The session and message IDs are gateway-generated. Native session/message/password/relay configuration cannot be submitted by a browser.

| Method/path | Input | Response |
| --- | --- | --- |
| GET `/api/chat/sessions` | `project_id`, optional canonical `offset` (default 0), `limit` (default 50, 1..100) | `{items:[{id,project_id,title,state,created_at}],coverage:{total,next_offset,exhausted}}` |
| POST `/api/chat/sessions` | `{requestId,title}` | 200 local session receipt; native session is created lazily before its first message |
| GET `/api/chat/sessions/:id/messages` | same pagination/project scope | `{items:[{id,session_id,requestId,role,content,state,error?,created_at}],coverage}` |
| POST `/api/chat/sessions/:id/messages` | `{requestId,document:{type:"doc",...},text,references:[assetID,...],annotations:[...]}` | 202 durable message; immediate definite native rejection returns a sanitized HTTP error |
| POST `/api/chat/sessions/:id/cancel` | `{requestId,message_id?}` | 200 `{status:"cancelling"}` receipt; read message/event state for completion |

Without `message_id`, cancel withdraws all queued messages in that session and explicitly aborts its active owned native session. With `message_id`, it withdraws that one **queued** message only; submitting/accepted/unknown messages cannot be edited or individually withdrawn. Queue editing is withdraw old message then send revised content with a new requestId. It never cancels separately admitted media tasks; use TaskCenter cancellation for those tasks. Every cancelled queue entry gets exactly one durable `chat_done` event. A cancel during an unresolved prompt POST aborts immediately, retains the epoch, and aborts/checks idle again after definite acceptance settles. Indeterminate acceptance does not release the epoch based on a temporarily absent native status.

Messages are persisted before native POST and before acquiring the application-wide turn. The ten-second grace timer ends only the frontend wait; the native request and event reconciliation belong to gateway lifecycle. Request cancellation does not cancel native inference. Native operation safety ceilings are ten minutes for prompt acceptance/model response and ten seconds for other private operations. An uncertain create/prompt is never automatically POSTed again. A session-creation uncertainty remains visibly unknown; restart checks known native message snapshots without resubmission. Already-sent states become unknown on startup; known-unsent queued messages remain queued, blocked behind unresolved prior work. Explicit session cancellation can settle an interrupted prior-runtime turn. Native sessions have an explicit `MovieDesign <localID>` title, avoiding an independent title-generation request.

Normalized global events preserve the existing `{id,project_id,session_id,task_id?,type,payload,created_at}` envelope/cursor. `type:"chat_message"` payload is `{session_id,message_id,part_id?,call_id?,step,type,part_type,content_delta?,content?,tool?,state?,tool_input?,tool_output?}`. Types include `content_delta`, `content_replace`, `tool_call`, `tool_result`; part_type distinguishes text/reasoning/tool. `type:"chat_done"` payload is `{session_id,message_id,done:true,status:"done"|"error"|"unknown"|"cancelled",error?}`. Unknown supplier acceptance carries a sanitized error, never fabricated model success. One terminal per local user message. Tool completion, question wait, or an event-stream gap is not chat completion. Native `session.error` is sanitized; ordinary message completion/error is matched by native parent message ID. Per-part authoritative snapshots preserve repeated text and UTF-8; raw native deltas without durable offsets are read-invalidations, not deduplication keys. Reconnection queries messages/questions and never resends prompts. Read history contains normalized assistant parts, not raw native events.

Source comparison: the recovered plugin SDK `dist/types.d.ts:652,675` has camelCase `ChatMessageEnvelope{sessionId,kind,text?,raw}` and `ChatDonePayload{sessionId,taskId,status:success|failed|cancelled,errorMessage?}`. That plugin surface is **not** copied into the gateway. User specification §8 and the existing global SSE protocol govern the explicit normalized snake_case adaptation above. No plugin raw event escape hatch is exposed.

Native questions are admitted by the existing `Turns.GateQuestion` before card publication and counted in the same exact 40-call budget as MCP. The private mapping and send state survive restart. Only the existing question service's committed `actor=human` receipt can be sent to native `/question/:id/reply` as `{answers:string[][]}` or to `/reject`. No question reply is a session message or an MCP tool. Billing/delivery hooks are unchanged. GET question cards add optional `native_send_state` (`pending`, `sending`, `sent`, `unknown`). Unknown/non-idempotent native replies are queried, never automatically resent; a missing memory-only native question does not manufacture a successful human answer. Startup converts interrupted `sending` to `unknown`.

The native launch now receives only a fresh private relay token and gateway inference URL. Original endpoint/model and supplier key remain frozen in gateway memory/DPAPI credential storage. `/internal/inference/{chat/completions|responses|messages}` requires exact Origin, private token, expected protocol operation/model, and the current owned native session header. It is not a public proxy. Only known SDK POST operations are accepted; no arbitrary URL/header/script transformations. Gateway strips native headers, supplies the frozen supplier credential, and uses the same guarded Go transport as providers: no proxy, no redirects, checked DNS answers dialed as numeric IP, no metadata/admin ports, no automatic HTTP retry.

Each admitted model request is recorded before forwarding. A repeated fingerprint or any prior forwarding/unknown result is refused with sanitized nonretryable HTTP 400. A subsequent request in the same turn must contain fresh result IDs for exactly the tools from the immediately preceding complete response; this includes native question results and MCP results. A plain text step admits only one supplier request. Supplier 5xx, redirect, broken/truncated stream or persistence failure causes owned native abort and keeps uncertain evidence; it never forwards again. Fixed native outer retries therefore cannot trigger a second billable POST. `maxRetries:0` is not the enforcement mechanism.

Deliberate ceiling: one application turn and one model response at a time; complete supplier SSE is buffered up to 32 MiB/ten minutes before native delivery. This adds first-token latency and does not claim live supplier-token streaming. The upgrade path is a durable streaming protocol that preserves pre-effect tool admission and uncertain-response identities. Existing configured context/output limits remain intact; crossing the buffer/time ceiling returns an explicit failure/unknown, never truncated success. M5.5 text generation must reuse this guarded transport with a separate tools-disabled scope.

Migration `000010_chat_bridge` adds only support state for dispatch, snapshots, native questions and inference admissions plus session title/state. Previously committed migrations and five core tables remain unchanged. Project deletion with chat history returns the existing referenced-object 409. Pipeline migration moves to 000011.

Verification evidence: actual pinned 1.19.0 owned-loopback fault trace `gateway/internal/agent/testdata/opencode-1.19.0-guarded-trace.json` shows supplier count one for 5xx/redirect/disconnect, zero requests to redirect target, and count two for a native question followed by committed human reply. The existing actual parallel MCP+question fixture now uses the production private bootstrap/relay and still completes two legitimate steps/budget three. OpenAI Responses and Anthropic operations/credentials/completion/result linkage have Go HTTP fixtures; their actual native and real-provider inference are UNEXECUTED. No paid provider or original application credentials were used. TipTap UI and the final M5.5 tool matrix remain separate tasks.

M5.3 recovery: a public `chat_done` does not release the owned epoch until native idle, tool drain, native MCP disconnect and epoch close succeed. Private cleanup failures retain the worker/scope and are retried without replaying prompts or inference. Cancellation after a lost prompt response continues reading snapshots; only a terminal matching the persisted native session and parent can resolve unknown acceptance, followed by owned abort/idle/drain. An absent native status alone cannot resolve it. Interrupted/closed native question mappings are reconciled without GateQuestion or receipt sends. Missing upstream memory sets `native_send_state=unknown`; an unanswered card becomes `superseded` with incremented revision and a `question_resolved` update, so stale new replies fail. Existing human receipts and their exact request replay remain audit evidence and are never resent by recovery.

If a native question card was published before its mapping was linked, recovery reads the completed `question.ask` receipt under the original project/native requestId and verifies both receipt and card session/project/request identity. Linking that existing card and retiring it commit atomically. Recovery does not create a replacement. Already unknown mappings linked to nonpending cards are excluded from subsequent recovery work; unchanged unresolved mappings do not notify subscribers.

## M5.5: complete tools, document text and Canvas handoff

The current official SDK catalog contains **18** `design_*` tools plus the native `question` permission (not an MCP tool). The six additions are `design_read`, `design_analyse_media`, `design_document_write`, `design_project_rules`, `design_voice_search`, and `design_image_search`. Existing project/epoch checks, exact Origin/private token, strict schemas, persistent execution accounting and 40-call limit apply to all 18. No native bash/read/write/edit/todowrite, plugin or arbitrary provider protocol is granted.

| Tool | Accepted contract and explicit source adaptation |
| --- | --- |
| `design_read` | Exactly one `versionId` or managed `file_path`; optional zero-based `offset`, `limit` (default 200, 1..2000), complete line `coverage` and `sourceBytes`. md/txt/srt/ass UTF-8 ≤1 MiB; two allowlisted embedded provider knowledge paths. Unlike original C60714..60755, no 1-based offsets, PDF/Word, pdf_page or mutable contentRevision; no host paths. |
| `design_analyse_media` | `{references:[realAssetID,...],type:"metadata"}`, 1..16 ordered references. Project-active asset resolution; image dimensions via Go stdlib; audio/video physical metadata via pinned ffprobe. Original C58840 semantic/both modes are explicitly refused. No subjective quality review or inferred media content. |
| `design_document_write` | `{requestId,mode:"create"|"replace"|"patch",format:"md"|"txt"|"srt"|"ass",content?,edits?,output}`. Create/replace require explicitly present complete content. Patch uses `{exact,occurrence,replacement}` (positive occurrence, all anchors resolved on original content, overlap/missing anchor aborts). Exactly one output resource; revision declares `new_version`/`baseVersionId` in standard output. Original C59115..59180 mutable Version/baseRevision/disposition are deliberately replaced by immutable new Version + normal Delivery, preserving the Resource and source lineage. |
| `design_project_rules` | `read` returns `{content,revision}` with opaque completed receipt ID or null. `save` requires requestId, full UTF-8 Markdown ≤16 KiB and `expectedRevision` (null for missing). It only proposes a card bound to exact content/hash/revision/project/session. Existing human question reply commits CAS and human receipt atomically. Original C60309..60345 text-only persistence authorization is overridden by the approved human structured-card contract. |
| `design_image_search` | `{query,offset?,limit?}` (default 30, max100), project-local active image assets, real assetId/name/source/MIME, coverage and `remoteSearched:false`. Original C58645..58705 cloud query/download/composition parameters are unsupported. No Internet search or synthetic results. |
| `design_voice_search` | Same local search/pagination. User imports managed `voice-catalog.json` containing an array of `{voice_id,provider,description,language,gender?,sample_audio?}`. Results retain real registered provider ID and catalogAssetId/source. No catalog means explicit empty coverage. Original C61413..61465 ordered multi-request cloud voice selection is a documented local-catalog adaptation; no third generator, voice cloning or guessed IDs. |

`POST /api/generate/text?project_id=P` now uses the same strict Request DTO, 202 admission, frozen full-wire receipt, billing, task events, collection and Delivery as image generation. Capability is `text:configured-llm`, also discoverable via HTTP/MCP capability list/get. HTTP uses `capability_id`, MCP `capabilityId`; MCP project/session remain controller-owned. Exactly one declared output; params `{}` and references `[]` only. Model/temperature/arbitrary options/paths/tool grants are unsupported. The running frozen LLM settings and DPAPI credential version are used; saved changes still activate on restart. `generation.Service.TextProvider` and `tasks.InstallText` inject Host execution/query without a tasks→agent import cycle.

Optional LLM `pricing` uses the existing Pricing shape with `unit:"token"`; the UI explicitly asks for an upper per-token input/output price. Unknown pricing is refused, never treated as free. Quote `basis` states context-token ceiling × declared upper token price and configured output ceiling; this is a conservative declared-price estimate, **not measured supplier billing**. Input UTF-8 bytes are conservatively limited to min(65536,context-output); relay checks serialized request bytes against context-output, including native overhead, and actual max_tokens/max_completion_tokens/max_output_tokens ≤ output. Byte ceilings are intentionally conservative and can reject prompts near the advertised limit; they are not an exact tokenizer contract. Context must exceed output. Provider-side non-token charges and a dishonest price declaration are outside this estimate.

Text execution has its own native `text` agent/session with deny-all permissions and actual tools array empty, including question/MCP. It does not acquire or replace the media-agent turn. One text task executes; other approved text tasks wait. The shared private inference relay registers a native text session against a durable text request/attempt, admits at most one supplier POST, enforces completed no-tool response, and never falls back to another scope. Cancellation/scope retirement is rechecked before supplier admission. Cleanup failures retain the owned native session and block subsequent session creation until abort/idle reconciliation succeeds. An uncertain session creation with no ID requires app restart; no prompt is resent. Unknown/restart queries are read-only and never create a model session/message. Native prompt acceptance remains independently pending beyond the frontend grace window. Empty completed text is an explicit failure. Cancellation cannot undo supplier billing. Migration `000011_text_inference` adds only task-owned request/inference support tables with attempt FKs; future M6 pipeline starts at 000012.

Trusted admission freezes internal `tasks.Input.actor` in durable JSON: MCP → agent, HTTP → user. Public DTOs do not accept this field. Collection selects the corresponding immutable Delivery actor while carrying CompleteTask/Notify hooks; unknown journal replay preserves the first actor. Delivery now invokes `store.Canvas.PlaceDelivery` within its existing final SQL transaction through `Canvas.ExecuteTx`. It first matches the exact composer `(requestId,outputKey)` placeholder and preserves that node/layout regardless of Resource current selection. Only an unmatched single output at the current Version may reuse an ordinary Resource node; older outputs and same-Resource candidate batches receive separate deterministic `delivery-<versionId>` nodes at the rightmost edge. Multiple candidate Versions retain distinct node identities, and unrelated selected nodes are unchanged. A completed receipt replay never re-places a node deleted or moved afterward. Frontend singleton SSE events trigger read-only Canvas refresh, with monotonic revision, pending-command and gesture guards; no duplicate frontend command creates agent nodes.

Document writes freeze complete bytes, actor, output and deterministic staging identity in the existing write_requests journal before Stage. Stage is a recoverable owned operation, then normal Delivery reserves and archives the Version. On restart the original plan/stage/Delivery receipt resumes, without regenerating content or choosing another Version. Project deletion protects human rules/questions and task/text history using the existing referenced-object conflict.

Canonical `agent/media-agent.md` retains original 49 logical lines (branding only), followed by explicit MovieDesign port constraints. Mapping to original source: L3 identity/responsibility; L7 stable Composition/Resource/Version; L9 independent object boundaries/coappearance; L11 direct membership/reuse/role; L13 multiple members/reused types; L15 revision vs independent/derived content; L17 attributes vs version content; L21 current basis; L23 authorized judgment; L25 reviewable steps; L27 dependency-only pauses/no inferred permission; L29 steering; L31 explicit long-term rules; L35 backward planning; L37 reuse known evidence; L39 no scope expansion; L41 objective inputs/no self-regeneration; L43 selected Skill methodology until completion/real assets; L47 success vs completion vs acceptance; L49 natural language. `scripts/build-gateway.ps1` generates the native Go prompt constant, and an equality check prevents drift. `BuildConfig` installs this prompt on the actual native build agent; future M6 skills are not falsely claimed loaded in M5.5.

## M6.3: immutable creative artifacts and human approval

This is an explicit MovieDesign typed port addition. The existing 18 design tools and native `question` permission are unchanged; there is no approve MCP tool. Ordinary native/choice/storyboard questions and plain text containing approval words never authorize creative execution.

`design_document_write` / `POST /api/documents/write` also accept `typedDocument:{kind:"intent"|"script",pipelineId,expectedRevision,content,sourceVersionId?}`. Complete nonempty UTF-8 `content` is the semantic artifact (≤1 MiB, bounded further by the existing request-body limit); format is `md`, mode is create/replace, and each independent artifact uses its own Resource. Rendered artifact bytes are `# <kind>\n\n<content>`. Replace uses the exact prior Version and per-gate expectedRevision. Optional outer Markdown must exactly equal these rendered bytes. Upstream shapes do not use the storyboard field. Existing storyboard wire remains unchanged; optional sourceVersionId may pin the expected upstream Version. No pricing, resolution, model or other provider execution fields enter these semantic document shapes.

An intent is a real document Version before any storyboard exists. Script save requires the current human-approved intent; storyboard save requires the current human-approved script and its still-approved intent. The shared document plan freezes the exact upstream Version in its existing transaction, and final Delivery/recovery checks that same Version and current approvals again in the supplied transaction. Semantic no-op returns the existing artifact only when both semantics and source dependency are unchanged. Changing the upstream artifact therefore requires a new downstream revision even if downstream text/semantic JSON is unchanged. Pipeline revision zero is only an identity reservation, never a fabricated storyboard row or approval. Previously persisted M6.2 storyboards remain readable; new writes/approval require real upstream artifacts.

A completed typed MCP write returns `{document:<normal Delivery receipt>,approvalProposal:"proposed"|"pending"|"blocked",question?,reason?}`. After successful document completion, the tool proposes a typed question under the actual trusted MCP session and original document requestId. This is a separate idempotent proposal step: a failed/unknown card proposal never undoes or disguises a completed document. Retrying the same document request replays its original receipt and proposes/replays the same original Version/card key; it never invents a replacement Version. A historical replay can contain the original frozen question snapshot; read `/api/question` for its current state. A changed current artifact cannot make a newly proposed old card valid. Generic document writes retain their existing receipt shape.

`GET /api/pipeline/artifact?project_id=P&versionId=V` returns immutable `{pipelineId,gate:"intent"|"script"|"storyboard",revision,documentVersionId,semanticHash}` for an exact project-owned Version. `POST /api/question?project_id=P` additionally accepts `{requestId,session_id,kind:"creative",creative:<that exact binding>}` without caller-supplied prompt, options, price, receipt, actor or approval state. The gateway validates the current artifact/dependencies and generates the card, including the full immutable rendered document preview. Existing Origin, strict JSON/query validation, request identity and question reply rules apply. Canvas's explicit proposal button uses the local question session `pipeline`; MCP proposals use the real owned chat session. This local manual session never represents a native OpenCode answer.

The existing human-only `POST /api/question/:id/reply?project_id=P&session_id=S` is the sole approval writer. The card offers `批准意图`, `批准脚本` or `批准分镜`, separately from billable generation. Within **one** existing `store.WriteRequest` SQL transaction, Reply checks pending question/CAS/session, validates exact answer, inserts the `question_receipts.actor=human` row, then `pipeline.ApproveStoryboard(ctx,tx,HumanReceipt)` checks project/pipeline/gate/revision/Version/hash/current upstream dependency and actual receipt-to-question binding before inserting approval. Any failure rolls back card state, receipt and approval. This adapts the conceptual no-Tx plan signature to the existing shared transaction interface. Duplicate original reply replays once; a new key against an already decided or superseded card fails. Native answered events are not human receipt proof.

Migration `000013_creative_approvals` adds immutable `creative_artifacts`, and replaces the M6.2 future-only approvals support table with a real Version/hash/receipt-bound table (no core table changes, old migration bytes unchanged). Any preexisting M6.2 support approvals are retained as invalid audit records. Down-migration deliberately refuses to discard immutable upstream and human audit history. Human receipt IDs are unique approval bindings; they are inserted before their approval FK in the same transaction.

Invalidation direction is intent → script → storyboard → unsubmitted Prompt contracts; script changes preserve intent approval; storyboard changes preserve intent/script approval. Pending or approved affected cards become `superseded`, publish question state events and retain historical human receipts. Pending/ready prompt contracts become `invalidated`; submitted contracts and frozen in-flight facts are untouched. Existing `/api/pipeline/storyboard` now reports `approvalStatus:"approved"|"unapproved"` for the exact version, and historical revisions cannot appear currently approved. Provider-only execution parameter/pricing changes do not write creative revisions or invalidate semantic approval.

All Delivery paths and direct `/api/versions` creation share `store.CheckCreativeVersion`: untyped new Versions cannot replace a registered creative Resource. They must go through typed document save, preserving current semantic identity and dependencies. Unregistered ordinary documents/images retain their existing Version workflow. Generic text, patch, direct Delivery or direct Version CRUD is not a bypass around authoritative creative revision checks.

**M6.4/M6.5 handoff:** current generation/task DTOs do not yet accept pipeline-bound execution contracts. M6.3 invalidates actual pending/ready contract support rows and exposes current approval checks; it does not claim an implemented video-task admission gate. Future compiler/admission must bind the exact artifact/dependency and approval receipt, reject invalidated contracts before submission, and preserve submitted frozen inputs. UI explicitly says existing tasks retain their frozen revision and require an explicit user cancellation decision; no automatic cancellation, regeneration or paid call was added.

## M6.4: final_video_prompt.md, 7-point Preflight, and SQL lineage

This is an explicit MovieDesign typed port addition. The existing 18 design tools and native `question` permission remain unchanged.

1. **`final_video_prompt.md` Typed Document & Exact Byte Lock**:
   - `design_document_write` and `POST /api/documents/write?project_id=P` accept `typedDocument:{kind:"prompt",pipelineId,expectedRevision,content,sourceVersionId?,contract?}` with `format:"md"`, `mode:"create"|"replace"`, and `output.resources[0].name === "final_video_prompt.md"`.
   - Writing `kind:"prompt"` requires the pipeline's current storyboard revision to be human-approved (with valid upstream `intent` and `script` approvals).
   - `content` is preserved byte-for-byte (`Document.Render()` returns `d.Content` verbatim without trimming whitespace or normalizing `\r\n` vs `\n`). `PromptDigest(raw)` computes the exact UTF-8 byte `SHA-256` hex digest.
   - `prompt_revisions` (migration `000014_prompt_contracts`) stores immutable `(pipeline_id, revision)` rows bound to `storyboard_revision`, `document_version_id`, `resource_id`, `content`, `prompt_sha256`, `source_version_id` (approved storyboard version), and `approval_id`. `version_dependencies` records `(prompt_version_id, storyboard_version_id, 'creative_source')` and `(prompt_version_id, base_version_id, 'prompt_revision')`.
   - If `typedDocument.contract` (`{capabilityId,params?,references?,placeholders?,audio?}`) is provided on write, `SavePrompt` compiles and persists a `status='ready'` `prompt_contracts` row in the same Delivery transaction; otherwise it records a `status='pending'` contract row and sets `pipelines.prompt_status='pending'`.

2. **7-Point Preflight & Contract Compilation (`GET /api/pipeline/prompt`, `POST /api/pipeline/preflight`, `POST /api/pipeline/compile`)**:
   - `GET /api/pipeline/prompt?project_id=P&versionId=V` returns `PromptVersionView` (`pipelineId`, `promptRevision`, `currentPromptRevision`, `storyboardRevision`, `currentStoryboardRevision`, `documentVersionId`, `resourceId`, `sourceVersionId`, `approvalId`, `content`, `promptSha256`, `promptBytes`, `promptStatus`, and `contract?`).
   - `POST /api/pipeline/preflight?project_id=P` (read-only) and `POST /api/pipeline/compile?project_id=P` (idempotent via `store.WriteRequest`) run the 7-point Preflight check:
     1. `approval`: current `intent`, `script`, and `storyboard` human approvals are valid (`STALE_CREATIVE_APPROVAL` on failure).
     2. `prompt_bytes`: `final_video_prompt.md` exists for the current approved storyboard revision, its archived asset `xxh3` hash and byte size match `prompt_revisions.content`, and any caller-supplied `prompt` matches the locked bytes and `SHA-256` (`PROMPT_BYTES_MISMATCH` / `STALE_PROMPT_REVISION`).
     3. `placeholders`: zero unresolved template placeholders (`{{...}}`, `${...}`, `<TODO...>`, `[TODO...]`, `[待填...]`, `__TODO__`, or unpopulated `placeholders` map keys), while preserving normal cinematic bracket tags such as `[近景]` or `[00:00-00:05]` (`UNRESOLVED_PLACEHOLDER`).
     4. `capability`: target `image:<id>` or `video:<id>` provider is enabled, prompt byte length is within `max_prompt_bytes`, and all parameters match the capability's declared schema (`CAPABILITY_MISMATCH`).
     5. `references`: all reference asset IDs exist and are `active` in the project, count is within `max_references`, media kind matches the capability, and exact order + `full_hash` + `version_id` are frozen (`INVALID_REFERENCE_ASSET`).
     6. `duration_aspect`: for `video:*`, `duration` equals `round(timeline.durationMs / 1000)` and is supported by `capability.duration_seconds`, and `size` / `aspect_ratio` are consistent (`DURATION_ASPECT_MISMATCH`).
     7. `audio`: machine-readable audio requirements derived from `timeline.audioIntent` and optional `audio` override (`narration`, `music`, `sfx`, `mode`). When oral narration or music is required (e.g., 15s narration + music), silent capabilities (`capability.audio == false`), `generate_audio == false`, or incompatible `audio_mode` (such as `sfx` or `none`) are blocked (`AUDIO_CAPABILITY_MISMATCH`).
   - When all 7 checks pass, `Compile` inserts a `status='ready'` `prompt_contracts` row (`revision = storyboard_revision`, `prompt_revision = prompt_revisions.revision`) and sets `pipelines.prompt_status='ready'`.

3. **Execution Admission Gate, Worker Claim Re-verification & SQL Lineage (`GET /api/pipeline/lineage`)**:
   - `generation.Request` (`/api/generate/image`, `/api/generate/text`, `/api/generate/estimate`) and MCP `design_execute` accept optional `contract_id` / `contractId`.
   - In any project that has a registered creative pipeline (`pipelines` or `creative_artifacts`), omitting `contract_id` / `contractId` on `video:*` generation or on `image:*` generation targeting a pipeline-bound Composition (`isCompositionPipelineBound`) or pipeline `BaseVersionID` is rejected at admission (`409 waiting_user`), while standalone non-pipeline `image:*` Compositions and M3 `ImageEditor` revision edits on already-delivered media Versions (`isDeliveredMediaRevisionEdit`) remain permitted.
   - When `contract_id` is supplied, `AuthorizeExecution` verifies within the admission/billing transaction that the contract is `ready` (or `submitted` for the same task), current `intent`/`script`/`storyboard` approvals are valid, `prompt` bytes and `SHA-256` match verbatim, `capabilityId` and canonical `params` match, and `references` match in exact order. `tasks.Submit` binds `prompt_contracts.task_id` in the same SQLite transaction.
   - In `tasks.Worker.Run`, immediately before transitioning `task_attempts` to `'submitting'` and invoking `Executor.Submit`, `ClaimExecution` re-verifies the contract and upstream approvals inside the claim SQLite transaction and atomically transitions `prompt_contracts.status` from `'ready'` to `'submitted'` (`pipelines.prompt_status='submitted'`). If upstream artifacts were edited after billing approval but before worker claim, the task transitions to `Failed` (`STALE_CREATIVE_APPROVAL`, `NotAccepted: true`) and never calls the provider. Once `'submitted'`, subsequent creative edits do not invalidate the in-flight contract.
   - On task output delivery (`delivery.Service.commit`), `RecordTaskDeliveryLineage` sets `versions.source_version_id = prompt_version_id` on delivered media versions and inserts `version_dependencies` rows for `(media_version_id, prompt_version_id, 'prompt_contract')`, `(media_version_id, storyboard_version_id, 'storyboard_source')`, and `(media_version_id, ref_version_id, 'reference:<index>')`.
   - `GET /api/pipeline/lineage?project_id=P&versionId=V` (or `assetId` / `taskId` / `contractId` / `pipelineId`) returns the complete SQL-backed lineage chain (`intent` → `script` → `storyboard` → `prompt` → `contract` → `task` → `outputs` + `dependencies`). Deleting any referenced project, resource, version, or asset returns `409 Conflict`.

## M6.5: failure classification, revisioned return paths, and one-sentence workflow

This is an explicit MovieDesign typed port addition. The existing 18 design tools and native `question` permission remain unchanged.

1. **Failure Classification & Authoritative Recovery Actions (`gateway/internal/pipeline/recovery.go`)**:
   - `ClassifyFailure(problem)` maps a task or preflight failure to a recommended return stage (`parameter | semantic | upstream | unknown`):
     - `unknown`: submission outcome uncertain (`Unknown == true` or `SUBMISSION_OUTCOME_UNKNOWN`). Never enters a generator or permits `retry`; server actions are `["query", "cancel"]` (`recommendedAction = "query"`).
     - `semantic`: stale or invalidated creative approvals (`STALE_CREATIVE_APPROVAL`, `STALE_PROMPT_REVISION`), content policy rejection, or narrative/duration/audio intent incompatibility. Server actions are `["modify_storyboard"]` (`recommendedAction = "modify_storyboard"`).
     - `parameter`: invalid capability parameter, resolution/size/aspect mismatch, or reference limit error where approved storyboard semantics remain valid. Server actions are `["modify_params", "modify_storyboard"]` (`recommendedAction = "modify_params"`).
     - `upstream`: temporary provider rate limit (`429`), `5xx`, network error before acceptance, or local delivery collection pause (`RESULT_AWAITING_DELIVERY`). Never auto-retries. For pipeline-bound provider failures, server actions are `["retry", "modify_params", "modify_storyboard"]` (`recommendedAction = "retry"`); for local delivery collection (`source == "delivery"`), server actions remain `["retry"]`.

2. **Durable Recovery Receipts & Stage Transitions (`POST /api/tasks/:id/resolve`)**:
   - `tasks.Service.Resolve` accepts `action` in `{"retry", "cancel", "query", "modify_params", "modify_storyboard"}` gated by `expected_revision` and idempotent `store.WriteRequest`.
   - Every human recovery choice records a `RecoveryState` receipt (`stage`, `recommendedAction`, `selectedAction`, `targetStage`, `receiptId`, `requestId`, `fromTaskId`, `fromContractId`, `pipelineId`, `storyboardVersionId`, `promptVersionId`, `updatedAt`) inside `tasks.result_json`:
     - `"modify_params"`: preserves current storyboard revision and valid human approval; surfaces `RecoveryState` on `GET /api/pipeline/prompt` (`PromptReview.tsx`) so the user can compile a new `PromptContract` (`POST /api/pipeline/compile` with `fromTaskId` and `fromContractId`) on the same storyboard revision.
     - `"modify_storyboard"`: surfaces `RecoveryState` on `GET /api/pipeline/storyboard` (`StoryboardReview.tsx`) so the user edits and saves a new storyboard revision (which directionally invalidates old storyboard and prompt approvals while preserving upstream `intent` and `script` approvals).
     - `"retry"`: allowed only when `"retry"` is in `task.actions` (never for `parameter`, `semantic`, or `unknown`). Replays the exact frozen `generation_inputs` (`prompt`, `prompt_sha256`, `params`, `references`, `output`, `provider`) into a new billing-gated attempt (`waiting_user`) without altering any input bytes.
     - `"query"`: allowed only on `unknown` tasks; invokes `Executor.Query` read-only (`reconcileUnknown`) to reconcile upstream status without ever calling `Executor.Submit`.

3. **M3 Editor Media Revisions & Offline M6 Video Boundary**:
   - Delivered media Versions produced by a `submitted` `PromptContract` can enter the M3 `ImageEditor` (annotation + mask repair `mode: "new_version"` on the same media `Resource`). `RecordTaskDeliveryLineage` records `(new_media_version_id, base_media_version_id, 'media_revision')` and `TraceLineage` includes descendant media revisions on the same `Resource` in the SQL lineage graph.
   - In M6, `video:*` capabilities are supported for offline storyboard review, approval, and 7-gate `PromptContract` compilation (`PromptReview.tsx` explicitly marks the M6 video execution adapter as test-only / pending M7.1 `ark-video` integration).

