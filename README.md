# MovieDesign

本地优先的 AI 设计工作台，使用 Tauri 2 + React 19 + Go 1.25 + SQLite + opencode。

**M1–M7 工程实现、离线与本地协议测试、生产 WebView2 冒烟及独立任务审查全部完成**：涵盖项目与素材库、版本血缘、三态幂等投递、八图形编辑与标注、持久画布、本地/云端自定义供应商（`openai-images`、`ark-video` 与 LLM）、图像生成与修复、受管 `opencode` 与 18 项 `design_*` MCP 工具、TipTap 聊天与人类确认卡、`口播视频` 与 `图片标注修复` 两个方法论技能、分镜流水线与 7 门禁 Preflight、15 秒口播配乐视频执行与 MP4 校验、打断恢复与计费对账、以及签名更新策略（`manual_only` + 24h LKG 缓存 + minisign 验签）。

按四阶交付门槛纪律独立核算：
- **功能已编码**：**通过（PASS）**
- **离线测试通过**：**通过（PASS）**
- **真实生成通过**：**未执行（UNEXECUTED）**（本机未配置真实付费/可用云端或本地模型凭据；本地协议 fixture 与生产窗口验证不代表真实模型效果或费用验收）
- **签名发布完成**：**未执行（UNEXECUTED）**（`scripts/release.ps1 -DryRun` 与签名校验测试已通过；因未注入正式 `TAURI_SIGNING_PRIVATE_KEY`，生产发布按契约 fail-closed 阻断未签名构建，正式私钥签名与干净 Windows 账户真实安装升级保持未执行）

## Windows x64 开发

需要 Node.js 22.12+（当前验证 24.16）、pnpm 12.8.1、Rust stable / MSVC C++ Build Tools、WebView2，以及支持自动工具链下载的 Go。`gateway/go.mod` 指定 Go 1.25.8 工具链；不会替换机器全局 Go。

```powershell
# 一键停止本仓库已有进程并启动生产版（若尚未构建则自动构建）
./restart.bat
# 其他模式：./restart.bat build（重新构建并启动） | ./restart.bat dev（开发热重载） | ./restart.bat stop（仅停止）

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

`scripts/check.ps1` 运行 Go 测试/vet、编辑器/画布及恢复检查、前端类型检查/生产构建、Rust 测试、gateway 进程冒烟、`scripts/verify-lineage.ps1`（`sqlite3 .dump` 全链血缘与脱敏校验）和 `scripts/release.ps1 -DryRun`。测试数据保留于忽略提交的 `.runtime/`，便于检查数据库和日志。

真实桌面验证（需要可交互的 Windows 会话）从仓库根目录运行 `./scripts/smoke-desktop.ps1 -Mode release`；开发版使用 `-Mode debug`，先运行 `cargo build --manifest-path desktop/src-tauri/Cargo.toml` 并在另一终端启动 Vite。测试会打开并关闭本次创建的窗口，检查真实 WebView 健康请求、保存截图并确认 sidecar 回收。

生产版完整画布与原生重启检查使用 `./scripts/smoke-desktop.ps1 -Mode release -Smoke canvas`；普通素材导入恢复检查使用 `-Smoke m2`；分镜流水线与四路失败回流检查使用 `-Smoke pipeline`；15 秒口播视频与画布播放/血缘检查使用 `-Smoke video`；打断恢复、阶段取消与计费对账检查使用 `-Smoke interruptions`。丰富图形/文字/标注检查使用开发版 `-Smoke editor`、`-Smoke editor-product`，依赖 Vite，不能当作生产版检查。实际结果、协议适配及验证范围见 [全规格验收报告](docs/acceptance.md)、[里程碑记录](docs/milestones.md)、[契约文档](docs/contracts.md) 和 [签名发布指南](docs/releasing.md)。

## 图像与视频连接及生成

在“供应商设置”中保存图像（`openai-images`）或视频（`ark-video`）连接，填写本地或云端 Base URL、model ID、能力和估算价格来源。应用支持已知协议的连接配置，不把任意 HTTP API 当成兼容服务。免费模型列表探测不执行生成，也不证明模型能力已通过验收。

密钥仅在设置时输入，以 Windows DPAPI 加密保存在应用数据目录；读取配置不返回原密钥。MovieDesign 使用 BYOK，费用由供应商收取，不提供充值或钱包。未知价格会阻止生成，明确的零价也需确认卡批准。

选择项目后在画布或分镜流水线声明目标、提示词和参考图，经 7 门禁 Preflight 校验并点击请求费用确认，再在卡片批准或拒绝。15 秒口播配乐视频要求单一具备 `audio: true` 与 `15s` 时长声明的 `video:<id>` 能力，产物经 MP4 容器/音视频流与时长校验后落盘为画布可播放视频节点。区域标注可创建源尺寸 PNG 蒙版，图像修复沿用选中的源版本及 Resource。`ark-video` 排队阶段（`queued`）取消可调用远端 `DELETE` 撤回；运行阶段（`running`）取消仅停止本地自动投递，供应商可能继续处理和收费，迟到结果须另行通过采纳卡授权。下载/投递失败只能续收集原结果；未确定的生成结果不会自动重新提交。重启后的“确认原请求”使用原请求身份与内容。

原生协议验证使用 `./scripts/smoke-desktop.ps1 -Mode release -Smoke generation`、`-Smoke video`、`-Smoke interruptions`；设置与卡片分别使用 `-Smoke providers`、`-Smoke questions`。这些脚本使用隔离数据和本地协议 fixture；真实供应商调用及验收范围见 [全规格验收报告](docs/acceptance.md) 与 [里程碑记录](docs/milestones.md)。

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

- [全规格审计与端到端验收报告](docs/acceptance.md)
- [签名发布与更新策略指南](docs/releasing.md)
- [完整实施计划：M2–M7](docs/superpowers/plans/2026-10-15-moviedesign-complete.md)（自定义本地/云端供应商、30项任务与验收门槛）
- [原始实现规格](docs/spec.md)
- [已确认契约与待决项](docs/contracts.md)
- [里程碑验收记录](docs/milestones.md)
- [逆向资料索引](analysis-docs/README.md)

当前首个交付平台是 Windows x64；其他平台尚未验证。
