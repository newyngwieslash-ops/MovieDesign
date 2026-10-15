# MovieDesign 契约

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
- M1 无业务写入 HTTP 路由。后续写入必须统一实现 requestId 幂等，不以 M1 缺省行为代替。
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

- M2：项目与类型存储、投递幂等记录和 supporting tables；核心表不随意改变。
- M4/M7：官方图像/视频模型及计费估算来源。15 秒口播＋音乐必须由实际能力满足，不用示意结果冒充成功。
- M5：question 回执 HTTP 适配、SSE 回放边界、LLM 提供商配置；原生 question 回执为 answers 二维字符串数组，禁止把普通聊天文本当批准。
- M6：分镜→prompt→task→version→asset 完整血缘、revision 绑定批准、参数与语义变更区别；source_version_id 一列不能替代全部支撑关系。
- M7：自有更新策略地址、签名与 manual_only 的缓存兜底。M1 不接入原厂更新基础设施。

## 安全边界

Origin 校验针对浏览器 CSRF；curl 或本机程序可以伪造 Origin。M1 不宣称具有本机恶意进程隔离或令牌认证。窗口 CSP 限制 gateway 和开发 HMR，前端无 shell/文件系统执行权限。
