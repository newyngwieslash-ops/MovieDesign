# MiniMax Design 深度逆向分析 · 第三篇（收尾篇）

> 承接第一篇（架构与技术栈）、第二篇（gateway/agent/模型目录）。本篇覆盖第二篇 §11 列出的全部剩余项：opencode.exe 原生分析、21MB renderer 深度拆分、gateway 写操作边界、CDP 方案评估。
> 分析日期：2026-10-15 · 方法：PE 手工解析、Wakaru Bun 容器提取、分片恢复、只读探测

---

## 1. opencode.exe：不是原生程序，是 Bun 单文件可执行

**第二篇的预期（"需要 Ghidra/IDA"）被证伪**——零依赖检查就翻盘了：

1. `rea inspect-go-binary` → 无 Go build ID（不是 Go）
2. `rea inspect-rust-binary` → 无 `.dep-v0` 段（不是 Rust）
3. 手工 PE 解析（脚本级，非引擎）发现决定性证据：

```
PE32+ x64，12 个节区，链接时间 2026-05-12
.text(56.3M) .rdata(35.3M) .data(2.8M) ... .bun(77.2M)  ← 决定性
工具链标记：bun! · JavaScriptCore · WebKit（Bun 的 JS 引擎）
```

**结论**：opencode.exe = **Bun `--compile` 单文件可执行**——JS 代码 + Bun 运行时打包成一个 exe，`.bun` 节区存全部内容（77.2 MB）。所谓"原生分析"实际是**容器提取 + JS 分析**，比反汇编便宜一个量级。

### 1.1 容器提取（Wakaru bun extract）

1309 个文件全部无损提取（80.8 MB 内容）：

| 类别 | 数量 | 内容 |
| --- | --- | --- |
| JS 模块 | 332（`chunk-*.js` 1167 个文件中） | 入口 `src/index.js`（206 KB）+ 大量 chunk |
| 资源 | 977 | KaTeX 字体全套、代码高亮语言包、图标 SVG、提示音 mp3/aac、ghostty-web 终端（1.3 MB） |

manifest.json 关键字段（Bun 容器规范）：`entry_point_id: 47`（`B:/~BUN/root/src/index.js`）、`compile_exec_argv: "--user-agent=opencode/1.18.18 --use-system-ca"`、ESM 格式、无字节码（纯源码打包，可读）。

### 1.2 opencode 内部架构（源码级发现）

- **入口 worker 通信**：`src/cli/tui/worker.js` 是 TUI 的 worker 入口；`src/index.js` 是服务器入口——提取的 worker 代码显示其 RPC 面：`{ fetch, snapshot(heap), server, checkUpgrade, reload, shutdown }`，事件总线 `L.on("event")` → `X.emit("global.event")`，**鉴权注入模式**：无 Authorization 头时自动补 `_.header()`（客户端注入）
- **模型注册表内嵌**：3.3 MB 的 `chunk-wcfp2349.js` 含 **125 个 LLM 提供商**完整注册表（models.dev 快照格式）：anthropic、openai、google、deepseek、moonshotai、zhipuai、minimax、qwen、xiaomi、alibaba……每家含 API 端点、npm SDK（`@ai-sdk/*`）、模型清单与定价
- **核心服务 chunk**：3.7 MB 的 `chunk-62mppgjc.js` 提取自恢复版（11,182 行），Provider 引用 79 处、Tool 58 处、Session 29 处——服务注册中枢
- **tree-sitter**（语法高亮）、**LSP**、**KaTeX/astro** 等语言资产齐全——完整 agent 工作台
- **自动更新**：`OPENCODE_DISABLE_AUTOUPDATE`/`OPENCODE_ALWAYS_NOTIFY_UPDATE` 控制，patch 版静默自更、minor+ 通知

**工程启示**：Bun 单文件让 MiniMax 把一个完整开源 agent CLI（含 125 家模型注册表）作为**应用内私有组件**分发——无 Node 依赖、无安装步骤。`.bun` 提取后是纯 JS，全部可审计。

---

## 2. 21MB renderer：分片恢复攻克

**第二篇的"物理限制"结论被部分推翻**。Wakaru 直接吃 21MB 会 OOM（SIGABRT），但按**顶层语句边界分片**后每片可独立恢复：

```
策略：在列 0 语句起点（var|function|const|class|export|import|async）处切 6 片
      每片 2.7–5.0 MB → REA recover-javascript-sources 各自成功
结果：6/6 片 complete，43 个模块文件
```

