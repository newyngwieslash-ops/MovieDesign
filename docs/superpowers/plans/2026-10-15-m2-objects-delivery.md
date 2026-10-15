# M2 对象模型与幂等投递 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 完成项目、资产、对象 CRUD 和可跨崩溃恢复的三态投递，交付可导入素材、浏览版本的工作台。

**Architecture:** 在 M1 的同一 SQLite 上增加支撑表；文件阶段与 SQL 阶段用持久 journal 衔接。HTTP 与后续 MCP 调用同一个 delivery.Service。

**Tech Stack:** Go、database/sql、modernc.org/sqlite、golang-migrate、go-playground/validator/v10、xxh3、React/TanStack。

## Global Constraints

继承总计划全部约束。核心五表不改。“所有写操作带 requestId”；unknown 同输入同键续做，不重新生成。所有路径受项目边界约束；先完成事实追踪再写最小代码。

## 文件与接口分工

新增 server/{request,objects,files,delivery}.go、store/{projects,objects,requests}.go、workspace/{files,fingerprint,fingerprint_windows}.go、delivery/{types,service}.go；保留 store/sqlite.go 的连接与迁移所有权。每个 Go 包用单个主要测试文件覆盖其契约。前端复用 gateway.ts，不再创建第二个 HTTP 客户端。

### Task M2.1：请求边界、项目作用域与迁移

**Files:**
- Modify: `gateway/internal/server/server.go`, `gateway/cmd/open-design-gateway/main.go`, `docs/contracts.md`。
- Create: `gateway/internal/server/request.go`, `gateway/internal/store/requests.go`。
- Create: `gateway/migrations/000003_workspace_requests.up.sql`, `gateway/migrations/000003_workspace_requests.down.sql`。
- Test: `gateway/internal/server/request_test.go`, `gateway/internal/store/requests_test.go`。

**Interfaces:** `DecodeJSON[T](w http.ResponseWriter,r *http.Request)(T,error)` 严格单对象解析；`CanonicalJSON(raw []byte)([]byte,error)` 稳定编码；`Scope{ProjectID,Operation,RequestID string}` 标识写操作。`server.New(origin string, db *sql.DB, workspaceRoot string) http.Handler` 取代只处理 health 的 Handler，Origin 仍在全部路由外层。

- [ ] 先写 table tests：未知字段、多 JSON、缺 requestId、超限正文、非法 project_id、未获准 Origin/预检均无 DB 写入。CanonicalJSON 的实际检查：

```go
func TestCanonicalPreservesPrompt(t *testing.T) {
    a, err := CanonicalJSON([]byte(`{"prompt":"  中文\n","references":["a","b"]}`))
    if err != nil { t.Fatal(err) }
    b, err := CanonicalJSON([]byte(`{"references":["a","b"],"prompt":"  中文\n"}`))
    if err != nil { t.Fatal(err) }
    if string(a) != string(b) { t.Fatal("object order changed identity") }
    c, err := CanonicalJSON([]byte(`{"prompt":"中文","references":["a","b"]}`))
    if err != nil { t.Fatal(err) }
    if string(a) == string(c) { t.Fatal("prompt was normalized") }
}
```

- [ ] `go -C gateway test ./internal/server ./internal/store -run 'TestCanonical|TestRequest' -v`；初次应因缺接口/行为失败。
- [ ] 增加 validator 并固定兼容版本；JSON body 限 1 MiB（媒体 multipart 单独限额），使用 DisallowUnknownFields、UseNumber、EOF 检查；重复 JSON key 在输入扫描阶段拒绝，避免两个解析器理解不同。保留 number token，不转 float64。字符串不得 trim。SQL 新增：

