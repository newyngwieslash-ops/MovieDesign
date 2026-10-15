# MiniMax Design 深度逆向分析 · 第二篇

> 承接 `MiniMax-Design-Analysis.zh.md`（第一篇：架构与技术栈）。本篇覆盖第一篇未触及的深度：源码恢复、gateway 完整 API 面、agent 配置体系、模型目录活体数据、技能市场。
> 分析日期：2026-10-15 · 方法：Wakaru 反编译（REA recover-javascript-sources）、bundle 符号提取、gateway 只读探测（GET）

---

## 目录

1. [源码恢复成果](#1-源码恢复成果)
2. [Gateway：完整 357 路 API](#2-gateway完整-357-路-api)
3. [Gateway 数据库：19 版迁移的资产系统](#3-gateway-数据库)
4. [Agent 配置体系（opencode 档案）](#4-agent-配置体系)
5. [media-agent 系统提示词全文解读](#5-media-agent-系统提示词)
6. [模型目录（活体数据）](#6-模型目录活体数据)
7. [技能市场（18 个捆绑 Skill）](#7-技能市场)
8. [插件体系（3 个捆绑插件）](#8-插件体系)
9. [模型知识库（vendor 卡片）](#9-模型知识库)
10. [修正与补充第一篇的结论](#10-修正与补充)
11. [剩余未覆盖项（诚实边界）](#11-剩余未覆盖项)

---

## 1. 源码恢复成果

用 REA + Wakaru 1.14.0（WSL Ubuntu 24.04 内的 Linux 版，因 REA 的 Wakaru 通道仅限 Linux x64 + prlimit）对三个 bundle 做了反编译恢复：

| Bundle | 大小 | 恢复结果 | 质量 |
| --- | --- | --- | --- |
| main `index-WiZKxTAT.js` | 9.9 MB | 211,734 行单文件（结构性恢复，esbuild 模块拼接） | **233 个命名类完整还原**，含完整类名/方法名/错误类 |
| preload 三件套 | 418 KB | 3 模块全恢复（v3-workbench 3,754 行重排） | 常量、参数协议完整 |
| renderer v3 | 1.7 MB | 104,406 行单文件 | 134 个组件函数还原（但 minified 名如 `U1Component`） |
| renderer v1 | 21 MB | 仅拆出 39 模块（主体 2.2 万行单文件，Wakaru 内存受限 SIGABRT×1 后直跑成功） | scope-hoisted bundle，模块边界丢失是打包方式的物理限制 |

**恢复产物位置**：`recovered-main/modules/bundle.js`、`recovered-v3/modules/`、`wakaru-renderer-direct/`（39 模块）、`recovery-*.json`（REA 证据）。

**main bundle 233 类的架构还原**（这是本次恢复的最大价值——第一篇只能看到符号，现在能看到类关系）：

```
DI 容器层（VS Code 同源模式）
  ServiceCollection → InstantiationService → SyncDescriptor
  CyclicDependencyError（依赖环检测）

存储层
  GlobalStorage · AssetIndexStore · AccountStore(+Limit/Duplicate 错误)
  BindingStore · CredentialStore(+Corrupt/Permission/Cipher 错误族)
  DatabaseBrowserService · WorkspaceLocateRequests · WorkspaceEntryBudget

IPC/协议层
  ChannelServer/ChannelClient · IPCServer · Protocol
  ValidatedIpcMain（通道白名单的实现类） · InboundMessageFilter
  MainBridgeServer · ImBridge*（IM 桥接错误族——飞书/Telegram 集成）

Agent 编排层
  OpenCodeRestartDeferredError / OpenCodeRestartPartialFailureError
  GatewayReadinessService · TeamAccountCoordinator · TeamDataInvalidationService
  TeamOperationCoordinator · FifoAdmissionQueue（工作区准入队列）
  SuspendRefusalBackoff · MaxOpenWorkspacesResolver · UnconfirmedTerminationTracker

资产生命周期
  AssetCenterMainService · HcpManifestError · UpdaterMainService
  RequestMainService · TrashService · WindowsLoginItemCleaner

可观测性
  DesktopLogService · RendererErrorHandler · ErrorHandler · EventLogger
  FileRegistry · Buffering · Logger（分层日志）
```

**有据推断**：`AccountStore`/`BindingStore`/`CredentialStore` 的错误类族（Limit/Duplicate/Corrupt/Cipher）表明凭据存储有容量上限、去重和加密信封设计——与 fuses 里 `EnableCookieEncryption: disabled` 相对，应用在**应用层**自己做了凭据加密（`createDecipheriv/createCipheriv/pbkdf2Sync` 都在导入清单里）。

---

## 2. Gateway：完整 357 路 API

第一篇只列了 main 进程里调用的 27 个 `/api/` 路径。gateway 自己的 bundle（`resources/gateway/dist/main.js`，20.2 MB）里注册了**357 个路由**。按功能域分组（全部提取自源码，观察事实）：

**生成流水线（核心域，~80 路）**
- `POST /api/generate/{image,video,music,speech,text}` + `/submit` 异步版 + `/api/generate/tasks/:task_id/query`
- 厂商专线：`/api/v1/video/{minimax-v3,kling,kling-omni,veo3,seedance,wan,wan3,mediakit,avatar}(/generate|/tasks)`、`/api/v1/image/{kling,midjourney,nano_banana,seedream}/*`、`/api/v2/audio/{tts,music,voice_clone,voice_design,voice_isolation}/*`
- 模型目录：`/api/models{,/image,/video,/music,/speech}`、`/api/runtime/models`

**媒体编辑（/api/edit/*，~20 路）**
`analyze-media`、`asr`、`asr-whisper`、`asr-mediakit`、`concatenate-videos`、`embed-audio`、`enhance-image`、`enhance-video-mediakit`、`erase-banana`、`erase-subtitle-mediakit`、`extract-audio`、`hailuo03-video-super-resolution`、`layer-decompose`、`lip-sync`、`move-object-banana`、`outpaint-banana`、`redraw-banana`、`remove-background`、`super-resolution`

**文件与上传（~35 路）**
- `POST /api/upload`（第一篇追踪的入口）+ **两阶段提交**：`/api/upload/commit`（`/abort`、`/finalize`）+ `/api/upload/staging/delete`
- 完整文件管理：`/api/files/{delete,mkdir,rename,fork-rename,move,copy,duplicate,content,text-asset,adopt,track,import-external,check-conflicts,mention-search,versions}`
- `upload-cdn`（云端直传通道）

**画布（/api/v3/canvas/*，~15 路）**：`agent-compositions`、`audios`、`commands`、`content`、`images`、`local-files`、`media`、`previews`、`text/generate`、`text/models`、`video-audio-splits`、`videos`——v3 画布的每个媒体类型一条 REST 面

**项目语义（Composition/Resource 模型，~25 路）**：`/api/project/{compositions,resources}/{get,list,search,commands,versions,tags}`——**这印证了 media-agent 提示词里的 Composition/Resource/Version 三层创作对象模型是数据库级实现**

**技能市场（~30 路）**：`/api/skills/market/{search,categories,trending,install,uninstall,whitelist,sync-status}` + 创作者计划 `creator-plan/{submit,submissions,asset-presign,cover-upload,offline}` + 运营端 `operator/{publish,submissions,batch-approve}`

**连接器（MCP 生态，~12 路）**：`/api/connectors/{capability-catalog,capability-selection,capability-access,capability-resolve,prepare,tools,tools/call,mcp,mcp/authenticate,mcp/authorize,proxy-status,proxy-sync}`

**团队/计费（~20 路）**：`/api/v1/{credit,billing,team,promotions}/*`——积分钱包、并发限额（`models/concurrency/limits`）、促销领取（hailuo03 试用）

**运维（~15 路）**：`/api/health`、`/api/heartbeat`、`/api/pty`（伪终端！）、`/api/log-upload`、`/api/telemetry`、`/api/operations/undo`、`/api/permission/{request,saved}`、`/api/question/request`（agent 向用户提问的通道——提示词里的 `question` 工具落地）

**Web 媒体**：`/api/web-media/{browser,yt-dlp}`——**gateway 内置 yt-dlp 拉流能力**

---

## 3. Gateway 数据库

`resources/gateway/dist/` 里有 **19 版 SQLite 迁移**（`0001_init.sql` 起），注释里直接引用内部 ADR 编号（ADR-004/005），架构信息密度极高：

**0001_init.sql 的资产表设计**（摘录核心）：

```sql
CREATE TABLE assets (
  id TEXT PRIMARY KEY,            -- UUID，工作区生命周期内稳定
  path TEXT NOT NULL UNIQUE,      -- 工作区相对路径（reconcile 可重绑）
  status TEXT DEFAULT 'active',   -- active | missing（灰显 30 天后 GC）
  -- 三层指纹匹配（Tier 1.5/2）：
  dev_id, inode, birthtime_ms,   -- fs.stat 三元组（硬链接/重命名跟踪）
  quick_hash TEXT,               -- xxh3(前 4MB) 快速候选匹配
  full_hash TEXT,                -- xxh3(全文件)，冲突时惰性计算
  -- 业务字段：width/height/duration_ms/mime_type/metadata(JSON: prompt,seed,sampler…)
  candidate_asset_id TEXT REFERENCES assets(id) ON DELETE SET NULL  -- 缺失资产的候选合并
);
```

**迁移轨迹 = 功能演进史**：软删除(0002) → 实体父子(0003) → 插件节点存储(0004) → 文本文档版本(0005-0010，含 agent_origin/restored_from) → 资产锚点(0011) → **任务系统(0012)** → 资产引用快照(0013) → **聊天模型追踪(0014)** → 消息引用(0015) → 任务投递(0016) → **媒体编辑操作(0017)** → 能力 ID(0018) → 每终端 outbox(0019)。

**运行时组件**：better-sqlite3 + sharp（图像处理）+ ws + undici（HTTP 客户端）+ @napi-rs/@img（Rust 原生模块）+ `pdf.worker.mjs` + `protos/health.proto`（gRPC 健康检查）+ LUTs 预设 + 水印资产。

**配置**（`production.yaml`）：`port: 8001`（与实测一致）、`openCodeUrl: http://127.0.0.1:4096`（**opencode 的 MCP 端口**）、SQLite 输出目录 `./output_files`。

---

## 4. Agent 配置体系

`resources/agent-profiles/` 有 **v2/v3 两代档案**，每代含 `base.json`（基础）+ `base.domestic.json`（国内差异）+ `base.overseas.json`（海外差异）。

**v3 `base.json` 的关键事实**：

```json
{
  "$schema": "https://opencode.ai/schema.json",   // 标准 opencode 配置
  "default_agent": "media-agent",
  "experimental": { "mcp_timeout": 3600000 },      // MCP 调用 1 小时超时（视频生成够慢）
  "mcp": {
    "design": {                                    // 注册名 "design" 的 MCP server
      "command": ["node", "../mcp-tools-v3/dist/main.js"],
      "environment": { "GATEWAY_URL": "http://localhost:8001" }
    }
  },
  "agent": { /* build/plan/explore/general 全部 disable，只留 media-agent */ }
}
```

**捆绑的 opencode 是 v1.18.18**（178 MB 单文件 exe + rg.exe 4 MB）。**架构闭环确认**：

```
用户对话 → Electron main（IPC）→ gateway :8001 → opencode :4096（MCP）
                                       ↓
                          mcp-tools-v3（design MCP server）
                                       ↓
                          gateway 357 路 API（生成/编辑/文件/技能…）
```

media-agent 可用的 **29 个 design_* 工具**（从 base.json 提取，观察事实）：

| 类别 | 工具 |
| --- | --- |
| 能力发现 | `design_capability_list/get/search`、`design_connector_call` |
| 读 | `design_read`、`design_analyse_media`、`design_resource_get` |
| 项目语义 | `design_composition_{search,get,edit,type_list}`、`design_resource_edit`、`design_project_rules`、`design_document_write` |
| 任务 | `design_task_{query,wait,resolve}`、`design_execute`、`design_deliver` |
| 搜索 | `design_image_search`、`design_voice_search` |

被**显式禁用**的：opencode 原生的 read/write/edit/bash（除 bash）/todowrite/task/webfetch/codesearch——**agent 不能直接写文件系统，一切创作动作必须走 gateway 的资产系统**。这是权限收敛的典范设计。

---

## 5. media-agent 系统提示词

`agent-profiles/v3/config/agents/media-agent.md`（中文原文，约 3,500 字）是整个产品的灵魂。逐节解读：

**身份**："你是 MiniMax Design，一位与用户共同创作的专业伙伴"——不是工具，是创作责任承担者。

**项目语义（领域模型，最重要的一节）**：
- **Composition** = 有明确内容范围和稳定身份的创作对象（"主角立绘"、"片头动画"），是决策/修改/复用的基本单位
- **Resource** = 一份创作内容的持续身份，**Version** 固定某次具体内容
- 判例式的边界规则写得极细："共同出现或共用参考不合并对象身份"、"制作阶段、媒体形式、文件数量和模型生成批次本身也不决定对象边界"、"派生关系不意味着同一 Resource"
- **这直接对应 gateway 数据库的 `project_compositions/resources/versions` 表**——提示词、MCP 工具、SQL 三层同构

**协作与决策**：区分"用户已确认的要求 / 授权内选择 / 待讨论设想"；默认按可审阅成果分步推进、每步请用户确认；无法推断的实质缺口（画幅/时长/关键人物事实）只暂停依赖项并给推荐方案。

**制作与修改**：从最终交付反向规划；引用真实结果不重新描述；**不对自己生成的产物做验收式自查**（质量问题以用户反馈为准——防 agent 过度自我循环）。

**Skill 消费规则**：选用 Skill 即采用其工作方法（流程/确认节奏/输入组织），覆盖环节不再另选。

**工具纪律**：`question` 工具用于在可行方案间让用户选择；`task` 被 deny（不做自主子任务）；技能 `*` 全 allow。

**工程启示**：这是一份**产品级 agent 提示词的范本**——领域模型判例化、授权边界显式化、协作节奏默认化。比通用 "You are a helpful assistant" 高出数个量级。

---

## 6. 模型目录（活体数据）

gateway 运行中实测 `GET /api/models/*`（未登录态，观察事实）：

**视频（14 个）**：
`MiniMax-H3` / `H3-Max` / `H3-Max-Turbo`（主力，backend `minimax_v3`）、`seedance2.0/2.0-fast/2.0-mini/2.5`（豆包）、`kling-v3-omni-video`、`kling-avatar`、`wan2.6-i2v`、`wan3.0-video(-prime)`、`beta-3-1(-fast)`（nano_banana 系）

**图像（8 个）**：`g-image-2`、`banana-2`、`banana-pro`、`doubao-seedream-5-0-pro-260628`、`doubao-seedream-4-5-251128`、`midjourney-8.2`、`g-image-2.5-flare/sunburst`

**音乐（2）**：`music-3.0`、`elevenlabs-music-v2`；**语音（4）**：`MiniMax-H3 Audio`、`speech-2.8-hd`、`seed-audio-1.0`

**模型条目结构**（MiniMax-H3 实测，节选）——参数 schema 驱动 UI：

```json
{
  "id": "MiniMax-H3", "backend": "minimax_v3",
  "max_refs": 9, "max_video_refs": 3, "max_audio_refs": 3,
  "supportsLastFrameOnly": true, "promptMaxLength": 7000,
  "inputMediaLimits": { "imageMinWidth": 256, "imageMaxAspectRatio": 2.5, ... },
  "params": {
    "image_mode": { "type": "select", "options": ["reference","first-last-frame","video-extension"] },
    "duration":   { "type": "select", "options": ["4"..."15"], "default": "5" }
  }
}
```

计费面也活着：`/api/v1/billing/pricing`（30 KB 价目）、并发限额 `/api/v1/models/concurrency/limits`。

---

## 7. 技能市场

`GET /api/skills/runtime` 实测 **18 个捆绑 Skill**（全部中英双语摘要）。按类型：

**视频创作**：anime-game-pv（动漫 PV）、cinematic-title-sequence（电影片头）、cool-music-video（音乐短片）、koc-video（种草视频）、video-deconstruct（镜头拆解反推制作方案）、ui-motion（界面动效宣传）

**专业工具链**：blender-workflow、houdini-workflow、touchdesigner-workflow、design-photoshop、design-after-effects（生成**可继续编辑的工程文件**而非死视频——这是与普通 AI 视频工具的本质差异）

**能力型**：voice-clone（含授权确认流程）、brand-ad、education-studio、h3-visual-design

**元技能**：skill-creator（创建新 Skill）、skill-reviewer（上架前质检）——**技能市场有完整的创作-审查-上架流水线**（对应 §2 的 creator-plan/operator API）

**浏览器操作**：control-in-app-browser——**agent 可控制内置浏览器做真实网页操作**（读/点/填/截图/登录接管），与第一篇的 browser IPC 域闭环。

市场端点还包括 `trending`、`categories`、`whitelist`（白名单安装管控）。

---

## 8. 插件体系

3 个捆绑插件（实测 `/api/plugins`）：

| 插件 | 版本 | 定位 |
| --- | --- | --- |
| clip-studio（AI剪辑工坊） | 0.10.2 | 开源 AI 视频剪辑器（自动剪辑+字幕）；`clip-studio-export/` 里有完整的前端（index.html/cursor/extensions/fonts）——**嵌入式 Web 应用** |
| 3d-director-stage（导演台） | 0.24.10 | 3D 舞台编排 |
| comfyui | 0.2.6 | ComfyUI 工作流桥（对应 §2 的 /api/comfyui/* 14 路和 `comfyUiManagedDataDirs`） |

插件通过 `BROWSER_PLUGIN_CHANNELS` 四通道（第一篇 §6）注入浏览器页面；gateway 侧有 `hub-plugin-sdk`（`node_modules/@hilo/`）作为插件 SDK。

---

## 9. 模型知识库

`agent-profiles/v3/config/knowledge/vendors/` 有 **13 张厂商卡片**（.md），通过 `design_search_knowledge` 供 agent 检索。两张实测样本：

**minimax.md（H3 系列视频卡）**——prompt 工程经验成文：
- slot 引用语法：`@图片1`/`@视频1` 逐一说明锁定关系，"避免 H3 把参考用错对象"
- 局部编辑模式：只加源视频+本次需要的参考
- 多镜头写法：`全局基准 + 【镜头1】【镜头2】` 分段；每镜 1.5-5 秒节奏参考
- 风格写法："写 2-4 个可观察的媒介/色彩/材质/光影特征，比只写风格名更稳定"
- 已知行为："简单动作按物理阶段切成多段，容易得到重复镜头"；"只写'轻微跟随'通常得到静止画面"
- QC 清单 + 参数契约核对项

**veo.md（veo3 卡）**：5-part prompt 公式、positive-absence 式 negative prompt、已知 bug 列表（风格参考需落实为可观察特征）。

**这套知识库 = 把"哪个模型怎么写 prompt 才稳"的运营经验固化为 agent 可检索的资产**——是整个产品护城河的缩影：模型会换，经验沉淀。

---

## 10. 修正与补充

对第一篇的修正与补强：

1. **上传链路补充**：`/api/upload` 只是入口，完整生命周期是 `upload → commit(abort/finalize) → staging/delete` 的**两阶段提交**；另有 `files/upload-cdn` 云端直传通道。第一篇的 `__hubBlobRef` 是本地 vault 引用，与 CDN 上传并存。
2. **凭据安全修正**：第一篇指出 fuses 层 CookieEncryption 关闭；本篇发现应用层有完整的 `CredentialStore`（Cipher 信封 + pbkdf2 派生）——**加密在应用层自建，不在 Electron 层**。
3. **agent 子系统从推断变实证**：第一篇的 opencode/MCP 架构图是"资源目录+环境变量推断"，本篇用 base.json + production.yaml + 357 路由 + 19 版 SQL 完整实证。
4. **技能/插件/知识库是第一篇完全未覆盖的三大产品面**。
5. **模型目录实测 28 个在线模型 ID**，含豆包 seedream/seedance、wan、kling 等第三方——多厂商聚合是产品核心。

---

## 11. 剩余未覆盖项

诚实边界（截至本篇完成时仍未分析）：

1. **原生二进制**：`opencode.exe`（178 MB，Go/Rust/Zig? 未验）、`node-screenshots.win32-x64-msvc.node`、`Update.exe`——需要 Ghidra Windows P0 或 IDA 配置后用 `open_binary` 系列工具
2. **CDP 运行时观察**：调试 fuse 关闭，需带 `--remote-debugging-port` 重启应用
3. **21MB renderer v1 的模块级拆分**：scope-hoisted 打包的物理限制，已尽力（40 模块 + 格式化单文件 2.2 万行）
4. **nupkg 增量更新包**内部结构（更新链投毒面分析需解包对比）
5. **写操作 API 的行为验证**（357 路里只 GET 探测了 27 路；POST 全部未碰——遵守只读原则）
6. **gateway 与 opencode 的 MCP 协议细节**（:4096 端口未探测，属运行时内部协议）

---

## 附：本次新增分析产物清单

| 产物 | 位置 |
| --- | --- |
| main bundle 恢复源码（211K 行） | `recovered-main/modules/bundle.js` |
| preload 恢复源码（3 模块） | `recovered-v3/modules/` |
| renderer 恢复（v3 + v1 39 模块） | `recovered-renderer-v3/`、`wakaru-renderer-direct/` |
| gateway 探测记录（27 路由） | `gateway-probe.json` |
| 恢复证据（REA JSON） | `recovery-v3.json`、`recovery-main.json` 等 |
| WSL 工具链 | Ubuntu 24.04 内 `~/node24`、`~/bin/wakaru` |

*第二篇完。所有路由/模型/技能/插件清单均为源码提取或活体 GET 探测的观察事实；架构推断处已标注依据。*
