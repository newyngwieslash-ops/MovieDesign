# 里程碑验收

完整后续计划见[总计划与六份分计划](superpowers/plans/2026-10-15-moviedesign-complete.md)。M2 正在实施，M3–M7 待实施；计划中的测试命令不是已执行结果，各阶段证据另行记录。

| 里程碑 | 范围 | 状态 |
| --- | --- | --- |
| M1 | Tauri、gateway、SQLite、空工作台、生命周期 | 验证通过 |
| M2 | 对象模型 CRUD、幂等投递、unknown 重放 | 实施中，尚未整体验收 |
| M3 | 八图形编辑器、历史、标注 | 待实施 |
| M4 | 图像模型、能力契约、任务状态机、计费与事件 | 待实施 |
| M5 | opencode、MCP、提示词、聊天与确认卡 | 待实施 |
| M6 | 两个技能、分镜批准、Prompt 锁定与 Preflight | 待实施 |
| M7 | 视频、任务中心完善、更新策略 | 待实施 |

## M1 验收项

- [x] Go 1.24.13 工具链与 CGO_ENABLED=0 构建。
- [x] Go 全量测试及 vet，含迁移、持久化、外键、Origin、JSON-RPC。
- [x] React 类型检查与生产构建。
- [x] Rust 测试与桌面构建。
- [x] 独立 gateway 冒烟：实际 HTTP、端口冲突、重启、关闭及管道 EOF。
- [x] 开发窗口实际连接健康端点。
- [x] 生产窗口实际连接健康端点。
- [x] 关闭桌面窗口后 sidecar 与端口释放。
- [x] 核对规格、记录确认后的适配。

Git 提交与远程推送结果以仓库历史为准，不以本地构建成功代替远程交付。

## M1 验证记录（2026-10-15）

- `scripts/check.ps1`：通过，Go 三个测试包、go vet、TypeScript、Vite、Rust 协议测试和 gateway 进程冒烟。
- `pnpm desktop:build`：通过，生产 exe 与同目录 gateway sidecar 可运行。
- `pnpm tauri build --bundles nsis`：通过，生成 `MovieDesign_0.1.0_x64-setup.exe`（9.52 MiB）；未执行安装。
- `scripts/smoke-desktop.ps1 -Mode debug`：真实 WebView 的 `http://localhost:1420` 请求健康端点成功，无未捕获异常，正常关闭后无 gateway 残留。
- `scripts/smoke-desktop.ps1 -Mode release`：真实 WebView 的 `http://tauri.localhost` 请求健康端点成功，画布显示正常，关闭后端口释放。
- UI 冒烟用 Node 内置 WebSocket 连接本次测试专用的 WebView2 调试端口；端口只通过测试进程环境启用，日常运行不启用。截图与 JSON 证据保留在 `.runtime/desktop-*/`，不提交运行数据。
- 初次沙箱联调无法访问 Vite；改在同一桌面会话运行开发服务和测试后通过。沙箱失败没有被当成产品通过证据。
- 安装包生成不等于安装/卸载验证；M1 未修改本机安装注册。
- 开发服务排除 `src-tauri` 文件监听，避免 Windows 编译期间锁定 exe/PDB 导致 Vite EBUSY 退出。

## M1 范围说明

- 目前只交付 Windows x64；尚未验证 macOS/Linux。
- 登录按已确认 Q4 延后；opencode 按 M5 接入，M1 健康不代表 agent 可用。
- 编辑器、对象 CRUD、任务表、SSE 与业务写入按里程碑逐项接入。
- 原规格本文完整保存于 spec.md；确认后的适配与未知项见 contracts.md。

### M2 实施记录（2026-10-15）

M2.1 提交为 `29f7b0d`，严格输入修正为 `9983337`；专项测试、Go 全量测试/vet 和 gateway 真实进程生命周期冒烟通过。M2.2 对象 CRUD 及严格输入修正已通过独立审查（`b279d80`、`8b0583c`）；真实进程验证了创建、幂等重放、冲突拒绝、Origin 与重启持久化。M2.3 文件层与恢复隔离修正已通过独立审查（`2796144`、`20e26fa`），全量 Go 测试/vet、Windows 句柄及恢复回归通过；真实 gateway 进程完成上传、提交重放、重命名、复制和重启持久化检查。M2.4 投递恢复与审查修正已完成（`627bc04`、`a914910`、`6f6a509`）。M2.5 桌面 UI 和专项验收见下；完整 M2 的独立审查与整体集成由控制任务继续执行，尚不声明整体完成或推送。