**恢复成果：877 个命名类 + 11,440 个命名函数**。类层完整浮现（此前只有 minified 碎片）：

| 子系统 | 恢复出的类（观察事实） |
| --- | --- |
| **画布编辑器**（自研核心） | `Shape` 基类 + `ArrowShape/EllipseShape/FreePathShape/LineShape/MosaicShape/RectangleShape/TagShape/TextShape`；`Tool` 基类 + `BrushTool/DrawShapeTool/MosaicTool/SelectTool/TagTool/TextTool`；`Renderer/Editor/EventBus/HistoryManager`（撤销栈）；`WebGLBackend/WebGPUBackend/ImageColorGrading`（渲染后端） |
| **富文本**（ProseMirror 全套） | `Schema/Node/TextNode/Mark/Fragment/Slice/ResolvedPos/ContentMatch/NodeType/Attribute/MarkType/DOMParser/DOMSerializer/Step/StepMap/Mapping/AddMarkStep/RemoveMarkStep/ReplaceStep…`（TipTap 底层） |
| **React Query 层** | `MenuStore/PreviewCardStore/ChatDiagnostics/instances(BaseBackend)` |
| 表格解析 | `HtableParseError`（Excel/HTML 表格导入） |

**现在可以做的事**：`recovered-renderer-p0…p5/modules/` 里的代码可以直接读、可以直接改（配合第二篇的恢复产物）。画布编辑器的 Shape/Tool 类层次是学习自研画布架构的最佳材料。

**诚实边界**：分片恢复保住了类名与函数名，但跨片引用仍是 minified 导出名（`A1`/`z$5`），模块级来源映射只有 Wakaru 的 39 模块直拆版（第一篇）+ 这 43 个恢复模块可用做交叉对照。原始 TS 源文件名不可恢复（bundle 无 source map）。

---

## 3. Gateway 写操作边界（未执行任何写入）

### 3.1 鉴权模型实测（重要安全发现）

只读探测得到一个明确事实：**gateway 对本机 HTTP 请求没有请求级鉴权**。

```
GET  /api/v1/account/profile → 200 {"account":"139****","uid":"440***","user_name":"***"}
POST /api/skills/fork (空体) → 200 {"ok":false,"error":"name is required"}   ← 校验的是参数，不是身份
OPTIONS /api/upload → 204
```

**含义**：任何能访问 `127.0.0.1:8001` 的本机进程（含浏览器里恶意页面的 localhost 探测）都能以**当前登录用户身份**调用 gateway——包括 357 路里的全部写操作。唯一的自然边界是环回绑定（只听 127.0.0.1，实测 `netstat` 无 0.0.0.0 监听）。

**缓解因素（源码证据）**：
1. `tokenFromHeaders()` 支持从请求头取 token（`token:` 头或 `Authorization: Bearer`），带 token 的请求**可以**覆盖身份——但**不带也放行**（回退到进程态身份）
2. 高危写路径有自己的二次防线：`TrashService` 在生产环境缺 `HILO_MAIN_BRIDGE_TOKEN` 时**拒绝所有删除**（"REFUSE all deletes to avoid silent permanent deletion"——源码原话）；连接器安装要求用户在能力卡上显式操作（"Skill scripts cannot install this connector"——ForbiddenException）
3. 鉴权信息面：JWT（98 处引用）、Bearer、`x-hilo` 头族、device_id 从 JWT payload 提取

**风险定性（推断，供加固参考）**：这是"环回信任"模型的典型取舍——桌面应用普遍如此（VS Code 的本地 server 同病），但 357 路里含 `/api/pty`（伪终端）、`/api/files/*`（文件写删）、`/api/opencode`（agent 执行）等高权限面，若用户机器上有其他不可信进程或浏览器扩展可打 localhost，风险面比一般桌面应用大。**CSRF 缺失**（0 处引用）——本地端口不校验 Origin（浏览器表单可直接 POST）是这个模型下最现实的攻击面。

### 3.2 写操作面枚举（结构级，全部未执行）

按源码路由常量表 + 错误信息推断的写路径分组（未发过任何带有效参数的写请求）：

- **文件系统**：`files/{delete,mkdir,rename,move,copy,duplicate,writeContent,adopt,import-external}` + `files/upload-cdn`
- **上传事务**：`upload` → `upload/commit{,/abort,/finalize}` → `upload/staging/delete`
- **生成**：`generate/{image,video,music,speech,text}(/submit)` + `generation/cancel` + 20 路 `/edit/*` 媒体操作
- **项目**：`project/{compositions,resources}/commands(/batch)`、`operations/undo`
- **技能市场**：`market/{install,uninstall}`、`creator-plan/submit`、`fork`、`import`
- **系统**：`/api/pty`、`opencode/bridge`、`connectors/mcp/{authenticate,authorize}`、`permission/request`

