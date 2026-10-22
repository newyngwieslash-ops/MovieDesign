# MovieDesign 开发交接

生成时间：2026-10-20 20:08:42 +08:00（Asia/Shanghai）  
交接基线：`main@f9432ae6dd485f51441d7f6202e127107ef1966e`  
工作目录：`E:/AI_Rea_Things/MovieDesign`  
目标：在现有实现上完成 PRD 缺口修复、真实模型验收和正式签名发布，避免重建项目或重复已完成里程碑。

## 1. 先读这个结论

M1–M7 已有实现提交，M2–M7 的阶段审查、离线测试和若干真实桌面进程检查有历史记录。**不能据此宣布原 PRD 已完整交付**：真实 LLM、图像、视频生成与正式签名安装升级仍未执行；本次代码对照还确认更新功能只有策略/验签基础，尚未接通运行时下载、验签、安装。

下一会话应从下文 **H01 更新链路缺口**开始，随后完成发布一致性、视频验收精度、启动脚本保护及真实验收。不要从 M4.2 或 M6.3 重启实施；聊天旧上下文和旧进度账本落后于 Git。

本轮是文档交接：读取规格、代码、计划和证据，核实 Git 状态；没有重新运行全量测试或真实模型调用。下述“历史通过”来自现存报告，不表示本轮再次通过；新增缺口是有路径证据的静态核对结果，还需回归测试确认修复。

## 2. 权威资料与阅读顺序

| 用途 | 文件 |
| --- | --- |
| 原 PRD/实现规格 | [docs/spec.md](E:/AI_Rea_Things/MovieDesign/docs/spec.md)；文中残留 `OpenDesign` 是旧名称，产品名仍是 MovieDesign |
| 已确认适配和实际协议 | [docs/contracts.md](E:/AI_Rea_Things/MovieDesign/docs/contracts.md)；按对应阶段查阅，历史段落不能脱离后续修订使用 |
| 总计划与约束 | [总计划](E:/AI_Rea_Things/MovieDesign/docs/superpowers/plans/2026-10-15-moviedesign-complete.md) |
| 各里程碑任务 | 同目录 M2–M7 六份分计划；特别是 [M7](E:/AI_Rea_Things/MovieDesign/docs/superpowers/plans/2026-10-15-m7-video-release.md) 未勾选的真实验收项 |
| 历史交付和使用说明 | [里程碑](E:/AI_Rea_Things/MovieDesign/docs/milestones.md)、[README](E:/AI_Rea_Things/MovieDesign/README.md) |
| 历史验收矩阵 | [acceptance.md](E:/AI_Rea_Things/MovieDesign/docs/acceptance.md)；其中“全部完整实现”等表述需要按本次缺口重新校准 |
| 发布步骤 | [releasing.md](E:/AI_Rea_Things/MovieDesign/docs/releasing.md) |
| 原产品实证 | [analysis-docs 索引](E:/AI_Rea_Things/MovieDesign/analysis-docs/README.md)，重点 01、02、04、05、06/07 |
| 最新阶段审查 | `.superpowers/sdd/m6-whole-review.md`、`m7.1-review.md` 至 `m7.5-review.md`、`m7-whole-review.md` |
| 原应用只读参考根 | `E:/AI_Rea_Things/MinimaxDesignThings1015`；禁止修改或复用其账号/用户数据 |

`.superpowers/`、`.runtime/`、`.tools/` 是本机证据/缓存，通常被 Git 忽略。不要 `git clean -fdx`。新会话若仍使用本工作目录可直接读取；重新克隆时不能假设这些证据也存在。

## 3. 当前仓库与实际进度

