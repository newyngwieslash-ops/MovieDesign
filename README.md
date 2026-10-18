# MovieDesign

本地优先的 AI 设计工作台，使用 Tauri 2 + React 19 + Go 1.25 + SQLite；opencode 在 M5 接入。

当前已实现 **M1–M4 工程功能**：项目与素材库、版本血缘、幂等投递、八图形编辑与标注、持久画布，以及自定义图像/视频连接设置、图像生成与标注修复、费用确认卡、任务中心和 SSE 恢复。画布支持图像、视频、音频和只读文本；同一图像修订保存为同一 Resource 的新 Version。M4 已通过工程检查与独立整阶段审查，真实凭据支持的图像生成/修复验收仍未执行；已通过的本地协议 fixture 与生产窗口验证不代表真实模型效果或费用验收。opencode/聊天、分镜流水线、视频生成与签名更新由 M5–M7 接续。

## Windows x64 开发

需要 Node.js 22.12+（当前验证 24.16）、pnpm 12.8.1、Rust stable / MSVC C++ Build Tools、WebView2，以及支持自动工具链下载的 Go。`gateway/go.mod` 指定 Go 1.25.8 工具链；不会替换机器全局 Go。

```powershell
cd desktop
pnpm install --frozen-lockfile
pnpm desktop:dev
```

Tauri 自动先构建 gateway，下载并校验固定 OpenCode 1.19.0 应用专用 binary，再启动 Vite。首次构建需要访问官方 GitHub Release；缓存归项目 `.tools/` 所有，不修改全局 opencode。只运行 `pnpm dev` 不会启动桌面壳。

## 验证与构建

从仓库根目录运行：

```powershell
./scripts/check.ps1
cd desktop
pnpm desktop:build
# 可选：构建 NSIS 安装包
pnpm tauri build
```

`desktop:build` 输出可运行的生产程序到 `desktop/src-tauri/target/release/`，运行时必须保留同目录的 `open-design-gateway.exe` 与 `moviedesign-opencode.exe`。NSIS 安装包位于该目录下的 `bundle/nsis/`。

`scripts/check.ps1` 运行 Go 测试/vet、编辑器/画布及恢复检查、前端类型检查/生产构建、Rust 测试和 gateway 进程冒烟。测试数据保留于忽略提交的 `.runtime/`，便于检查数据库和日志。

真实桌面验证（需要可交互的 Windows 会话）从仓库根目录运行 `./scripts/smoke-desktop.ps1 -Mode release`；开发版使用 `-Mode debug`，先运行 `cargo build --manifest-path desktop/src-tauri/Cargo.toml` 并在另一终端启动 Vite。测试会打开并关闭本次创建的窗口，检查真实 WebView 健康请求、保存截图并确认 sidecar 回收。

生产版完整画布与原生重启检查使用 `./scripts/smoke-desktop.ps1 -Mode release -Smoke canvas`；普通素材导入恢复检查使用 `-Smoke m2`。丰富图形/文字/标注检查使用开发版 `-Smoke editor`、`-Smoke editor-product`，依赖 Vite，不能当作生产版检查。实际结果、协议适配及验证范围见 [里程碑记录](docs/milestones.md) 和 [契约文档](docs/contracts.md)。签名安装包与更新验收留待 M7。

## 图像连接与生成

在“供应商设置”中保存图像连接，选择已实现的 `openai-images` 协议，填写本地或云端 Base URL、model ID、能力和估算价格来源。应用支持已知协议的连接配置，不把任意 HTTP API 当成兼容服务；`ark-video` 视频连接可先保存，实际视频执行在 M7 接入。免费模型列表探测不执行生成，也不证明模型能力已通过验收。

密钥仅在设置时输入，以 Windows DPAPI 加密保存在应用数据目录；读取配置不返回原密钥。MovieDesign 使用 BYOK，费用由供应商收取，不提供充值或钱包。未知价格会阻止生成，明确的零价也需确认卡批准。

选择项目后在画布声明目标、提示词和参考图，点击请求费用确认，再在卡片批准或拒绝。区域标注可创建源尺寸 PNG 蒙版，图像修复沿用选中的源版本及 Resource。取消只停止本地投递，供应商可能继续处理和收费；迟到结果须另行通过采纳卡授权。下载/投递失败只能续收集原结果；未确定的生成结果不会自动重新提交。重启后的“确认原请求”使用原请求身份与内容。

原生协议验证使用 `./scripts/smoke-desktop.ps1 -Mode release -Smoke generation`；设置与卡片分别使用 `-Smoke providers`、`-Smoke questions`。这些脚本使用隔离数据和本地协议 fixture；真实供应商调用及验收范围见里程碑记录。

## 运行数据与故障诊断

M5.1 在供应商设置中提供独立 LLM 连接：自定义 OpenAI Chat Completions 兼容、OpenAI Responses、Anthropic Messages。填写 Base URL、模型 ID 和 token 上限；API 密钥仅写入 DPAPI。保存后重新启动应用生效，界面同时显示当前模型与待生效状态。免费测试只读取已保存的模型列表，Anthropic 只校验本地配置；不会自动推理。LLM host 就绪与尚未注册的媒体工具分别显示，聊天和工具轮次将在后续 M5 任务接续。

`./scripts/smoke-desktop.ps1 -Mode release -Smoke agent` 验证真实 WebView 的 LLM 设置、模型列表本地 fixture、密钥回执恢复、重启及两个 owned child 的退出。固定 agent 端口为 `127.0.0.1:4096`，随机 Basic 密码仅在原生进程与 gateway 之间传递；前端只访问 gateway。隔离配置/home/cache/temp、会话 data/state 和日志都位于 `agent-host/`，不读用户 opencode 配置或凭据。来源和许可见 [第三方依赖](docs/third-party.md)。

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

- [完整实施计划：M2–M7](docs/superpowers/plans/2026-10-15-moviedesign-complete.md)（自定义本地/云端供应商、30项任务与验收门槛）
- [原始实现规格](docs/spec.md)
- [已确认契约与待决项](docs/contracts.md)
- [里程碑验收记录](docs/milestones.md)
- [逆向资料索引](analysis-docs/README.md)

当前首个交付平台是 Windows x64；其他平台尚未验证。