```sql
CREATE TABLE projects(id TEXT PRIMARY KEY,name TEXT NOT NULL,created_at INTEGER NOT NULL);
CREATE TABLE composition_types(id TEXT PRIMARY KEY,project_id TEXT NOT NULL,label TEXT NOT NULL,UNIQUE(project_id,label));
CREATE TABLE asset_projects(asset_id TEXT PRIMARY KEY REFERENCES assets(id),project_id TEXT NOT NULL REFERENCES projects(id));
CREATE TABLE write_requests(project_id TEXT NOT NULL,operation TEXT NOT NULL,request_id TEXT NOT NULL,input_json TEXT NOT NULL,state TEXT NOT NULL,response_json TEXT,PRIMARY KEY(project_id,operation,request_id));
CREATE TABLE file_operations(project_id TEXT NOT NULL,request_id TEXT NOT NULL,operation TEXT NOT NULL,input_json TEXT NOT NULL,stage TEXT NOT NULL,source TEXT,target TEXT,receipt TEXT,PRIMARY KEY(project_id,request_id));
```

- [ ] 将 Origin 预检扩为 GET/POST/OPTIONS 及实际需要的 Content-Type、Last-Event-ID；不能因为添加上传而放开任意来源。requestId 冲突统一409/invalid_input。新增 API、状态码和项目查询参数逐一写入 docs/contracts.md。
- [ ] 原 M1 tests 调用新构造函数并继续通过；运行上述测试及 `go -C gateway test ./...`，提交 `feat(M2.1): validate scoped idempotent requests`。

### Task M2.2：项目、类型与对象 CRUD

**Files:**
- Create: `gateway/internal/store/projects.go`, `gateway/internal/store/objects.go`, `gateway/internal/server/objects.go`。
- Modify: `gateway/internal/server/server.go`。
- Test: `gateway/internal/store/objects_test.go`, `gateway/internal/server/objects_test.go`。

**Interfaces:** `store.Objects{DB *sql.DB}`；`CreateProject(ctx,name,requestID)(Project,error)`；`List(ctx,projectID,kind string,offset,limit int)(Page,error)`；`Mutate(ctx,projectID,kind string,input Mutation)(json.RawMessage,error)`。`Mutation{RequestID,Action,ID string; Value json.RawMessage}`，Value 按对象专属 schema 解析，禁止任意 SQL 字段映射。Page 为 `{items,coverage:{total,next_offset,exhausted}}`。

- [ ] 用临时 SQLite 写跨项目拒绝、相同类型 label 复用、同内容 new_version 保留 resource、独立内容新 resource、被引用资产删除拒绝、分页 total 稳定等测试。核心边界检查：

```go
func TestResourceIdentity(t *testing.T) {
    db, err := Open(filepath.Join(t.TempDir(), "objects.db")); if err != nil { t.Fatal(err) }; defer db.Close()
    _, err = db.Exec(`INSERT INTO compositions VALUES('c','p','type','片头',NULL);
INSERT INTO resources VALUES('r','c','主视频');
INSERT INTO versions(id,resource_id,source_version_id,created_at) VALUES('v1','r',NULL,1),('v2','r','v1',2)`)
    if err != nil { t.Fatal(err) }
    var count int
    if err = db.QueryRow(`SELECT COUNT(DISTINCT resource_id) FROM versions`).Scan(&count); err != nil || count != 1 { t.Fatalf("identity: %d %v",count,err) }
}
```

- [ ] 运行 `go -C gateway test ./internal/store ./internal/server -run 'TestResource|TestObjects' -v`，跨项目/幂等 cases 在 handler 未接入时失败。
- [ ] GET 各 endpoint 使用明确列名及参数化 SQL。POST action 限 create/update/delete；versions 为不可变内容记录，修改内容必须新版本，delete 对被血缘/任务/当前选择引用的版本返回409，不执行级联物理删除。compositions.name/attributes 与 resource.description 可更新。删除有成员的对象拒绝并说明依赖，避免“CRUD”抹掉历史。
- [ ] 同一次事务写业务对象及 write_requests completed receipt；分页 limit 默认50、范围1–200，offset≥0，总数与页数据来自同一读事务。项目类型通过 UNIQUE(project_id,label) 复用，禁止应用层先查后写竞态。
- [ ] 运行完整 Go 测试；提交 `feat(M2.2): persist project object identities`，记录不可变版本的更新语义。

### Task M2.3：上传、文件操作与真实资产指纹