### M2.5 专项验证（2026-10-15）

- `node --test scripts/check-m2-recovery.mjs`：通过；上传同文件内容核对、上传→提交→投递各边界序列化/重读、固定三 ID 与精确 body、修订 baseVersion、跨项目记录拒绝。
- `powershell -NoProfile -File scripts/check.ps1`：最终在本机授权环境通过；全量 Go API/存储/文件/投递测试及 vet、sidecar 构建、Node 恢复检查、TypeScript/Vite、Rust 测试、M1 真实 gateway 生命周期/Origin。进程证据 `.runtime/smoke-83f9f9d5b80349f1af13b84442b6a9a8/`。先前沙箱执行在 M1 RPC 超时，其余检查已绿；不计作通过。
- `pnpm --dir desktop desktop:build`：重建当前生产 exe 与 sidecar，通过。
- `scripts/smoke-desktop.ps1 -Mode release -Smoke m2`：当前生产版真实 WebView 通过。DOM 表单创建项目、文件输入真实 PNG 上传/提交/投递、naturalWidth=1、相同投递一次 Version、新版本同 Resource 且保留 source_version_id、第二项目零素材。真实创建与投递 operation 在发送前由 DOMStorage 捕获，重播记录模拟响应丢失；创建刷新后同 body 续建仍一项目，投递刷新并原生重启后同 body/requestId 续投递仍两版本。
- M2 最终证据 `.runtime/desktop-release-c339074e146341bf9b7783fdc4a3627f/`：`m2-before-restart.png`、`m2-recovered.png`、`m2-expected.json`、`m2-renderer.json`、`webview-profile.json`。profile command line 和持久 marker 均确认测试专用隔离，renderer 无未捕获异常，正常关闭后 sidecar/8001 均释放。
- `scripts/smoke-desktop.ps1 -Mode release -Smoke m1`：当前生产版 M1 画布/HTTP health/Origin/正常关闭回归通过；证据 `.runtime/desktop-release-da58f82adb274615964a114ff2ee2a27/`。
- 初始旧 M1 exe 在 M2 smoke 明确报 `M2 project creation UI missing`；后续一次测试观察误选 CORS OPTIONS（无 postData），收紧为 method=POST 后通过。未调整生产逻辑绕过断言。
- 当前每项目一个待导入；256 MiB WebCrypto 全缓冲、完整项目列表内存读取是已注明上限。开发 Origin 仍由现有 Go/M1 测试覆盖，本任务真实桌面使用生产 Origin；没有声称开发窗口或 M3–M7 新验收。

### M2.5 审查修正验证（2026-10-15）

- 修复旧组件迟到响应覆盖/清除新导入：所有阶段写入和终态清除同步比较完整旧记录，已推进或被替换时停止旧续链；同意图阶段也不回退。项目选择偏好读写失败独立告警并保留内存选择，gateway ready 不受影响；关键写意图仍须先持久化。
- `node --test scripts/check-m2-ui.mjs` 修前 7 失败/1 通过；修后结合原 recovery 检查 9/9 通过。检查直接转译实际 App/AssetLibrary 源码，控制 upload/commit/deliver 迟到次序、重挂载与续投递，覆盖 A 不能改 B、同 A 不能回退/继续旧链、选择存储读写异常及关键保存失败零 POST。证据 `.runtime/m25-fix-targeted.txt`。
- 修后 `pnpm --dir desktop desktop:build` 与最终 `scripts/check.ps1` 通过；后者含 Go 全量/vet、sidecar、9 项 Node 检查、前端、Rust 与原生 gateway M1，日志 `.runtime/m25-fix-check.txt`，生命周期证据 `.runtime/smoke-bc27920b40814a57ab6d21b02c0e8408/`。
- 最终生产真实 M2 UI/原生重启恢复通过：`.runtime/desktop-release-7ea7d46f089c449986313120528970de/`；真实 M1 WebView/生命周期回归通过：`.runtime/desktop-release-d3085fecfdde47f7895966b5c5109019/`。均使用测试独立 profile，正常关闭无 sidecar/端口残留。并发迟到/存储拒绝使用受控实际组件源码检查，真实 WebView 仍验证现有正常导入、幂等、血缘、隔离和重启恢复；不将源码控序检查冒充真实网络故障注入。
