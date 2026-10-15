# MovieDesign

本地优先的 AI 设计工作台，使用 Tauri 2 + React 19 + Go 1.24 + SQLite；opencode 在 M5 接入。

当前实现 **M1 基础工作台**：桌面壳启动本地 gateway、完成迁移、前端健康检查、显示空创作画布，退出时回收 gateway。素材编辑、对话、生成均未在 M1 实现。

## Windows x64 开发

需要 Node.js 22.12+（当前验证 24.16）、pnpm 12.8.1、Rust stable / MSVC C++ Build Tools、WebView2，以及支持自动工具链下载的 Go。`gateway/go.mod` 固定 Go 1.24.13 工具链；不会替换机器全局 Go。

```powershell
cd desktop
pnpm install --frozen-lockfile
pnpm desktop:dev
```

Tauri 自动先构建 gateway，再启动 Vite。只运行 `pnpm dev` 不会启动桌面壳；浏览器预览会显示缺少本地桥的错误，这是预期行为。

## 验证与构建

从仓库根目录运行：

```powershell
./scripts/check.ps1
cd desktop
pnpm desktop:build
# 可选：构建 NSIS 安装包
pnpm tauri build
```

`desktop:build` 输出可运行的生产程序到 `desktop/src-tauri/target/release/`，运行时必须保留同目录的 `open-design-gateway.exe`。NSIS 安装包位于该目录下的 `bundle/nsis/`。

`scripts/check.ps1` 运行 Go 测试/vet、前端类型检查/生产构建、Rust 测试及进程冒烟。测试数据保留于忽略提交的 `.runtime/`，便于检查数据库和日志。

真实桌面验证（需要可交互的 Windows 会话）从仓库根目录运行 `./scripts/smoke-desktop.ps1 -Mode release`；开发版使用 `-Mode debug`，先运行 `cargo build --manifest-path desktop/src-tauri/Cargo.toml` 并在另一终端启动 Vite。测试会打开并关闭本次创建的窗口，检查真实 WebView 健康请求、保存截图并确认 sidecar 回收。

## 运行数据与故障诊断

默认数据目录由 Tauri 的 `app_data_dir` 决定（应用标识 `io.github.newyngwieslash-ops.moviedesign`）。测试可通过 `MOVIEDESIGN_DATA_DIR` 指定独立目录。

- `moviedesign.db`：业务表和迁移记录。
- `logs/gateway.log`：结构化服务日志。
- `logs/shell.log`：sidecar 启动错误。
- 固定绑定 `127.0.0.1:8001`；端口被其他程序占用时报告失败，绝不连接或停止该程序。
- 原应用源码只作为参考，不读取或复用其用户数据、账号、数据库或凭据。

健康检查要求正确 Origin；生产模式示例：

```powershell
Invoke-RestMethod http://127.0.0.1:8001/api/health -Headers @{Origin='http://tauri.localhost'}
```

该校验防浏览器跨站请求，不承担本机进程身份认证。

## 规格与进度

- [原始实现规格](docs/spec.md)
- [已确认契约与待决项](docs/contracts.md)
- [里程碑验收记录](docs/milestones.md)
- [逆向资料索引](analysis-docs/README.md)

当前首个交付平台是 Windows x64；其他平台尚未验证。
