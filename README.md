# MovieDesign

本地优先的 AI 设计工作台，使用 Tauri 2 + React 19 + Go 1.25 + SQLite + opencode。

**M1–M4 已通过工程检查与独立整阶段审查**：项目、素材库、版本血缘、幂等投递、八图形编辑与标注、持久画布、自定义连接、图像生成与修复、费用卡和任务中心。M5 已接入受管 opencode、TipTap 聊天、资产引用、结构化问题、18项 design 工具与独立文本任务，当前正在独立审查修复。分镜流水线、视频执行和签名更新仍由 M6–M7 接续。真实凭据支持的 LLM/图像/视频验收未执行；本地协议 fixture 与生产窗口验证不代表真实模型效果或费用验收。

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

`desktop:build` 输出可运行的生产程序到 `desktop/src-tauri/target/release/`，运行时必须保留同目录的 `open-design-gateway.exe`、`moviedesign-opencode.exe` 及 `media/` 资源。首次构建也会下载并校验固定 ffprobe 与许可文件。NSIS 安装包位于该目录下的 `bundle/nsis/`。

`scripts/check.ps1` 运行 Go 测试/vet、编辑器/画布及恢复检查、前端类型检查/生产构建、Rust 测试和 gateway 进程冒烟。测试数据保留于忽略提交的 `.runtime/`，便于检查数据库和日志。

真实桌面验证（需要可交互的 Windows 会话）从仓库根目录运行 `./scripts/smoke-desktop.ps1 -Mode release`；开发版使用 `-Mode debug`，先运行 `cargo build --manifest-path desktop/src-tauri/Cargo.toml` 并在另一终端启动 Vite。测试会打开并关闭本次创建的窗口，检查真实 WebView 健康请求、保存截图并确认 sidecar 回收。

生产版完整画布与原生重启检查使用 `./scripts/smoke-desktop.ps1 -Mode release -Smoke canvas`；普通素材导入恢复检查使用 `-Smoke m2`。丰富图形/文字/标注检查使用开发版 `-Smoke editor`、`-Smoke editor-product`，依赖 Vite，不能当作生产版检查。实际结果、协议适配及验证范围见 [里程碑记录](docs/milestones.md) 和 [契约文档](docs/contracts.md)。签名安装包与更新验收留待 M7。

## 图像连接与生成

在“供应商设置”中保存图像连接，选择已实现的 `openai-images` 协议，填写本地或云端 Base URL、model ID、能力和估算价格来源。应用支持已知协议的连接配置，不把任意 HTTP API 当成兼容服务；`ark-video` 视频连接可先保存，实际视频执行在 M7 接入。免费模型列表探测不执行生成，也不证明模型能力已通过验收。

密钥仅在设置时输入，以 Windows DPAPI 加密保存在应用数据目录；读取配置不返回原密钥。MovieDesign 使用 BYOK，费用由供应商收取，不提供充值或钱包。未知价格会阻止生成，明确的零价也需确认卡批准。

选择项目后在画布声明目标、提示词和参考图，点击请求费用确认，再在卡片批准或拒绝。区域标注可创建源尺寸 PNG 蒙版，图像修复沿用选中的源版本及 Resource。取消只停止本地投递，供应商可能继续处理和收费；迟到结果须另行通过采纳卡授权。下载/投递失败只能续收集原结果；未确定的生成结果不会自动重新提交。重启后的“确认原请求”使用原请求身份与内容。

原生协议验证使用 `./scripts/smoke-desktop.ps1 -Mode release -Smoke generation`；设置与卡片分别使用 `-Smoke providers`、`-Smoke questions`。这些脚本使用隔离数据和本地协议 fixture；真实供应商调用及验收范围见里程碑记录。

## 运行数据与故障诊断

供应商设置提供独立 LLM 连接：OpenAI Chat Completions 兼容、OpenAI Responses、Anthropic Messages。填写 Base URL、模型 ID 和 token 上限；API 密钥仅写入 DPAPI。保存后重启生效，界面分别显示当前模型和待生效配置。免费测试只读取模型列表，Anthropic 只校验本地配置，不自动推理。文本生成另需明确估算价格，并经过费用卡；未知价格不能执行。

聊天支持 TipTap 资产引用、标注、排队与撤回、流式消息和人工问题卡。原生文件和命令工具禁用，媒体 agent 只允许18项 design 工具与 question；每轮最多40次工具调用。文本生成使用独立的零工具 session。未知结果保留原请求身份，重启不自动重发；取消聊天不隐式取消已经受理的生成任务。

`./scripts/smoke-desktop.ps1 -Mode release -Smoke agent` 验证真实 WebView 的 LLM 设置、模型列表本地 fixture、密钥回执恢复、重启及两个 owned child 的退出。固定 agent 端口为 `127.0.0.1:4096`，随机 Basic 密码仅在原生进程与 gateway 之间传递；前端只访问 gateway。隔离配置/home/cache/temp、会话 data/state 和日志都位于 `agent-host/`，不读用户 opencode 配置或凭据。来源和许可见 [第三方依赖](docs/third-party.md)。

`-Smoke tools` 验证实际 opencode 与独立文本、人工费用/规则卡及画布投递；`-Smoke chat-fix` 验证草稿、队列竞态和重启恢复。它们使用本地合成供应商；Responses/Anthropic 的原生推理及真实供应商调用仍未验收。实际审查结论与当前构建对应证据以里程碑记录为准。

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