---

## 4. CDP 运行时观察：方案就绪，未执行

**事实修正**：第一篇说"CDP 因 fuse 关闭做不了"不准确。fuse 表里 `EnableNodeCliInspectArguments: disabled` 关闭的是 **Node 侧 `--inspect`**；Chromium 侧的 `--remote-debugging-port` 是独立开关，大概率仍可用（未实测）。

**未执行的原因**：需要完全退出 8 个进程带参重启——应用当前**已登录**（gateway 实测返回账号信息），重启可能中断未保存的工作区状态。这是会话级侵入，应由你决定时机。

**执行方案**（任意时候可用）：

```powershell
# 1) 完整退出 MiniMax Design（托盘右键退出，确保 8 个进程全清）
# 2) 带调试端口启动（直接起 current 下的主程序，跳过 velopack hook 也行）
& "E:\AI_Rea_Things\MinimaxDesignThings1015\MiniMax Design\current\MiniMax Design.exe" --remote-debugging-port=9222
# 3) REA 运行时观察
npx -y rea-agents@latest list-electron-targets http://127.0.0.1:9222 --json
npx -y rea-agents@latest inspect-electron-page http://127.0.0.1:9222 <TARGET_ID> --observation-ms 100 --json
npx -y rea-agents@latest observe-javascript-runtime ...   # 脚本位置/执行上下文记录
```

---

## 5. 三篇分析总账

| 维度 | 覆盖 | 证据强度 |
| --- | --- | --- |
| 应用架构、IPC 协议（161 通道）、桥暴露面（9 全局/135 成员） | 篇一 | 源码+Evidence |
| Gateway 357 路 API、19 版 SQL、agent 配置、28 模型、18 技能、13 知识卡片 | 篇二 | 源码+活体 GET |
| opencode.exe = Bun 容器，1309 文件全提取，125 提供商注册表 | 本篇 | PE 解析+提取 |
| renderer 877 类/11,440 函数（分片恢复） | 本篇 | Wakaru 恢复 |
| gateway 无请求级鉴权（环回信任 + 无 CSRF） | 本篇 | 只读实测 |
| main bundle 233 类、上传链路、fuses、更新机制 | 篇一/二 | 恢复源码 |
| CDP 运行时 | 方案就绪，待你重启执行 | — |
| POST 写操作 | 面已枚举，零执行 | 结构级 |

**仍未做且已无低成本路径的**：真原生反汇编（现在已无必要——唯一的大原生物 opencode.exe 已是 JS）；更新链投毒面（nupkg 对比）；带参数的真实写请求（原则问题，永不做）。

---

## 6. 学习路径最终版

现在三份报告 + 全部恢复产物构成完整学习包：

1. **产品视角**：读篇二 §5（media-agent 提示词）→ §7（18 技能）→ 理解"AI 创作工作台"的产品设计
2. **架构视角**：读篇一 §4-7（进程/IPC/桥）→ 篇二 §2-4（gateway/数据库/opencode 档案）→ 理解四层架构（Electron 壳 → gateway → opencode → 云端）
3. **代码视角**：`recovered-main/modules/bundle.js`（DI 容器/凭据存储）→ `recovered-renderer-p*/modules/`（画布 Shape/Tool 类层、ProseMirror）→ `opencode-extracted/files/`（一个工业级 agent CLI 的全部源码）
4. **安全视角**：本篇 §3.1（环回信任模型）→ 篇一 §13（fuses）→ 思考"桌面 AI 应用的新攻击面"

---

## 附：第三篇新增产物

| 产物 | 位置 |
| --- | --- |
| opencode 全部内容（1309 文件，81 MB） | `opencode-extracted/`（manifest.json 记录入口/范围） |
| opencode 容器清单 | `opencode-extracted/manifest.json` |
| renderer 分片恢复源码（6 片 43 模块） | `recovered-renderer-p0…p5/modules/` |
| opencode 入口恢复 | `recovered-opencode-entry/modules/` |
| 鉴权探测记录 | （本篇 §3.1，脚本 `/tmp/probe-auth.mjs` 逻辑已记录） |

*第三篇完，三篇系列终。至此本应用的静态可分析面已全部覆盖；剩余项（CDP 实测、写操作）属于"需要你授权侵入"或"原则上不做"的类别。*