**Files:**
- Create: `gateway/internal/workspace/files.go`, `gateway/internal/workspace/fingerprint.go`, `gateway/internal/workspace/fingerprint_windows.go`, `gateway/internal/server/files.go`。
- Test: `gateway/internal/workspace/files_test.go`, `gateway/internal/server/files_test.go`。
- Modify: `gateway/go.mod`, `gateway/go.sum`, `gateway/internal/server/server.go`。

**Interfaces:** `workspace.Open(root string)(*Workspace,error)`；`Stage(ctx,projectID,requestID,name string,r io.Reader)(string,error)`；`Commit(ctx,projectID,requestID,path string)(Asset,error)`；`OpenRegular(projectID,relative string)(*os.File,error)`；`Fingerprint(file *os.File,full bool)(Fingerprint,error)`。Fingerprint 含 size/mtime_ms/dev_id/inode/quick_hash/full_hash。

- [ ] 表驱动测试：`../`、绝对盘符、UNC、junction/symlink、路径前缀碰撞、流超限、上传断开、提交响应丢失、rename 后 id 稳定、同 size/mtime 不同内容不能合并；Windows 文件 identity 用真实临时文件移动验证。

```go
func TestRejectEscape(t *testing.T) {
    w, err := Open(t.TempDir()); if err != nil { t.Fatal(err) }
    for _, path := range []string{"../secret", "C:/secret", "//server/share/file", "a/../../b"} {
        if f, err := w.OpenRegular("project", path); err == nil { f.Close(); t.Fatalf("accepted %q", path) }
    }
}
```

- [ ] 运行 `go -C gateway test ./internal/workspace ./internal/server -run 'TestRejectEscape|TestFiles|TestFingerprint' -v`。
- [ ] Stage 限默认256 MiB、单文件、requestId 必填；同键以内容 hash 校验重放。临时文件先 Flush/Close，再以确定目标路径归档，持久 file_operations 阶段；响应 path 是 opaque staging 相对路径，不暴露外部绝对路径。Commit 创建或复用 asset 并返回 `{ok:true,relative}`；失败保留可恢复阶段，不覆盖既有文件。
- [ ] 采用 Go 可用的受根目录约束文件访问，并在 Windows 对 reparse point、junction 与实际句柄最终路径验证；不是只做字符串 StartsWith。quick_hash 用 xxh3 对首4MiB，full_hash 仅候选冲突/显式请求时计算。mime 用内容探测，尺寸读图片 header，文件标识取 Windows volume/file identity。delete 标 missing/移入受管回收区域，禁止无引用核查就物理删除。
- [ ] 实现 list/content/delete/mkdir/rename/copy，全部写操作复用 file_operations；content 使用流式输出、正确 mime 与 Range，支持图片/视频预览但拒绝主动 HTML 执行。JSON错误仍统一。跑完整测试并提交 `feat(M2.3): manage workspace files and asset fingerprints`。

### Task M2.4：投递三态、预分配与崩溃恢复

**Files:**
- Create: `gateway/internal/delivery/types.go`, `gateway/internal/delivery/service.go`, `gateway/internal/server/delivery.go`。
- Create: `gateway/migrations/000004_delivery_journal.up.sql`, `gateway/migrations/000004_delivery_journal.down.sql`。
- Test: `gateway/internal/delivery/service_test.go`。

**Interfaces:** `Deliver(ctx context.Context,projectID string,in Request)(Receipt,error)`；Request 完全按 §7（requestId/sources/output）定义；Receipt 外层 `delivery`，内层 `status,versions,problem?`。`Recover(ctx,projectID,requestID)` 只续做 journal，不调用 provider。测试 fault hook 只作为 Service 的未导出字段，生产路由不暴露故障注入。

- [ ] 表驱动写出 prepare后、archive后、SQL提交前、SQL提交后/响应前四个崩溃点；每次关闭并重开 DB、重新构造服务，再以相同 input 重放。并发重复、同键异输入、缺 key、重复 key、跨项目 taskOutputKey、过期 baseVersionId 均覆盖。
- [ ] 运行 `go -C gateway test ./internal/delivery -run 'TestReplay|TestConcurrent|TestBlocked' -count=1 -v`，先确认没有幂等恢复时版本数检查会失败。
- [ ] 新表保存 input_json、stage、status、receipt；delivery_sources 保存 source_key、确定的 archive_path、asset_id/resource_id/version_id 及来源 task/output。prepare 在一个事务分配全部 ID；archive 每完成一个文件更新对应阶段；commit 事务一次性写所有 assets/resources/versions/members 和 completed receipt。不要出现一批 SQL 部分成功的 blocked 回执。