- 分支：`main`。本地 HEAD、跟踪分支 `origin/main`、本轮实际 `git ls-remote --heads origin main` 三者均为 `f9432ae6dd485f51441d7f6202e127107ef1966e`。已核实远端，无需补推这批既有提交。
- 本轮开始 `git status` 有 **32 个既有 tracked 修改标记**，但 `git diff --quiet` 与 `git diff --cached --quiet` 均返回 0；没有规范化后的内容差异，表现为换行/工作树统计噪声。不要重置、批量暂存或将它们混入后续提交。路径快照见同目录 `repository-state.json`。
- 本次新增交接文件尚未提交；是否随下一次文档提交纳入由后续工作决定，不改变上述代码基线。
- 旧 `.superpowers/sdd/progress.md` 尾部仍写 M6.5 进行中，已过时；**以当前 Git、M7 报告和本交接为准**。旧 agent ID 不作为新会话恢复依据。

| 阶段 | 当前已有内容 | 状态与边界 |
| --- | --- | --- |
| M1 | Tauri/Go 生命周期、SQLite、健康检查、工作台 | 已实现，有进程和桌面证据 |
| M2 | 项目/资产/对象模型、文件管理、三态幂等投递 | 已实现，unknown/并发/崩溃恢复有测试 |
| M3 | 八图形 Canvas 编辑、50 步历史、标注、持久无限画布 | 已实现；物理跨屏 DPI、真实输入法候选框仍有验收边界 |
| M4 | 自定义图像供应商、能力白名单、任务/SSE、计费卡 | 已实现；真实文生图和标注修复未执行 |
| M5 | 固定 opencode、18 design 工具、TipTap/引用/队列、权限隔离 | 已实现；真实 LLM 及原生 Responses/Anthropic 推理未验收 |
| M6 | 双技能、意图/脚本/分镜回执、不可变 revision、精确 Prompt、Preflight、失败回流 | 已实现，阶段审查和本地链路有证据；真实模型自主遵循技能未验收 |
| M7 | ark-video、MP4 技术检查、视频画布、打断恢复、更新策略、发布脚本/审计 | 已提交并有历史审查；更新运行时接线存在 H01，正式发布和真实视频未完成 |

关键后续提交：M6 阶段验收 `1f13834`；M7.1 `9d03cd7`；M7.2 `0d4b166`；M7.3 `6837dac`；M7.4 `d1aa130`；M7.5 `26a30ac`；审查记录 `20578d9`；计划/导出编码修正 `4ec6b98`；启动器 `f9432ae`。这些是历史节点，不是要求重新执行的任务。

## 4. 对照 PRD 的覆盖与剩余边界

| PRD | 已有落点 | 仍需完成/核实 |
| --- | --- | --- |
| §0 参考资料、契约优先级 | spec/contracts/analysis-docs | H07 校准实现与文档不一致 |
| §1 产品五项能力与非目标 | desktop/gateway/agent | 真实创作验证 H08–H11；不新增插件市场、团队等范围 |
| §2 栈与进程 | Tauri 2、React 19、Go 1.25.8、SQLite、opencode 1.19.0、MCP SDK 1.4.1 | updater 依赖存在，但实际执行链未接通：H01 |
| §3 对象模型/血缘 | 五核心表、支撑迁移 000001–000014 | H12 真实项目血缘导出和备份恢复 |
| §4 HTTP/幂等/Origin/SSE | server 与共享领域服务，有离线检查 | H08–H11 真实环境异常和重连，不改已确认协议 |
| §5 MCP 工具 | 18 项 design 工具 + 允许的原生 question | H08 实际模型工具调用；素材搜索为已有本地目录/登记能力，不是新建搜索平台 |
| §6 状态机/计费 | tasks/questions，费用卡及阶段取消 | H11 真实取消/失败/重试和费用事实核对 |
| §7 deliver 三态 | delivery 日志、原请求重放、并发幂等 | H11/H12 真实产物与导出，禁止把 unknown 当新生成 |
| §8 聊天 | 10s 受理、全局 SSE、40 工具上限、队列/取消 | H08 原生模型矩阵；H15 检验完整响应缓冲对流式体验的影响 |
| §9 编辑器、确认卡、任务中心 | 八图形/标注/TipTap/卡片/视频节点 | H14 物理设备验收，H15 历史提示与媒体文案修正 |
| §10 中文 agent 提示词 | agent/media-agent.md 与生成配置 | H08 真实方法遵循；不能以脚本模型替代 |
| §11 两个完整技能 | 口播视频、图片标注修复 | H08/H09 真实模型消费与人工反馈 |
| §12 分镜→批准→精确 Prompt→生成 | pipeline、7 门禁与完整依赖链 | H04 时长精度；H10 一句话真实成片及语义修订 |
| §13 校验、安全、日志/events | Origin、受管文件、DPAPI、共享校验 | 修复时保留安全边界；H05/H06/H11 验证实际运行入口 |
| §14 里程碑测试/提交/推送 | M1–M7 已有提交和历史审查 | H16 修复后的定向/全量/独立审查及推送 |
| §15 最终验收 | 本地协议、SQL dump、WebView 证据 | 真实生成与正式签名安装升级仍 UNEXECUTED：H08–H13 |

