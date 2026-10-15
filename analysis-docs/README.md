# MiniMax Design 逆向分析 · 总索引

> 分析对象：MiniMax Design v4.0.0（内部代号 **hilo**，`@hilo/desktop`）Windows x64 桌面应用
> 分析周期：2026-10-15（单日完成）· 方法：REA 6.5.0 静态分析 + Wakaru 反编译 + 只读运行时探测
> 本文件夹是全部八份文档的入口，按编号即阅读顺序。

---

## 文档地图

| # | 文档 | 回答的问题 | 深度 |
| --- | --- | --- | --- |
| 00 | [REA工具使用指南](00-REA工具使用指南.md) | 这次分析用的工具怎么用 | 工具手册 |
| 01 | [架构与技术栈](01-架构与技术栈.md) | 这个应用是什么、怎么组织 | 系统级 |
| 02 | [系统实现篇](02-系统实现篇-gateway与agent.md) | gateway 357 路 API、agent 配置、模型目录、技能市场的内部实现 | 实现级 |
| 03 | [深度恢复篇](03-深度恢复篇-opencode与renderer.md) | opencode.exe 是 Bun 容器（1309 文件全提取）、renderer 877 类恢复、鉴权实测 | 源码级 |
| 04 | [画布编辑器专题](04-画布编辑器专题.md) | 自研 Canvas 编辑器（8 Shape/8 Tool/四层渲染/快照历史）逐行精读 | 源码级 |
| 05 | [剧本到视频流水线专题](05-剧本到视频流水线专题.md) | 剧本→分镜→视频的五层流水线、门禁体系、任务状态机 | 流程级 |
| 06 | [复现差距分析](06-复现差距分析.md) | 用其他技术栈复现还缺什么、怎么换栈、路线图 | 复现导向 |
| 07 | [补齐篇](07-补齐篇-七类空白实证.md) | 06 列的七类空白全部补齐（插件协议/更新策略/聊天桥/埋点…） | 收官 |
| 08 | [Codex复现提示词](08-Codex复现提示词-Tauri-Go-opencode.md) | 用 Tauri+Go+opencode 复现的完整工程规格（可直接喂给 Codex） | 交付物 |

**快速路径**：
- 只有 10 分钟 → 读 01 的执行摘要 + 05 的全景图
- 想复现 → 06 → 07 → 按需回查 02/04/05
- 想学架构 → 01 → 02 → 03 → 04
- 想学 AI 产品设计 → 05（全文）+ 02 的 §5 提示词解读

---

## 关键事实速查卡

```
产品      MiniMax Design（hilo）= 设计工作台 + 本地 AI Agent 基础设施
四层架构  Electron 壳(161 IPC 通道) → gateway :8001(357 路 API) → opencode :4096(MCP agent) → MiniMax 云
agent     opencode v1.18.18（Bun 单文件，内嵌 125 家 LLM 注册表）
          media-agent：29 个 design_* 工具、原生文件操作全禁、3,500 字中文系统提示词
模型      28 个在线后端（H3/seedance/kling/veo3/midjourney-8.2/seedream…），统一 capability 协议
对象模型  Composition(创作对象) → Resource(内容身份) → Version(具体内容)，SQLite 19 版迁移
技能      18 个 SKILL.md（成文方法论：门禁/确认卡/失效级联/整条重生成）
编辑器    自研 Canvas 2D（3,000 行核心：8 Shape + 8 Tool + 交互事务 + 快照历史）
更新      Velopack 整包（feed 白名单防投毒）+ 海外版渲染层热更新（SHA-256 manifest）
安全      contextIsolation✓ IPC 白名单✓ 但 asar 完整性 fuse 未开；gateway 无请求级鉴权（环回信任+无 CSRF）
```

---

## 分析产物清单（本文件夹之外，`MinimaxDesignThings1015\` 下）

| 目录/文件 | 内容 | 大小 |
| --- | --- | --- |
| `minimax-design-evidence.json` | REA 完整应用证据（5,621 节点图，ev_bf3f349a…） | 236 MB |
| `extracted/` | app.asar 解包源码（main/preload/renderer 全部） | 87 MB |
| `recovered-main/modules/bundle.js` | main bundle 恢复源码（**233 类**，211K 行） | ~10 MB |
| `recovered-renderer-p0…p5/` | renderer 分片恢复（**877 类 / 11,440 函数**） | ~25 MB |
| `recovered-v3/` | preload 三件套恢复源码 | ~1 MB |
| `opencode-extracted/` | opencode.exe Bun 容器提取（**1309 文件**，manifest.json 记录入口） | 81 MB |
| `wakaru-renderer-direct/` | 21MB renderer 直拆（39 模块） | 22 MB |
| `recovery-*.json` | 各次恢复的 REA 证据记录 | — |
| `gateway-probe.json` | 27 路由只读探测结果 | 小 |
| `trace-upload-result.json` | /api/upload 特性追踪证据 | 30 MB |
| `tools/` | 本地 npm 装的 wakaru 1.14.0 | — |
| WSL Ubuntu 24.04 | `~/node24`（Node 24.11）、`~/bin/wakaru`（Linux 版，REA 恢复通道需要） | — |

**目标本体**：`MiniMax Design\`（原安装目录，勿动）、`MiniMax Design Data\`（用户数据，含 SQLite）。

---

## 分析方法备忘（复用这套流程）

1. **识别**：`npx rea-agents inspect-go-binary / inspect-rust-binary`（零依赖排除 Go/Rust）→ PE 手工解析找 `.bun` 节区 → `wakaru bun extract` 拆容器
2. **建图**：`rea analyze-javascript-application <asar> --json` 存证据 → `inspect-analysis-view` 投影摘要/分页
3. **反编译**：REA `recover-javascript-sources`（需 Linux x64 + Wakaru，Windows 走 WSL）；超大 scope-hoisted bundle 按**列 0 顶层语句边界分片**再各片恢复
4. **深挖**：`trace-application-feature`（特性追踪）、`inspect-electron-fuses`（fuse 实测）、gateway 只读 GET 探测
5. **纪律**：只读原则（POST 一律不带有效参数）、每份文档标注观察/推断/不可补边界

---

## 诚实边界（最终声明）

- 静态分析覆盖：本机可分析面已全部读完（八篇文档 + 全部落盘产物）
- **永不可补三项**：云端服务端实现（有 95% 客户端契约规格书）、CI/签名配置（官方仓库）、模型本身（API 客户）
- 运行时 CDP 观察未执行（需带 `--remote-debugging-port` 重启应用，方案见 03 §4）
- 全部 POST 写操作零执行（只读原则）；技能/模型/插件清单为当时活体快照，会随更新漂移