```sql
CREATE TABLE deliveries(project_id TEXT NOT NULL,request_id TEXT NOT NULL,input_json TEXT NOT NULL,stage TEXT NOT NULL,status TEXT NOT NULL,receipt TEXT,PRIMARY KEY(project_id,request_id));
CREATE TABLE delivery_sources(project_id TEXT NOT NULL,request_id TEXT NOT NULL,source_key TEXT NOT NULL,archive_path TEXT NOT NULL,asset_id TEXT NOT NULL,resource_id TEXT NOT NULL,version_id TEXT NOT NULL,stage TEXT NOT NULL,source_json TEXT NOT NULL,PRIMARY KEY(project_id,request_id,source_key));
```

- [ ] 同键先比较 input；completed 返回原 receipt；unknown 读取确定 ID 与归档阶段继续；不可修正的输入返回 blocked，原键保留被阻止记录，修正需新键。Source key 集合必须与 resource key 集合相等；placements 只能引用存在 key；create 新 Resource，new_version 延续 baseVersion 对应 Resource。文件存在但 hash 不匹配是 blocked，不覆盖。
- [ ] 单源create重放验收 SQL 为 `SELECT COUNT(*) FROM versions WHERE resource_id=?` 期望1、`SELECT COUNT(*) FROM composition_members WHERE resource_id=?` 期望声明的placement数；new_version用基线数量加1断言。运行完整 Go tests；提交 `feat(M2.4): recover idempotent delivery receipts`。

### Task M2.5：对象 UI、unknown 重放与 M2 验收

**Files:**
- Modify: `desktop/src/gateway.ts`, `desktop/src/App.tsx`, `desktop/src/main.tsx`, `scripts/check.ps1`, `docs/milestones.md`。
- Create: `desktop/src/features/projects/ProjectPicker.tsx`, `desktop/src/features/assets/AssetLibrary.tsx`, `desktop/src/features/assets/VersionHistory.tsx`。
- Create: `scripts/smoke-m2.mjs`。

**Interfaces:** gateway.ts 导出 `api<T>(projectID,path,init):Promise<T>` 和 `newRequestID():string`；写入由用户动作创建一次 ID，重放闭包持有完整原输入。Query keys 必含 projectID；切换项目取消旧查询并拒收旧响应。

- [ ] 扩展现有真实 WebView 检查：新建项目、导入 PNG、commit、deliver 两次，检查同一版本 ID；另建项目看不到资产；新版本保留旧版本。断线重放按钮使用原请求，不能调用 newRequestID。
- [ ] `node scripts/smoke-m2.mjs` 连接测试专用已启动桌面与其 CDP 端口（参数与 smoke-desktop.mjs 一致）；初次应因项目/资产 UI 缺失失败。
- [ ] 复用 TanStack Router/Query；实现项目选择器、文件拖入/选择、资产列表和版本历史；错误信息取服务端结构化错误。unknown 显示“结果待确认”及“重新查询/续投递”，不展示“重新生成”。选择新版本更新对象选择，为 M3 持久画布接口留对象引用而非图片 URL。

```ts
export function newRequestID(): string { return crypto.randomUUID(); }
export function replayBody(input: object): string { return JSON.stringify(input); }
// 同一次提交的 input 与 requestId 保存在操作状态中，重连只重发这个 body。
```

- [ ] `scripts/check.ps1` 增加 M2 离线 API 检查；真实 WebView 验证与进程重启后对象/资产/版本持久化。原 M1 启停、Origin tests 不得回退。
- [ ] 更新 docs/contracts.md 的全部新增请求/响应实例与 M2验收证据，提交 `feat(M2.5): expose project assets and version history`；里程碑全量通过后推送。