## 5. 剩余完整任务清单

以下是本次交接识别的剩余范围。P0/P1 是交付前事项；P2 是已有已知边界/次要改善，不能混成阻塞功能或无条件扩建。

### 必须补齐的实现与发布准备

- [ ] **H01 · P0 · 接通真实 updater 检查→下载→验签→安装链。**
  - 证据：`desktop/src-tauri/src/lib.rs:20–35` 的 `updater_check_and_gate` 只读取 gateway 快照并调用 `evaluate_policy_gate`；`updates.rs:286` 的 `configure_runtime_updater` 只构建 updater，没有调用检查/下载/安装；`verify_gated_manifest_and_package` 当前调用集中在单元测试；`UpdateSettings.tsx:278` 附近安装按钮没有 `onClick`。
  - 工作：复用已安装插件，将真实远端 manifest、包 URL/版本/签名校验、进度/错误与安装动作接到运行时和 UI；安装前再次核对策略和活动状态，避免“检查时空闲、安装时忙碌”的竞态。需要用户决策的点仍用既有结构化确认卡。
  - 完成标准：在生产 WebView 可触发真实插件链；坏签名/版本错配/外部仓库 URL 拒绝；manual_only、策略失败、任务忙碌均零安装副作用；受控签名测试包可以走完整链。测试签名通过不等于正式发布通过。
- [ ] **H02 · P1 · 统一实际版本与发布版本来源。**
  - 证据：`UpdateSettings.tsx:156,171` 硬编码 `0.1.0`；Rust 命令允许 UI 传 current_version；Go 有 `DefaultAppVersion`；`release.ps1 -Version` 主要影响输出/manifest，尚需证明与实际二进制版本一致。
  - 工作：实际安装版本取原生包元数据，不能信任可变前端值；构建/Tag/manifest/安装包/Cargo/Tauri/npm 版本一致，不匹配即阻断发布。补 `CompareSemver` 的数字 prerelease 标识符比较，例如 rc.2 与 rc.10，保持半开区间判断正确。
- [ ] **H03 · P1 · 正式签名材料与可复现 CI。**
  - 已确认：`tauri.conf.json` 公钥与 `updates.rs` 单测公开向量相同，不能作为正式发布密钥已配置的证明。只在安全渠道配置正式匹配密钥对，私钥不进入仓库/日志/聊天。
  - 已确认配置差异：`.github/workflows/release.yml` 指定 pnpm 10，而 `desktop/package.json` 指定 pnpm 12.8.1；对齐并验证 frozen-lockfile，不能推定 CI 已成功。
  - 核实并修正：Tag/手动 version 输入、`TAURI_SIGNING_PRIVATE_KEY_PATH` 分支与实际 CLI 读入方式、公钥匹配、安装包 `.sig` 与 manifest signature 编码、可信版本注释是否与正式 Tauri 产物一致。这里只是待验收项，尚未用正式构建证明这些分支失败。
  - 完成标准：干净 Windows runner 完整构建并验签；生产无签名材料保持明确失败；测试密钥与正式材料隔离。
