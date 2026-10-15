# REA 使用指南（中文完整版）

> 适用版本：rea v6.5.0（本仓库 `package.json`）。官方英文文档以 `docs/index.md` 为入口；本文是其系统性中文导读 + 操作手册，面向"第一次接触 REA"到"日常使用"的完整路径。
>
> 本文是本地新增文档，不属于上游文档集；所有引用的相对链接均指向仓库内真实文件。

---

## 目录

1. [REA 是什么](#1-rea-是什么)
2. [五个核心概念（读懂再上手）](#2-五个核心概念)
3. [安装与准备](#3-安装与准备)
4. [分析引擎（Provider）配置](#4-分析引擎provider配置)
5. [CLI 命令面总览](#5-cli-命令面总览)
6. [MCP 工具面与会话](#6-mcp-工具面与会话)
7. [输出、退出码与大结果处理](#7-输出退出码与大结果处理)
8. [典型工作流配方（按目标类型）](#8-典型工作流配方)
9. [让 coding agent 用 REA 干活](#9-让-coding-agent-用-rea-干活)
10. [在本仓库做开发（源码视角）](#10-在本仓库做开发源码视角)
11. [安全与合规边界](#11-安全与合规边界)
12. [故障排查速查](#12-故障排查速查)
13. [官方文档地图](#13-官方文档地图)
14. [建议学习路径](#14-建议学习路径)

---

## 1. REA 是什么

**REA（Reverse Engineer Anything）= 一个 MCP 服务器 + 一个 CLI，两个入口共享同一套逆向分析工作流。**

- 你（或你的 coding agent）提出问题："这个功能是怎么实现的？给我证据。"
- REA 在**本地**检查目标（二进制、应用包、网站、数据库、抓包……），返回结论**附带证据（Evidence）**：观察了什么、推断了什么、还不知道什么。
- 你的 agent 用这些证据继续追问、解释行为、或照着实现一个复刻版本。

关键定位（摘自 [README](../README.md) 与 [AGENTS.md](../AGENTS.md)）：

- **local-only**：分析在本地运行，不上传目标文件（你的 agent 自身遵守其模型服务商的数据政策）。
- **evidence-first**：每条结论区分 `observed`（直接观察）/ `derived`（推导）/ `inferred`（推断），并声明局限（limitations）与未知（unknowns）。
- **不动目标**：Ghidra 用临时数据库、IDA 只读（绝不保存/关闭附着的 GUI 数据库）、静态 JS/.NET 分析根本不执行目标。运行时捕获类操作以你的用户权限运行，各指南会说明副作用。

三种典型用法：

| 用法 | 命令 | 场景 |
| --- | --- | --- |
| 让 agent 干活 | `npx rea-agents setup` 注册 MCP 后直接对话 | "看看这个 app 的搜索是怎么实现的" |
| 终端一次性检查 | `rea <命令> <目标> --json` | 脚本化、单独验证一个事实 |
| 开发/研究本仓库 | 本仓库源码 + 测试 | 改进 REA 本身 |

---

## 2. 五个核心概念

### 2.1 双入口，一套工作流

- **MCP 服务器**：`rea mcp`（stdio）。setup 会把它注册进你的 agent。有会话（session）概念：`open_binary` → 一系列查询 → `close_binary`，期间证据账本（ledger）驻留内存，后续调用可用 `{kind: "retained-evidence", evidence_id}` 引用而不必重跑。
- **CLI**：每个命令一个独立进程，无常驻会话；想复用结果就把它**存成文件**（Evidence JSON、快照），下一条命令读取。

### 2.2 Evidence（证据）

每条成功的观察产生一条规范化记录（`src/domain/evidence.ts`），包含：

- `evidence_id`：`ev_<64位hex>`，内容语义摘要，可校验；
- subject：目标路径、SHA-256、格式；
- provider 身份与 `analysis_profile`；
- `confidence`：`observed | derived | inferred`；
- `authority`：证据来源类别（如 `shipped-artifact`、`analyst-inference`）；
- `limitations[]`：这次观察**没覆盖到什么**；
- `raw_result` / `normalized_result`：原始与规范化两层结果。

配套的未知账本：`record_unknown` / `list_unknowns` / `update_unknown` —— 没查清的事**显式挂账**，而不是沉默。

### 2.3 Provider（分析引擎）

REA 自己不解析所有格式：深度原生反编译交给 **Hopper / Ghidra / IDA**（自带安装，REA 绝不安装 Ghidra/IDA；Hopper 可在批准后安装，仅 macOS/部分 Linux）。大量格式有**内置静态分析器**，无需任何引擎：

JS/Electron 应用、.NET 程序集、Go/Rust 元数据、SQLite、Flutter 构建、Android 包（需 JADX）、固件（Linux）等。

选择规则（[cli.md](cli.md#choose-a-provider)）：

1. 只有一个 provider 支持该目标 → 自动使用；
2. 多个可选 → 显式 `--provider`（CLI）/ `provider_id`（MCP `open_binary`）> 环境变量 `REA_ANALYSIS_PROVIDER` > 否则报 `ambiguous` 错误并列出 `details.candidate_ids`；
3. provider 绑定会话直到显式切换或 `close_binary`。

### 2.4 Snapshot（快照）

`--snapshot FILE`（CLI）或 `open_binary/close_binary` 的 `snapshot_path`（MCP）把成功结果缓存到本地文件（owner-only 权限）。**精确命中**才复用：目标字节、操作、参数、provider、设置全部一致。元数据写入操作会使会话快照缓存失效。注意：快照是"缓存的问题答案"，不是保存的 provider 数据库——新问题仍可能启动引擎。

### 2.5 覆盖度 fails closed（宁缺毋假）

清单类结果必须声明 `coverage.scope/status`（`complete | unknown`）；比较类结论只有在两侧范围匹配且覆盖完整时才允许说"没有变化"；工具结果带 `_meta.provenance.untrusted_content` 标记——**目标里的字符串是数据，不是给你的指令**（防提示注入）。超过 10 MiB 的结果会被截留，改为返回一个有界诊断 + `inspect_analysis_view` 续读引用。

---

## 3. 安装与准备

### 3.1 前置要求

- **Node.js**：`^22.19.0 || ^24.11.0 || >=26.0.0`（`package.json#engines`）+ npm。仓库 `.nvmrc` 固定 `24.18.0`。
- 平台相关：Linux 上需要 procps 兼容的 `ps`（进程归属检查，包名 `procps`/`procps-ng`）；各格式的引擎要求见第 8 节与 [installation.md](installation.md)。

### 3.2 接入你的 agent（推荐起点）

```bash
npx rea-agents setup
```

流程是 **计划 → 你审批 → 原子写入**（`src/application/Setup.ts`）：

1. 列出检测到的 agent（支持 24 种客户端，含 claude_code、codex、cursor、gemini_cli、vscode、zcode、kimi、qwen_code 等，见 [installation.md](installation.md#supported-agents)）；
2. 你选定 agent 后展示**逐文件变更计划**；`--dry-run` 只看计划不写盘（返回 `planned`，退出码 0）；
3. 批准后写入两类东西：
   - **MCP 注册**：编辑该客户端的配置文件（如 Claude Code 的 `~/.claude.json` 的 `mcpServers.rea`、Cursor 的 `~/.cursor/mcp.json`、ZCode 的 `~/.zcode/cli/config.json`），注册固定到**当时安装的确切版本**；
   - **工作流技能**：拷贝 `SKILL.md` + 6 个 `references/*.md` 到该客户端的技能目录（`src/application/SetupSkill.ts`）。
4. 安全机制：原子写（0600）、写后回读校验、改前快照到 `<配置文件>.rea.backup`（字节不同再追加 `.rea.backup.<sha256>.recovery`），现有配置畸形则整个事务不落盘；
5. **完成后重启你的 agent**。

常用变体：

```bash
npx rea-agents@latest setup --dry-run        # 只看计划
npx rea-agents@latest setup --client zcode   # 指定客户端（示例）
rea setup --install-hopper                   # 明确批准安装 Hopper（仅 macOS/部分 Linux）
```

### 3.3 安装 CLI

```bash
npm install --global rea-agents
rea --help
```

`rea` 与 `rea-agents` 是同一个分发器（`scripts/rea.mjs`）：`rea mcp` 进 MCP 服务器，`rea mcp doctor` 查 MCP 启动诊断，其余子命令进 CLI。

### 3.4 更新与卸载

```bash
rea update                    # 全局安装的更新
npx rea-agents@latest setup   # npx 用户：重新注册（会刷新技能与版本固定）
rea uninstall                 # 卸载（保留备份）
```

---

## 4. 分析引擎（Provider）配置

### 4.1 平台支持一览（Windows 用户重点看）

| Provider | 平台 | REA 是否安装 | 配置方式 |
| --- | --- | --- | --- |
| Hopper | macOS / 部分 Linux | 可批准后安装（Windows **不可用**） | `HOPPER_LAUNCHER_PATH` 覆盖发现路径 |
| Ghidra | Linux / macOS；**Windows 为实验性 P0**（只读，PE x86/x64 等，见 [windows-ghidra-p0.md](windows-ghidra-p0.md)） | **从不安装** | `GHIDRA_INSTALL_DIR` + `JAVA_HOME`（需 Ghidra 12.1.x + 64 位 JDK 21+） |
| IDA | Windows/macOS/Linux（attached GUI 或 headless，见 [ida-provider.md](ida-provider.md)） | **从不安装** | 你自备 `ida-pro-mcp` 上游插件，GUI 打开目标二进制 |
| JADX（Android） | Linux/macOS/Windows x64 | 从不安装 | 需完整 JDK |
| 内置静态分析器 | 全平台 | — | 无需配置 |

### 4.2 关键环境变量

| 变量 | 作用 | 默认 |
| --- | --- | --- |
| `GHIDRA_INSTALL_DIR` | Ghidra 安装目录（显式值优先，即使无效也采纳） | unset 时 doctor 在 PATH 找 `ghidra`/`ghidraRun` |
| `JAVA_HOME` | Ghidra 所需 JDK | — |
| `REA_GHIDRA_STARTUP_TIMEOUT_MS` | Ghidra 启动时限 | 330000 |
| `REA_HOPPER_STARTUP_TIMEOUT_MS` | Hopper 启动时限 | 120000 |
| `REA_ANALYSIS_PROVIDER` | 常备 provider 偏好 | —（显式 `--provider` 优先） |
| `REA_JEB_MCP_URL` | 已启动的 JEB MCP 客户端地址 | — |

PowerShell 示例（Windows 配 Ghidra）：

```powershell
$env:GHIDRA_INSTALL_DIR = "D:\ghidra_12.1.4_PUBLIC"
$env:JAVA_HOME = "D:\jdk-21"
$env:REA_GHIDRA_STARTUP_TIMEOUT_MS = "600000"
```

### 4.3 体检：`rea doctor`（严格只读）

```bash
rea doctor --json                       # 全量：所有注册、技能、引擎
rea doctor --provider ghidra --json     # 只查一个引擎
rea doctor --client claude_code --json  # 只查一个 agent 注册
rea doctor --skill --json               # 只查技能
```

报告分两层：`scope_checks` 决定 `healthy` 与退出码；其余为 `informational_checks`（无关的可选项不可用不算失败）。

### 4.4 能力查询

```bash
rea providers --json     # 可用 provider 及支持的目标
rea capabilities --json  # 辅助能力
```

---

## 5. CLI 命令面总览

规范清单在 `src/cliCommandNames.ts`：**154 个主命令** + 1 个别名（`compare-bundles`）+ 一批 MCP 工具名直通别名。按域分组：

| 域 | 代表命令 | 说明 |
| --- | --- | --- |
| **会话/原生总览** | `analyze`、`inspect`、`decompile`、`function`、`instructions`、`search`、`xrefs`、`trace` | 原生分析核心动词（需 provider） |
| **原生细查** | `annotate-native-function`、`inspect-native-data-type`、`read-bytes`、`search-bytes`、`address-to-file-offset`、`inspect-native-instruction`、`resolve-native-call-targets` | 地址/字节/类型级操作 |
| **二进制诊断** | `inspect-binary-layout`、`inspect-recorded-crash`、`inspect-go-binary`、`inspect-rust-binary`、`inspect-pe-resources`、`inspect-macho`、`inspect-signature`、`inspect-plist`、`demangle-swift` | 多数无需原生引擎 |
| **调试/插桩** | `debug-with-gdb`、`debug-with-rizin`、`list-frida-devices`、`instrument-with-frida`、`cutter-command`、`inspect-with-objdump` | 依赖外部工具 |
| **JS/Electron 应用** | `analyze-javascript-application`、`recover-javascript-sources`、`trace-application-feature`、`compare-application-versions`、`reconcile-javascript-runtime`、`compare-source-to-bundle` | 全静态或运行时观察 |
| **浏览器/Web** | `list-browser-targets`、`inspect-web-page`、`observe-web-session`、`capture-browser-scenario`、`export-web-scripts`、`trace-web-source-location`、`inspect-web-network-capture` | 走 CDP 的 Chrome 系浏览器 |
| **Electron 专项** | `list-electron-targets`、`inspect-electron-page`、`inspect-electron-fuses`、`capture-electron-scenario` | — |
| **Android** | `inspect-android-package`、`search-android-classes`、`inspect-android-method`、`trace-android-references`、`decode-android-resources` | 需 JADX / Apktool |
| **ADB 设备** | `inspect-adb-client`、`list-adb-devices`、`pull-adb-package`、`read-adb-logcat`、`capture-adb-screen`、`install-adb-package`、`start-adb-app` 等 ~20 个 | 需自备 adb |
| **JEB / Flutter** | `open-jeb-project`、`decompile-jeb-item`、`identify-flutter-build`、`inspect-dart-aot` | — |
| **托管代码 (.NET)** | `inspect-managed-artifact`、`inspect-managed-members`、`inspect-managed-native-boundaries`、`compare-managed-members` | 静态，无引擎要求 |
| **工件/资源** | `inspect-artifact`、`extract-artifact`、`decode-interface-builder`、`inspect-keyed-archive`、`inspect-asset-catalog`、`trace-dylib-resolution` | Apple 包解剖等 |
| **格式检查器** | `inspect-sqlite-database`、`inspect-wasm-artifact`、`inspect-evm-interface`、`inspect-firmware-regions`、`extract-firmware` | — |
| **抓包** | `inspect-packet-capture`、`inspect-packet-fields`、`inspect-pcap-capture`、`decode-signal-recording` | Linux：TShark 等 |
| **进程行为** | `capture-process`、`compare-process-captures` | Linux/macOS 原生 PTY |
| **证据管理** | `evidence-import`、`evidence-export`、`compare`、`inspect-evidence-bundle`、`inspect-analysis-view` | 第 7 节详解 |
| **原生 UI 观察** | `observe-native-ui`、`capture-native-ui-scenario`、`trace-native-ui-action` | — |
| **维护** | `setup`、`doctor`、`update`、`uninstall`、`providers`、`capabilities`、`import-reference-source` | — |

> 完整机读目录（命令↔MCP 工具映射）见生成的工具目录：[mcp-contracts.md](mcp-contracts.md#generated-catalog)。

---

## 6. MCP 工具面与会话

约 **201 个 MCP 工具**，按 33 个契约组注册（`src/contracts/toolContracts.ts`）。在 CLI 之外多出的主要是**交互式会话工具**（`MCP_TOOLS_WITHOUT_DEDICATED_CLI`，如 `goto_address`、`comment`、`list_bookmarks`、`batch_decompile`、`trace_call_path`、`start_gdb_session`、`start_frida_session` 等）。

会话生命周期（原生分析）：

```
open_binary {path, provider_id?, snapshot_path?}
  → binary_session { }            # 查询会话状态与 tool_availability
  → 各类查询（list_procedures、procedure_pseudo_code、trace_feature …）
  → close_binary {snapshot_path?, overwrite?}   # 可选先存快照
```

- 每次 MCP 连接有独立证据账本；`close_binary` 清空。
- 结果超预算（10 MiB stdio 帧）会被截留，用 `inspect_analysis_view` 分页续读。
- 6 个引导式 prompt：`investigate_feature`、`compare_application_versions`、`verify_reconstruction`、`trace_crash`、`audit_residual_unknowns`、`prepare_bounded_process_capture`（见 [mcp-prompts.md](mcp-prompts.md)）。

---

## 7. 输出、退出码与大结果处理

### 7.1 输出格式

- 终端默认 **TOON** 格式；`--json` 输出 JSON（终端上缩进，管道/重定向时紧凑——对 agent 和脚本友好）。
- 输出选择标志（`--format`、`--filter-output`、`--token-limit`、`--token-offset`）不影响操作成败。
- 脚本里建议 `set -o pipefail`，避免 `rea ... | jq` 吞掉失败状态。

### 7.2 退出码

| 码 | 含义 |
| --- | --- |
| `0` | 操作完成（结果可能仍带警告/未知项） |
| `1` | 操作未完成（结构化输出说明原因） |
| `128+N` | 信号结束（SIGINT→130、SIGTERM→143） |
| `141` | 下游管道提前关闭（`| head`），操作本身可能已完成 |
| 特例 | `setup --dry-run` 恒 0；`doctor` 必查项失败为 1；`setup` 被取消为 0 |

### 7.3 大结果：存文件 + 投影

应用级 Evidence 可能数百 MB。标准姿势：

```bash
# 1) 存完整证据
rea analyze-javascript-application "D:/apps/example" --json > app-evidence.json

# 2) 投影出 ~10 KB 摘要
jq -c '{source:{kind:"inline",evidence:.},view:{kind:"summary"}}' app-evidence.json > app-view.json
rea inspect-analysis-view app-view.json

# 3) 或按页翻模块清单
jq -c '{source:{kind:"inline",evidence:.},view:{kind:"page",collection:"modules",offset:0,limit:32}}' app-evidence.json > page.json
rea inspect-analysis-view page.json
```

（文档实测：334 MB 的 Obsidian 应用证据 → 约 10 KB 摘要视图。）

### 7.4 证据可携带

```bash
rea evidence-import bundle.json                                   # 校验
rea evidence-export bundle.json canonical.json                    # 导出规范形
rea compare left.json right.json                                  # 比较两份证据
```

---

## 8. 典型工作流配方

> 目标路径一律用**绝对路径**。Windows 下 PowerShell 引号包裹。

### A. JS / Electron 应用（零依赖，推荐第一次就跑这个）

```bash
# Electron 应用：找 resources/app.asar 或解包后的目录
rea analyze-javascript-application "C:/Users/<你>/AppData/Local/Programs/<应用>/resources/app.asar" --json > ev.json
```

得到：模块清单、导入关系、Electron 边界（main/preload/renderer、IPC）及其证据。深入见 [javascript-application-workflows.md](javascript-application-workflows.md) 与 [javascript-artifact-reconstruction.md](javascript-artifact-reconstruction.md)。

### B. 网站（需 Chrome 系浏览器）

```bash
rea list-browser-targets --json        # 附着的浏览器标签页
rea inspect-web-page <target> --json   # 页面结构、脚本、网络观察
rea capture-web-screenshot <target> --json
```

见 [browser-observation.md](browser-observation.md)、[browser-scenario-contract.md](browser-scenario-contract.md)。

### C. .NET 程序集（零依赖）

```bash
rea inspect-managed-artifact "D:/app/Assembly.dll" --json
rea inspect-managed-members "D:/app/Assembly.dll" --type Example.Parser --json
```

只读元数据与 CIL，不执行目标；不支持 .NET 单文件宿主解包与 IL2CPP。见 [managed-code-analysis.md](managed-code-analysis.md)。

### D. Go / Rust 二进制元数据（零依赖）

```bash
rea inspect-go-binary "D:/bin/program.exe" --json    # 编译器版本、模块、构建设置
rea inspect-rust-binary "D:/bin/program.exe" --json  # cargo-auditable 依赖清单
```

注意：Rust 无 `.dep-v0` 段 = 元数据缺失，**不是**"没有依赖"。

### E. SQLite（零依赖；Node 需 ≥24.15 或 26+）

```bash
rea inspect-sqlite-database "D:/data/app.db" --json
rea inspect-sqlite-database "D:/data/app.db" --table users --row-limit 20 --json
```

### F. 原生二进制深度分析（需 provider）

```bash
rea analyze "D:/bin/target.exe" --provider ghidra --json          # 总览
rea search "D:/bin/target.exe" "login" --provider ghidra --json   # 字符串/过程搜索
rea function "D:/bin/target.exe" main --provider ghidra --json    # 函数档案
rea decompile "D:/bin/target.exe" 0x1000 --provider ghidra --json # 伪代码
rea xrefs "D:/bin/target.exe" 0x1000 --provider ghidra --json     # 交叉引用
rea trace "D:/bin/target.exe" "login" --provider ghidra --json    # 特性追踪
```

格式/主机支持差异见 [native-investigation.md](native-investigation.md)；慢机器先调大启动超时（第 4.2 节）。

### G. Android

```bash
rea inspect-android-package "D:/apk/target.apk" --json     # 需 JADX + JDK
rea search-android-classes "D:/apk/target.apk" "crypto" --json
rea list-adb-devices --json                                # 需自备 adb
rea pull-adb-package --device <id> --package com.example.app --json
rea read-adb-logcat --device <id> --json
```

见 [android-analysis.md](android-analysis.md)、[adb-device-analysis.md](adb-device-analysis.md)。

### H. 抓包与网络

```bash
rea inspect-web-network-capture "D:/captures/session.har" --json   # HAR 或 Fiddler SAZ
rea inspect-pcap-capture "D:/captures/trace.pcap" --json           # Linux：TShark 4.x
```

见 [web-network-captures.md](web-network-captures.md)、[pcap-analysis.md](pcap-analysis.md)。

### I. 进程行为（Linux/macOS）

```bash
rea capture-process -- "D:/tool.exe" --some-flag --json
rea compare-process-captures run1.json run2.json --json
```

见 [process-capture.md](process-capture.md)。

---

## 9. 让 coding agent 用 REA 干活

setup 完成并重启 agent 后，直接用自然语言下任务。内置的 `reverse-engineer-anything` 技能（`.agents/skills/reverse-engineer-anything/SKILL.md`）会引导 agent：

1. **只在需要时用 REA**：问题依赖"已交付的二进制/包/运行时证据/版本对比"才动用；普通源码仓分析直接读代码；
2. **按目标路由到正确的第一工具**（技能里有完整路由表：JS/ASAR、APK、.NET、固件、原生 `open_binary`、浏览器、抓包、Go/Rust、SQLite、WASM、EVM……）；
3. **摘要优先、分页取证**：不重复跑相同的调用，用 Evidence 视图与续读；
4. **维护发现台账**：观察/推断/未知分开记录并附 Evidence ID；
5. **结束关闭原生会话**（`close_binary`）。

MCP prompt 是现成的任务模板（agent 里可调用）：`investigate_feature`（功能调查）、`compare_application_versions`（版本对比）、`verify_reconstruction`（复刻验证）、`trace_crash`（崩溃追溯）、`audit_residual_unknowns`（未知项审计）、`prepare_bounded_process_capture`（有界进程捕获）。

示例任务（README 官方示例）：

```text
Understand how search works in the Notes app, show me the evidence, and build a
similar feature for my project.
```

---

## 10. 在本仓库做开发（源码视角）

> 你当前的工作副本：v6.5.0，Node v24.16.0（满足 `^24.11.0`），**尚未 `npm ci`、尚未构建**——此刻直接跑 `node scripts/rea.mjs` 会提示"compiled runtime is missing"。作为使用工具，直接 `npx rea-agents` 用发布版即可；要读码/改码再构建本地。

### 10.1 构建

```bash
npm ci                    # 安装锁定依赖
npm run build:cached      # 构建 runtime 与打包技能（turbo 编排）
node scripts/rea.mjs --help   # 本地 CLI 验证
npm start                 # 本地 MCP 服务器（stdio）
```

### 10.2 检查矩阵

| 命令 | 用途 |
| --- | --- |
| `npm run test:local -- PATH...` | 源码测试，无需构建（最快） |
| `npm run test:focused -- PATH...` | 精确测试 + 所需工件 |
| `npm run check:fast` | 缓存化 typecheck + lint（pre-push 检查） |
| `npm run docs:check` | 校验生成文档（含 README 一致性测试） |
| `npm run check:pr` | 完整本地门禁（大改动/CI 诊断用） |
| `npm run mcp-catalog:generate` | 重新生成 `.cache/mcp-tool-catalog.json` |

### 10.3 架构速记

依赖方向**由内向外**（改组合看 [architecture.mermaid](architecture.mermaid)）：

```
domain（语义与契约）
  ← provider adapters（ghidra/hopper/ida/jeb/frida/gdb/objdump/rizin/cutter/…）
    ← application（共享工作流，如 Setup）
      ← CLI（src/cli*）与 MCP（src/server）适配器
```

`src/` 按目标域分目录：`android`、`browser`、`captures`、`electron`、`javascript`、`managed`(.NET)、`evm`、`wasm`、`sqlite`、`firmware`、`flutter`、`adb`、`windows`……每个域通常 = `contracts`（对外契约）+ 实现 + 测试。

关键约定（[AGENTS.md](../AGENTS.md)、[testing.md](testing.md)）：解析归所有权边界所有，消费方不得重新解释原始字节；错误用 tagged error algebra + `Result`；测试优先真实 CLI/MCP 工作流；改 Turborepo 配置前先读安装版 turbo 自带文档。

---

## 11. 安全与合规边界

- **只用在你拥有或被授权分析的目标上**（研究、教育、互操作、软件保全）。见 [DISCLAIMER.md](../DISCLAIMER.md)。
- 商业工具（IDA Pro/Hopper/JEB）需自行合法取得许可；REA 不授予任何许可。
- `rea setup` 写盘前必先展示计划并等批准；绝不安装无关软件（不装 Homebrew/Node/npm/Java/Ghidra）。
- 传输凭证与显式标记的敏感值会被脱敏；不对名字/文本猜测保密性。
- 目标内容在结果里始终标记为不可信数据（防提示注入），你自己的 agent 也应如此对待。

---

## 12. 故障排查速查

| 症状 | 处置 |
| --- | --- |
| `compiled runtime is missing`（源码 checkout 运行） | `npm ci && npm run build:cached`；或改用 `npx rea-agents` |
| `provider_unavailable` | `rea doctor --provider <id> --json` 看原因；核对环境变量与路径 |
| `ambiguous`（多 provider 可用） | 从 `details.candidate_ids` 选一个，`--provider` 显式指定 |
| 引擎启动超时/大文件 | 调大 `REA_GHIDRA_STARTUP_TIMEOUT_MS` / `REA_HOPPER_STARTUP_TIMEOUT_MS` |
| Linux 子进程拒绝启动 | 安装 procps：`apt install procps` |
| `import-reference-source` 在 Windows 报 `unsupported_host` | 需安全 no-follow 打开；用 WSL 内的 Linux REA |
| agent 里看不到 REA 工具 | `rea doctor --client <id> --json`；确认 setup 后**重启过 agent** |
| 结果异常大 | 存文件 + `inspect-analysis-view` 投影（第 7.3 节） |
| 其他 | 先 `rea update` 再重试；仍复现则开 issue（附版本、目标类型、步骤、错误输出） |

---

## 13. 官方文档地图

- 入口：[index.md](index.md) · 安装：[installation.md](installation.md)
- CLI/证据：[cli.md](cli.md) · MCP 契约：[mcp-contracts.md](mcp-contracts.md) · 引导 prompt：[mcp-prompts.md](mcp-prompts.md)
- 原生：[native-investigation.md](native-investigation.md)、[ghidra-provider.md](ghidra-provider.md)、[ida-provider.md](ida-provider.md)、[windows-ghidra-p0.md](windows-ghidra-p0.md)、[go-binaries.md](go-binaries.md)、[rust-binaries.md](rust-binaries.md)、[binary-diagnostics.md](binary-diagnostics.md)、[recorded-crashes.md](recorded-crashes.md)
- JS/Web：[javascript-application-workflows.md](javascript-application-workflows.md)、[javascript-artifact-reconstruction.md](javascript-artifact-reconstruction.md)、[javascript-recovery.md](javascript-recovery.md)、[browser-observation.md](browser-observation.md)、[web-runtime.md](web-runtime.md)、[electron-observation.md](electron-observation.md)、[electron-fuses.md](electron-fuses.md)
- 移动/嵌入式：[android-analysis.md](android-analysis.md)、[adb-device-analysis.md](adb-device-analysis.md)、[apktool-resource-analysis.md](apktool-resource-analysis.md)、[jeb-analysis.md](jeb-analysis.md)、[flutter-analysis.md](flutter-analysis.md)、[apple-application-analysis.md](apple-application-analysis.md)、[harmony-analysis.md](harmony-analysis.md)、[firmware-analysis.md](firmware-analysis.md)
- 数据/格式：[sqlite-databases.md](sqlite-databases.md)、[wasm-artifacts.md](wasm-artifacts.md)、[evm-bytecode.md](evm-bytecode.md)、[managed-code-analysis.md](managed-code-analysis.md)
- 网络/运行时：[web-network-captures.md](web-network-captures.md)、[pcap-analysis.md](pcap-analysis.md)、[packet-captures.md](packet-captures.md)、[signal-recordings.md](signal-recordings.md)、[process-capture.md](process-capture.md)、[frida-instrumentation.md](frida-instrumentation.md)
- 开发：[testing.md](testing.md)、[tool-design.md](tool-design.md)、[releasing.md](releasing.md)、[roadmap.md](roadmap.md)

---

## 14. 建议学习路径

**第 1 步（今天，10 分钟）**：不装任何东西，验证 CLI 可用：

```bash
npx -y rea-agents@latest --help
```

**第 2 步（第一个真实结果）**：挑一个你机器上的 Electron 应用（VS Code、聊天工具等），跑零依赖的 JS 应用分析并存证据，再用 `inspect-analysis-view` 投影摘要（第 8.A 节）。

**第 3 步（接入 agent）**：`npx rea-agents@latest setup --dry-run` 看计划 → 批准 → 重启 agent → 用一个 MCP prompt 模板下一个真实调查任务。

**第 4 步（按需深入）**：有原生逆向需求时再配 provider——Windows 上可选 IDA（自备）或实验性 Ghidra P0；先跑 `rea doctor --provider <id> --json` 确认就绪。

**第 5 步（读懂设计）**：按顺序读 [cli.md](cli.md) → [mcp-contracts.md](mcp-contracts.md)（Evidence/会话语义）→ [tool-design.md](tool-design.md)（为什么这样设计）→ 对照 `src/contracts/` 与 `src/domain/evidence.ts` 验证理解。

**第 6 步（动手改）**：`npm ci && npm run build:cached`，跑 `npm run test:local -- src/config.test.ts` 之类的小测试熟悉流程，再从 roadmap 挑小任务。
