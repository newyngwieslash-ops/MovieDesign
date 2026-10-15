# 里程碑验收

| 里程碑 | 范围 | 状态 |
| --- | --- | --- |
| M1 | Tauri、gateway、SQLite、空工作台、生命周期 | 验证通过 |
| M2 | 对象模型 CRUD、幂等投递、unknown 重放 | 待实施 |
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

## M1 范围说明

- 目前只交付 Windows x64；尚未验证 macOS/Linux。
- 登录按已确认 Q4 延后；opencode 按 M5 接入，M1 健康不代表 agent 可用。
- 编辑器、对象 CRUD、任务表、SSE 与业务写入按里程碑逐项接入。
- 原规格本文完整保存于 spec.md；确认后的适配与未知项见 contracts.md。