- [ ] **H04 · P1 · 按帧/时间基落实 15 秒视频技术验收。**
  - 已确认差距：`gateway/internal/providers/video.go:793` 使用固定 `diff > 500` 毫秒拒绝阈值；M7.2 计划要求依据编码时间基和一帧记录容差。
  - 工作：读取实际流帧率/时间基，采用有依据的时长判断，覆盖不同帧率/可变帧率、明显偏短、空音轨和元数据不一致。保留整条生成；不要裁片、补静帧或自动重生来“修”模型结果。
  - 客观音轨存在不证明口播和音乐内容正确；主观验收由人类完成。
- [ ] **H05 · P1 · 校准更新策略重试合同。**
  - 已确认差异：M7 计划写“仅 5xx 可重试”，`gateway/internal/updates/policy.go:669–683` 还把网络/超时/读取错误标为可重试；`docs/releasing.md` 又描述了后一种行为。
  - 先查原产品实证及已确认适配，按契约优先级统一代码/测试/文档。没有新确认依据时遵守已确认的 5xx-only 计划，不用新文档自行改变合同；仍保持 3s、最多 3 次、既有退避与 24h/LKG 语义。
- [ ] **H06 · P1 · 修复并验证一键启动器的工作区保护。**
  - 已确认：`restart.bat` 构建后无内容检查执行 `git checkout -- gateway/internal/agent/media_prompt.go`，可能覆盖该文件的真实修改；不能拿它做无害的构建入口。
  - 已确认：进程路径仅用无目录边界的 `StartsWith($root)`，可能匹配同名前缀的兄弟目录；启动/停止失败仍可能显示成功。默认强杀不构成优雅关闭验证。
  - 最小修复：移除无条件 Git 撤回，精确目录/自有进程身份，正确失败码；优先正常关闭并限定超时强制回收。补可运行检查后再使用该脚本，不全局按进程名终止。
- [ ] **H07 · P1 · 修正交付文档与账本的过度结论。**
  - 保留历史报告，不伪改历史通过结果；在 README/acceptance/milestones/contracts 标明 H01 尚未接线、H04 时长差距、H05 合同差异。
  - 拆分 M7.4“安装升级已检查”的勾选项：单元测试/模拟迁移与干净账户实际安装是两件事；后者目前未执行。
  - 核对核心表术语：原五核心表是 assets/compositions/resources/versions/composition_members，projects 是支撑表；不要照抄某些审计段落把 projects 误列为核心表。
  - 修正旧账本只到 M6.5、`OpenDesign` 残留说明、`design_design_*` 文档拼写；`4ec6b98` 已修导出 UTF-8，不重复派该已关闭任务。

### 有真实连接后完成的最终验收

- [ ] **H08 · P1 · 真实 LLM 与两个技能消费。** 在应用内配置可用本地/云端连接；验证 opencode 能实际完成两种技能流程、真实 asset mention、排队/取消/SSE、结构化人类门禁，原生文件/命令工具仍禁用。OpenAI Chat Completions、Responses、Anthropic 已有协议适配，但后两者的实际原生推理证据仍需补；不强制接第三家付费模型。只索取缺失的服务能力/凭据配置，不重问 BYOK 等已确认选择。
- [ ] **H09 · P1 · 真实图像文生图与标注修复。** 使用有权上传的素材和已配置 `openai-images` 兼容端点，经人工费用确认调用；记录能力/model/脱敏任务 ID、mask/有序引用、Version/Asset/画布结果及同 Resource 新版本血缘。至少验证一个实际模型，不能用免费 models 列表或本地协议 fixture 替代。
- [ ] **H10 · P1 · 一句话真实 15 秒口播配乐闭环。** 新项目上传参考图，真实 agent 执行意图→脚本→分镜批准→逐字 Prompt→Preflight→费用卡→单次整条 ark-video 生成→本地持久化→画布播放；改需求后生成同媒体 Resource 的新 Version，保留完整血缘。记录实际帧率/时间基/时长/音轨；口播正确、配乐合意由用户验收。模型能力不足须在付费前明确阻止，不悄悄换模型/拼片/TTS。
- [ ] **H11 · P1 · 真实取消/失败恢复/重新过门禁。** 在可控成本的人工批准范围验证发送前/排队/运行/收集阶段；确认 POST 数与批准 attempt 相符，unknown 只查询/原样恢复，retry 要人工动作和费用批准。真实 running 取消是否继续收费、queued 是否退款以供应商事实/账单为准；不能把远端取消成功直接当作费用为零的证明。改分镜后旧批准/合同必须拒绝，迟到结果不自动采纳。
- [ ] **H12 · P1 · 真实项目 SQL 血缘、备份与恢复。** 从 H09/H10 的选定项目导出脱敏 SQLite `.dump`，验证分镜→Prompt→合同→task/generation_inputs→task_output→Version→Asset 和有序参考图；排除其他项目与凭据。另做关闭/安全空闲时备份恢复，明确 DPAPI 的 Windows 账户绑定限制，不以本地合成数据库代替这项真实验收。
- [ ] **H13 · P1 · 正式签名 Release 与干净 Windows 安装升级。** H01–H03 完成后，用正式公钥匹配私钥生成 NSIS、签名、latest.json、SHA256 和许可说明，验证 GitHub 固定策略/manifest/资产地址。使用干净 Windows 账户做首次安装、建项目、关闭、旧版→新版升级、数据/回执保留、坏签名、manual_only、断网/LKG 和忙碌阻止更新。验证后在既有 GitHub 仓库发布；不能发布测试签名冒充正式包。

### 体验边界、次要项与最终收口

- [ ] **H14 · P2 · 物理设备验收。** 真实跨屏不同 DPI、系统中文 IME 候选/合成输入/Delete、键盘焦点与基本可访问性；M3 的受控 DPR/合成事件证明不等于物理硬件通过。其他平台不在首发范围，未通过矩阵不能声明支持。
- [ ] **H15 · P2 · 有依据的体验收尾。** 历史 M6/M7 审查保留：完成任务仍显示“恢复已批准任务”、旧失败 recovery 横幅、视频仍显示“图像任务/图像采纳”的文案。复用现有状态和组件修正；已有 Vite >500kB 警告在测得启动成本后再优化，不为消警告做框架重构。另核验 `agent/relay.go` 的完整响应缓冲（32MiB/10分钟，已有 ponytail 说明）对真实首 token/流式展示的影响；如确实不满足 §8 实时要求，以可持久化增量方案补齐，不能为流式牺牲原请求恢复/禁止自动重试。
- [ ] **H16 · P1 · 定向修复→阶段验证→独立审查→提交推送→更新验收表。** 修复必须有能复现问题的最小检查；代码变化后跑适用的测试，再在交付关口跑全量与当前生产 WebView。对新改动做独立审查，处理全部 Critical/Important，并给四阶门槛分别附证据。真实调用/正式发布仍无条件时继续记录 UNEXECUTED，不虚勾、不重复询问已确认事项。

建议执行顺序：**H01/H02 → H03/H05/H06 → H04 → H07 → H08/H09/H10 → H11/H12 → H13 → H14/H15 → H16 最终收口**。H07 和安全的离线修复可并行；H13 的正式发布依赖实际签名材料和安装环境。新发现问题先补证据、界定范围，再更新本清单。

## 6. 已确认决定：不要再次询问

- 产品 MovieDesign；实现根与原应用只读边界不变。用户明确规格/已确认修订优先于原 Electron 协议未移植路径；未规定语义先查 analysis-docs。
- Tauri 2 + React 19/TS/Vite + Go（已允许 1.25.8）+ modernc SQLite + 官方 MCP SDK 1.4.1 + opencode。前端只访问 gateway，Rust↔Go 仅三个 JSON-RPC 方法（info/health/shutdown）。
- BYOK，供应商直接收费，仅本地估算与人类费用卡，不建钱包/账号后端。M1 直接进入本地工作台，无独立登录服务。
- 用户要求可配置本地/云端供应商；当前已知模板 openai-images、ark-video，LLM 支持三种已有协议。不是任意 API/用户脚本/插件框架。
- 所有授权走结构化卡，允许原生 question 作为工具白名单例外；18 design 工具，原生 bash/read/write/edit/todowrite/skill 等禁用。每轮 40 次上限。
- requestId 原样幂等，unknown 同输入重放，禁止自动重新生成。billing waiting_user 继续等待，普通 waiting_user/unknown 结束 agent 等待，unknown 不是任务终态。
- 精确 Prompt 字节、批准 revision/hash、整条重生成、独立内容独立 Resource；同内容修改才复用 Resource 新 Version。
- 五核心表及已提交迁移不改写，新增支撑用新编号（当前到 000014）。HTTP/MCP 共用领域服务；SQL 事务内不重入 DB getter/网络/文件 I/O。
- 127.0.0.1 精确 Origin，包括预检和 MCP；Origin 防浏览器 CSRF，不证明本机进程身份。MCP 另有私有令牌。密钥保存在 DPAPI，不进入交接或聊天。
- Windows x64 首发，GitHub Releases 签名更新，manual_only/24h 缓存/LKG 依契约。
- 按 Ponytail 优先复用/标准库/已安装依赖；不增加无请求抽象、插件、钱包、团队协作、热更新等非目标。

## 7. 可用命令与证据

从工作根执行（本轮未重跑）：

```powershell
git status --short --branch
git -c core.safecrlf=false diff --stat
git log -10 --oneline
./scripts/check.ps1
pnpm --dir desktop desktop:build
./scripts/smoke-desktop.ps1 -Mode release -Smoke video
./scripts/smoke-desktop.ps1 -Mode release -Smoke interruptions
./scripts/verify-lineage.ps1
./scripts/release.ps1 -DryRun
```

按 README 选择其他 smoke 模式，避免无变化时反复跑全部历史套件。真实桌面自动化需交互 Windows；若沙箱拒绝 WebView/CIM，只对自有运行进程使用已授权提升，不杀无关程序、不改系统安全设置。8001/4096 占用时查明所有权；不连接或结束第三方服务。

历史证据入口：
- `.superpowers/sdd/m7.5-report.md`、`m7.5-evidence-manifest.json`：历史测试、源文件和构建证据对应关系。
- `.runtime/acceptance-lineage/verify-lineage-report.json`、`acceptance-dump.sql`：本地 fixture 全血缘/脱敏记录；不是实际供应商生成证明。
- `.runtime/release-dry-run/release-dry-run.json`：`signingKeyConfigured:false`、`signedInstallerProduced:false`，明确无正式签名包。
- `.runtime/desktop-release-743fc988efec4df7974a8a50581c87a1`（m1）、`9460b02d1bb04cd88cb85b41a1e9254a`（video）、`1c51369d935b4a2798e32bb3c892622f`（interruptions，路径同前缀）。
- 生成 `media_prompt.go` 的 CRLF 输入与 HEAD 字节有历史差异，报告有实际构建输入存档；不把旧二进制称为当前修改后产物。

## 8. 建议技能

- **ponytail**：本任务持续适用，最小可靠修复，优先既有代码/标准库/平台能力。
- **systematic-debugging**：H01 等必须追真实事件/调用链，先复现再修根因。
- **writing-plans**：把 H01–H16 转为有文件责任、验证与提交边界的小计划，不重抄 M1–M7。
- **subagent-driven-development**：若采用既有计划规定的实施者/独立审查流程，读取本机技能后按其分工；新会话不能依赖本聊天的旧 agent。
- **requesting-code-review / code-review / verification-before-completion**：审查本次实际差异，按证据报告范围。
- **handoff**：后续再次换会话时更新事实，不用旧聊天摘要覆盖当前仓库。

本次按用户明确要求保存在项目目录；这优先于 handoff 技能默认保存系统临时目录的建议。交接没有包含 API Key、密码、私钥或用户素材正文。

## 9. 新 Codex 会话提示词

完整可复制版本见同目录 [next-session-prompt.md](E:/AI_Rea_Things/MovieDesign/docs/handoffs/2026-10-20_200842+0800/next-session-prompt.md)。先加载本交接再执行，不从旧阶段恢复。
