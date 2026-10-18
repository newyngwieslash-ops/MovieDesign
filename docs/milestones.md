# 里程碑验收

完整后续计划见[总计划与六份分计划](superpowers/plans/2026-10-15-moviedesign-complete.md)。M2–M4 已通过全量检查与独立整阶段审查并推送。M5.1–M5.5 单任务复审均通过，整M5审查问题在9f497a6修复并复审通过，main@bb20c6f已推送核验；M6.1工程任务复审通过，M6.2准备实施，其余M6–M7待实施。真实凭据支持的LLM、图像和视频验收未执行。计划中的命令不是已执行结果，各阶段证据另行记录。

| 里程碑 | 范围 | 状态 |
| --- | --- | --- |
| M1 | Tauri、gateway、SQLite、空工作台、生命周期 | 验证通过 |
| M2 | 对象模型 CRUD、幂等投递、unknown 重放 | 验证通过 |
| M3 | 八图形编辑器、历史、标注、持久创作画布 | 验证通过 |
| M4 | 图像模型、能力契约、任务状态机、计费与事件 | 工程验收与整阶段复审通过；真实调用未执行 |
| M5 | opencode、MCP、提示词、聊天与确认卡 | 单任务与整阶段工程复审通过，真实调用未执行；远程交付见后文 |
| M6 | 两个技能、分镜批准、Prompt 锁定与 Preflight | M6.1任务复审通过，M6.2接续；整阶段未完成 |
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
- 开发服务排除 `src-tauri` 文件监听，避免 Windows 编译期间锁定 exe/PDB 导致 Vite EBUSY 退出。

## M1 范围说明

- 目前只交付 Windows x64；尚未验证 macOS/Linux。
- 登录按已确认 Q4 延后；opencode 按 M5 接入，M1 健康不代表 agent 可用。
- 编辑器、对象 CRUD、任务表、SSE 与业务写入按里程碑逐项接入。
- 原规格本文完整保存于 spec.md；确认后的适配与未知项见 contracts.md。

### M2 实施记录（2026-10-15）

M2.1 提交为 `29f7b0d`，严格输入修正为 `9983337`；专项测试、Go 全量测试/vet 和 gateway 真实进程生命周期冒烟通过。M2.2 对象 CRUD 及严格输入修正已通过独立审查（`b279d80`、`8b0583c`）；真实进程验证了创建、幂等重放、冲突拒绝、Origin 与重启持久化。M2.3 文件层与恢复隔离修正已通过独立审查（`2796144`、`20e26fa`），全量 Go 测试/vet、Windows 句柄及恢复回归通过；真实 gateway 进程完成上传、提交重放、重命名、复制和重启持久化检查。M2.4 投递恢复与审查修正已完成（`627bc04`、`a914910`、`6f6a509`）。M2.5 桌面 UI 和专项验收见下；完整 M2 的独立审查与整体集成由控制任务继续执行，尚不声明整体完成或推送。

### M2.5 专项验证（2026-10-15）

- `node --test scripts/check-m2-recovery.mjs`：通过；上传同文件内容核对、上传→提交→投递各边界序列化/重读、固定三 ID 与精确 body、修订 baseVersion、跨项目记录拒绝。
- `powershell -NoProfile -File scripts/check.ps1`：最终在本机授权环境通过；全量 Go API/存储/文件/投递测试及 vet、sidecar 构建、Node 恢复检查、TypeScript/Vite、Rust 测试、M1 真实 gateway 生命周期/Origin。进程证据 `.runtime/smoke-83f9f9d5b80349f1af13b84442b6a9a8/`。先前沙箱执行在 M1 RPC 超时，其余检查已绿；不计作通过。
- `pnpm --dir desktop desktop:build`：重建当前生产 exe 与 sidecar，通过。
- `scripts/smoke-desktop.ps1 -Mode release -Smoke m2`：当前生产版真实 WebView 通过。DOM 表单创建项目、文件输入真实 PNG 上传/提交/投递、naturalWidth=1、相同投递一次 Version、新版本同 Resource 且保留 source_version_id、第二项目零素材。真实创建与投递 operation 在发送前由 DOMStorage 捕获，重播记录模拟响应丢失；创建刷新后同 body 续建仍一项目，投递刷新并原生重启后同 body/requestId 续投递仍两版本。
- M2 最终证据 `.runtime/desktop-release-c339074e146341bf9b7783fdc4a3627f/`：`m2-before-restart.png`、`m2-recovered.png`、`m2-expected.json`、`m2-renderer.json`、`webview-profile.json`。profile command line 和持久 marker 均确认测试专用隔离，renderer 无未捕获异常，正常关闭后 sidecar/8001 均释放。
- `scripts/smoke-desktop.ps1 -Mode release -Smoke m1`：当前生产版 M1 画布/HTTP health/Origin/正常关闭回归通过；证据 `.runtime/desktop-release-da58f82adb274615964a114ff2ee2a27/`。
- 初始旧 M1 exe 在 M2 smoke 明确报 `M2 project creation UI missing`；后续一次测试观察误选 CORS OPTIONS（无 postData），收紧为 method=POST 后通过。未调整生产逻辑绕过断言。
- 当前每项目一个待导入；256 MiB WebCrypto 全缓冲、完整项目列表内存读取是已注明上限。开发 Origin 仍由现有 Go/M1 测试覆盖，本任务真实桌面使用生产 Origin；没有声称开发窗口或 M3–M7 新验收。

### M2.5 审查修正验证（2026-10-15）

- 修复旧组件迟到响应覆盖/清除新导入：所有阶段写入和终态清除同步比较完整旧记录，已推进或被替换时停止旧续链；同意图阶段也不回退。项目选择偏好读写失败独立告警并保留内存选择，gateway ready 不受影响；关键写意图仍须先持久化。
- `node --test scripts/check-m2-ui.mjs` 修前 7 失败/1 通过；修后结合原 recovery 检查 9/9 通过。检查直接转译实际 App/AssetLibrary 源码，控制 upload/commit/deliver 迟到次序、重挂载与续投递，覆盖 A 不能改 B、同 A 不能回退/继续旧链、选择存储读写异常及关键保存失败零 POST。证据 `.runtime/m25-fix-targeted.txt`。
- 修后 `pnpm --dir desktop desktop:build` 与最终 `scripts/check.ps1` 通过；后者含 Go 全量/vet、sidecar、9 项 Node 检查、前端、Rust 与原生 gateway M1，日志 `.runtime/m25-fix-check.txt`，生命周期证据 `.runtime/smoke-bc27920b40814a57ab6d21b02c0e8408/`。
- 最终生产真实 M2 UI/原生重启恢复通过：`.runtime/desktop-release-7ea7d46f089c449986313120528970de/`；真实 M1 WebView/生命周期回归通过：`.runtime/desktop-release-d3085fecfdde47f7895966b5c5109019/`。均使用测试独立 profile，正常关闭无 sidecar/端口残留。并发迟到/存储拒绝使用受控实际组件源码检查，真实 WebView 仍验证现有正常导入、幂等、血缘、隔离和重启恢复；不将源码控序检查冒充真实网络故障注入。

### M2.5 恢复视图再审修正（2026-10-15）

- 修复原请求先完成/推进后重挂载视图仍保留旧 pending 的问题：所有所有权不匹配出口均验证当前持久记录并同步视图（包括空记录），丢弃旧内存文件并给出状态提示；旧链不再 POST，也不改写当前存储。空记录解除导入禁用，推进记录允许用户下一次按当前阶段续做。
- 新增 4 项实际组件源码控序回归，修前 `node --test scripts/check-m2-ui.mjs` 为 4 失败/8 通过（`.runtime/m25-stale-ui-red.txt`）；修后连原恢复检查 13/13 通过（`.runtime/m25-stale-ui-green.txt`）。覆盖原响应先完成→旧视图续做同步 null→可新导入，原上传/提交先推进→首次只同步、再次发当前 body，以及空视图发现新待处理记录；原 P1/P2 覆盖保留。
- 最终 `pnpm --dir desktop desktop:build`、`scripts/check.ps1` 均通过；完整检查日志 `.runtime/m25-resync-final-check.txt`，原生 gateway 生命周期证据 `.runtime/smoke-71561b74beb04b2dbaa1d95e34871163/`。
- 最终生产 M2 真实 UI/重启恢复通过，证据 `.runtime/desktop-release-01d7a08ed2eb45598e5aa81947a5a37c/`；M1 真实 WebView/生命周期回归通过，证据 `.runtime/desktop-release-d0bc793522904eef81ba126bf3f3d16f/`。均正常关闭、无 sidecar/端口残留；控序竞态仍明确属于真实组件源码检查，未冒充 WebView 故障注入。

## M2 整体验收（2026-10-15）

- M2.1–M2.5 全部完成；最终应用代码 e678507，全量 scripts/check.ps1、生产构建、真实 M2 原生重启恢复与 M1 回归全部通过，最终证据见上面的审查修正记录。后续 babb15c 为文档与计划核实，不改变受验应用代码。
- 独立整分支审查范围 fa9cc87..babb15c，结论 Ready to merge: Yes，无待修正发现。已复核严格输入、项目隔离、文件/投递互斥预留、不可变版本与血缘、Windows 原生路径、恢复视图、Origin/CSP 及关闭排空；原始 000001/000002 迁移未修改。
- 已确认的适配均记录于 contracts.md：新增项目/类型/资产端点，提前使用获准 Go1.25.8，单路径提交，投递版本元素字段及 Resource 名称存于 description，Windows 首发与单 gateway 进程边界。taskOutputKey 实际任务产物查找留待 M4，未制造占位任务。
- 远程交付以 main 提交历史和 GitHub refs 核对为准；M2 不代表编辑器、模型生成、agent、分镜或签名更新已经完成。M3 接续实现八图形编辑器与独立持久创作画布。

### M3 实施记录（2026-10-15）

M3.1 已提交 556e086 并通过独立规格/质量审查：无 React 依赖的扁平 ShapeData、几何及矩形/椭圆/线/箭头行为完成。16 项 Node 行为检查、前端类型检查/生产构建和编辑器核心可擦除 TypeScript 检查通过；实际空心矩形误命中复现先失败后修正。没有新增依赖。真实 Canvas 像素、其余四种图形、历史/手势/渲染器、React 标注与持久创作画布仍由 M3.2–M3.5 接续；不把 Canvas 调用记录测试冒充像素验收。

M3.2 已提交 6244c6e 并通过独立规格/质量审查：八图形全部具备行为，Node 24/24，原生 dev WebView Canvas 13/13（DPR1.5、无异常）、前端类型/构建检查通过；真实 PNG192×192 与正常关闭证据在 .runtime/desktop-debug-ad09516de6a744f892c6bae1b94c09fc/。已记录原命中容差例外、短填充文字卡片边界修正，以及模糊有限边距的源码对照；尚未把图形原语验收视作完整编辑器或生产 UI 验收。

M3.3 已提交 bf071b3、b505a5b 并通过独立规格/质量复审：双栈 50 步历史、单手势事务、八工具命中委托、四层合帧/活动预览、自然尺寸导出和图片加载/销毁保护完成。修前完整 Node44/44、原生 WebView19/19，审查修正后交互19/19与核心严格类型检查通过；修正恢复了绘图工具命中已有对象时选择/移动的原行为。原生证据 .runtime/desktop-debug-e8113f545df44cebab51ee927f104023/ 验证受控 DPR 事件后的重建，不代表物理跨屏自动 DPI 已实测。透明 textarea、IME、成对标注由 M3.4 接续，持久项目画布由 M3.5 接续。

M3.4（2026-10-16）已提交 f9b9f8c、81385f8 并通过独立规格/质量复审：React 编辑器、透明 textarea/中文输入、标注配对与单次撤销恢复、源图坐标收集、PNG 新版本保存及原生 IndexedDB 精确字节恢复完成。61 项相关检查和前端生产构建通过；真实 debug WebView 产品编辑及原生重启恢复证据 .runtime/desktop-debug-0875fe532ede40759bffb3b133a41002/，核心19项与普通 M2 原生回归分别在 .runtime/desktop-debug-0c63159f6b1b4fa09d729dfe79f63bbc/、.runtime/desktop-debug-dbffb95d11af4aebbdb2e8c937fd456a/。

审查复现并修正普通导入迟到结果覆盖新选择、源图切换期间旧保存状态锁住新编辑器两项问题；实际 TSX 先6项失败，修后22/22相关检查及前端构建/类型检查通过。选择归属按调用前捕获的用户选择 revision 判定，切换离开再返回也不被旧结果接管；版本标签改为创建时间。未重复与该状态修正无关的原生/后端全套。原生 IME 覆盖真实 Chromium composition/input 与明确标注的合成按键分支，不声称物理输入法候选窗口已验收；当前原生证据为 debug exe+Vite，M3 最终生产 exe 与完整回归由 M3.5 完成。最终产物仍是不可变 PNG 新版本，标注聊天消费待 M5，未声称已接入 agent 或模型生成。

### M3.5 持久画布与最终 M3 验收（2026-10-16）

- 实现原 V3 map document、before/after patch、非嵌套 batch、CAS 与当前最新 document 重放；SQL 原子写 doc/effects/receipt，引用索引延伸共享删除保护。000005 支持原核心缺少的 Composition revision、Resource current head、持久 Version ordinal；五核心表与 000001/000002 未变。
- 创作画布独立于图像编辑器，支持本地平移/缩放、可见节点裁剪、选择、拖动/调整尺寸、嵌套画框/折叠/属性、版本选择、图片/视频/音频/安全文本阅读，并进入既有图像编辑与 PNG IDB 恢复路径。原命令先持久化；未知显式同 body/key 继续，已知冲突刷新并要求重新编辑。
- TDD 红灯：`go -C gateway test ./internal/store -run TestCanvas -v` 缺 Canvas；Node canvas test 缺 canvas.ts。绿灯含当前状态/旧 revision/原 appliedRevision+最新 document 重放、同键异输入、before/父环/重复/悬空/noop/空 batch、UTF-16 边界、跨项目/成员/持久 undo 删除保护、真实 schema4→5 升级/重开/ordinal不复用、跨 Composition 资源变化 revision。
- 实际 HTTP >1 MiB 的 10,000 节点 command 成功提交并读取，>16 MiB 返回结构化 413；content 精确批次、Origin 和未知字段/重复 JSON key 拒绝。Node 验证 activeItem 排除 generation-request、10k 可见节点裁剪、局部父坐标及折叠。
- `scripts/check.ps1` 全量通过，日志 `.runtime/m35-final-check.txt`：Go 全包+vet、sidecar、22 项 M2恢复/编辑器生命周期检查、51 项 editor/annotations/canvas Node检查、TS/Vite、Rust、M1 gateway。最后 M1 gateway 证据 `.runtime/smoke-a9f904065e974acd9bb500dea7860cc8`。
- `pnpm --dir desktop desktop:build` 当前生产 exe 构建通过。真实 release WebView `-Smoke canvas` 的生产 Origin/CSP、实际媒体与 Markdown 导入、节点/嵌套画框移动和尺寸、折叠、内容和版本选择、PNG CORS未污染/Range、图像编辑绘制/撤销/重做、原生 IDB PNG 字节重启恢复、same Resource/source Version 不变、未知画布完整 body 重放、CAS 冲突无自动重提均通过。视频为本机原生 MediaRecorder VP8 WebM fixture，音频为短 WAV fixture，均真实 HTML player 解码播放；不是 AI/provider/M7 生成验收。
- 生产 M2 ordinary import+restart 回归通过：`.runtime/desktop-release-1510ee2281464915a7d4132b7887eb5a`；生产 M1 WebView/Origin/关闭通过：`.runtime/desktop-release-ca9adc23eb7d4adba93129d0620d1b1d`。均验证正常关闭后 sidecar/8001 释放，无 renderer 未捕获异常。
- 现有 M3.1–M3.4 各阶段的八类 Shape/工具/历史/透明 textarea/IME/区域标注证据仍按各自原文区分 debug fixture 和真实产品 UI；本阶段重新运行对应全部 Node 检查。最终 production Canvas-to-Editor 集成证明真实图像编辑及不可变 PNG 恢复链路，没有把 debug 的源码 import harness 标成 production。
- 已知明确范围：仅原 canvas.patch/canvas.batch 启用；其他非 MVP content commands 返回结构化拒绝；M4 任务暂不存在；安全文档预览≤1 MiB；画布 UI undo/redo 暂一层、重启快捷按钮不恢复，服务端持久 undo 仍保留引用；没有历史截断/回收接口。当前源码适配详见 contracts 的 M3.5 段。未新增依赖、网络协议、模型/provider 或生产测试钩子。
- 整体审查修复：待确认画布记录冻结本机 normal/undo/redo 模式，响应丢失后同会话或重载续确认保持按钮含义与原 HTTP body/requestId；旧版无模式记录保守清空快捷操作，损坏模式禁止 POST 并保留记录。`check-canvas-recovery.mjs` 执行实际组件源码，覆盖 12 项受控 React/存储/HTTP 边界检查；不作为新增原生 UI 证据。

Final M3.5 canvas proof: `.runtime/desktop-release-6722d68171194ac6a8dcbd5cfeb69b97/` contains `canvas-expected.json`, `canvas-production.json`, `canvas-before-restart.png`, readable `canvas-recovered.png`, native `video-playback.json` / `audio-playback.json`, and owned `webview-profile.json`. Earlier `.runtime/desktop-release-9d0ed716731c44c98391fa9245c003ec/` also passed all functional checks. The 150 ms media sample first observed an audio clock at zero; replaced with bounded native timeupdate observation, not a product workaround. Initial sandbox-only desktop startup timed out; approved external native execution passed.

M3.5 独立任务复审（697b542）通过。初次审查发现共享枚举判断接受拼接值，已改为原始输入精确 token 比较；四个实际入口覆盖全部合法值以及拼接、首尾空白、空串、部分字符串，拒绝均不改变完整文档/revision/receipt；实际 HTTP 同样拒绝。覆盖检查 `go -C gateway test ./internal/store ./internal/server -run TestCanvas -v` 通过。先前生产证据中 `observedUpload` 会被后续媒体导入覆盖，不能单独引用它证明 PNG 恢复；原即时 ID/SHA 断言及持久 IDB 证据有效，新 smoke 单独保存 `recoveredPNGUpload` 供后续证据读取。最终整里程碑检查与整分支审查仍待记录，不以单任务审查代替。

## M3 整体验收（2026-10-16）

- M3.1–M3.5 全部通过独立规格/质量审查。整分支审查范围 7be80dd..dd4933c 后发现一项恢复边界问题，59541dd 修复并复审为 Ready to merge: Yes，无遗留发现：撤销/重做模式随本地 pending 冻结，初次发送和显式继续均使用原模式，HTTP body/requestId 不变；旧无模式记录重放后保守清除两个历史快捷动作。新增实际组件检查12项，涵盖同窗口/重载/损坏模式，原问题先失败后通过。
- 最终应用提交 59541ddb17decc03c7e4a4f152ce8649eb597c75。`scripts/check.ps1` 在该代码通过：Go 全包/vet、sidecar、34项恢复/生命周期检查、51项编辑器/标注/画布检查、TS/Vite、Rust、真实 M1 gateway 生命周期。完整日志 `.runtime/m3-final-approved-check.txt`，gateway 证据 `.runtime/smoke-e02d256e1e4e4e66a03ff5231b5c2711/`。
- 当前最终代码 `pnpm --dir desktop desktop:build` 成功，保留完整日志 `.runtime/m3-final-approved-build.txt`。随后生产 exe `-Mode release -Smoke canvas` 初次及原生重启均通过，正常关闭无残留 gateway；证据 `.runtime/desktop-release-d6f0d74382664da0a1ec38cfb85980c9/`，日志 `.runtime/m3-final-approved-native.txt`。生产 Origin、节点/框架/媒体/安全文档、图像编辑及不可变新版本、PNG IDB 字节与原上传号、画布原命令重放/CAS均由实际窗口检查。最终独立保存的 recoveredPNGUpload 与冻结 op.uploadID/完整SHA-256相等，renderer exceptions=[]。
- 普通 M2 原生导入/重启、M1 原生窗口回归先前在应用2965e15通过，证据见 M3.5；后续仅画布非法枚举拒绝和本地 undo恢复元数据修正，M2业务实现不变。最终全量恢复/生命周期检查及生产 Canvas 集成均重跑通过，没有声称把全部旧原生用例重复执行。早期丰富编辑器/IME debug 证据、受控 DPR/合成 composing 按键及物理硬件未验限制继续按原记录区分。
- 原始 000001/000002 迁移保持不变；Go1.25.8、标准库/原生 IndexedDB 与既有依赖复用，无新增业务依赖。支撑元数据、限定 patch/batch、一步 Canvas 快捷历史、只读文本1MiB及持久 undo引用暂不回收等范围已在 contracts 明确。M3 不代表供应商/AI生成、agent聊天、分镜或签名安装更新完成。
- README 更新为实际 M1–M3 能力、Go工具链及生产/开发检查边界。远程交付以 main 的 Git refs 核对结果为准；下一阶段 M4 接入自定义本地/云端供应商、持久任务、SSE和费用确认。

### M4.1 自定义供应商设置（2026-10-16）

- 完成 image/openai-images、video/ark-video 已知协议模板的本地/云端连接、能力与价格声明、默认切换、停用/软删除及不执行生成的模型列表探测；LLM 配置按 M5 接入。没有把模板配置或探测当成真实生成验收。
- 993fcd5、5c71b91、f407b8e；独立规格/质量复审通过，三个问题均关闭：未知密钥轮换跨重启保留原请求、可选数组统一输出 []、真实 DPAPI 解密/临时写入/原生发布失败覆盖。凭据按不可变加密版本先落盘，SQLite 只保存指针，旧快照可继续解密；无新依赖或 CGO，核心五表与 000001/000002 未改。
- 专项 Go providers/server、vet、TypeScript/Vite 和生产桌面构建通过；常规 Node 检查已包含供应商恢复，合并检查 52/52 通过。实际密文破坏及 CreateTemp/MoveFileEx 失败保持旧配置/密钥可用且无成功回执或明文临时文件。
- 当前生产 WebView 保存、默认切换、401 脱敏、已提交密钥轮换响应丢失后的原生重启/原回执查询（零 POST）、未提交请求刷新后的同密钥指纹/同 body/key 重放、图片/视频空数组编辑、状态对比度均通过。最终证据 `.runtime/desktop-release-269559bcca5b47139cf64ff263ec99b4/`；正常退出与重启后无 gateway 残留，无 JS 未捕获异常。只使用本机模型列表 fixture，不是实际供应商生成。
- 真实落盘只读扫描：schema version7、dirty0、13 条供应商回执、3 份 DPAPI 文件；DB、日志、加密文件及请求日志未发现三个测试密钥原文。浏览器持久记录仅非密钥输入和不可逆指纹，回执查询不返回密钥/指纹/credential_version。
- 曾因无关代理出站连接占用本地 8001 阻塞；连接自行释放后完成验证，没有终止代理、改端口或修改网络设置。M4 尚未整体完成或推送。

### M4.2 任务与事件实施（2026-10-16）
M4.2 实现提交 4d9f485，审查修复提交 1f7f085；专项及全量 Go/vet 通过，原生 Gateway 进程冒烟通过，独立规格与质量复审通过。模型适配器、费用批准与媒体投递分别由 M4.3–M4.5 接续，当前没有真实模型调用验收。

M4.2 独立审查在 1f7f085 复审通过（Spec compliant / Quality Approved），四项问题全部关闭。实际红灯覆盖已知/无上游 ID 的收集重启、排队/活动收集取消、普通投递缺失/跨项目类型、复合 query key；修后 tasks/events/server/store/delivery 五包测试与相关 vet 通过。取消保留首次结果/错误证据，已知结果恢复只提供同 attempt 显式收集重试，不启动 Submit/Query/Collect；缺失类型恢复 blocked/not_found 分类。当前阶段仍无真实模型执行、费用批准或产物投递，后续 M4.3–M4.5 接续。M4.2 原生冒烟覆盖后台生命周期，最终 M4 生产桌面整体验收另行执行。

### M4.3 图像能力与报价（2026-10-16，独立复审通过）
实现提交 ded0dca：已知 OpenAI Images 协议模板的 JSON 生成/有序 multipart 编辑、受限参数 schema、受管遮罩、整数报价和冻结输入、图像结果持久化及安全收集。生成 HTTP 新请求/重放均为 202 {task_id}，人工计费门禁仍关闭，最终 Delivery/界面由 M4.4–M4.5 接续。
实现者最终全量 Go 测试及 vet 通过；控制端在该提交重建纯 Go gateway 并实际启动验证健康、Origin、端口冲突、重启、关闭及父进程 EOF，证据 .runtime/smoke-7076129473e74c3bbffea5f312324894。该实现快照之后的独立复审和修复记录见下段。所有图像协议调用均为本地 httptest fixture；没有真实供应商、付费生成或最终媒体投递验收。
M4.3 独立复审在 f033c6f 通过（Spec compliant / Quality Approved）。ded0dca→b5d600f 修复属性 schema 大小写、精确数值与遮罩回执窗口；f033c6f 修复普通上传日志误认遮罩，通过原意图中的服务端子 UUID 绑定归属。定向恢复/数值/归属测试、全量未缓存 Go（b5d600f）、最终 providers/server/tasks 覆盖测试与 vet 通过。旧内部格式的已完成回执可重放；未完成且缺归属元数据的意图明确拒绝，不推断归属或自动重新生成。未完成的真实调用与最终媒体投递限制保持不变。

### M4.4 人工费用门禁（2026-10-16）
b120287 已通过独立规格/质量审查。初次提交/新 attempt 的报价、确认卡、回执、任务准入和事件共用事务；原回执恢复只允许尚未发送的 SQL claim，启动不自动提交。共享卡片持久化原请求，阻止损坏存储与迟到清理；SSE 单连接、持久游标、项目/会话过滤和旧连接取消。
全量未缓存 Go/vet、最终 questions/tasks/server 覆盖、前端 events/pending 6 项、editor 47 项、Rust 1 项及生产构建通过。实际 release WebView 初次/重启证据 .runtime/desktop-release-3df90b4072f74ab69792ae3e42938a79：拒绝0POST、双击/丢响应/原key恢复总1POST、旧卡409、字面文本、损坏记录阻断、重载/重启0自动写、Last-Event-ID、正常进程回收。该原生快照早于最终 SQL/拒绝/空记录小修复，未冒称最终 binary 原生全验。
非阻塞项：过期卡片直接拒绝入口交 M4.5 UI；换行警告如实记录。历史卡片暂保留并整体列出。真实付费供应商/生成质量/最终 Delivery 未执行，后续按阶段接续。

### M4.5 图像投递与工作台接线（2026-10-18，独立审查前）

已接入冻结输出身份→安全收集→共享 Delivery→Resource/Version/Asset→画布的实际路径。投递回执与任务完成在同一 SQL 事务提交；重启先恢复原投递日志，再考虑收集，保持预留 Version ID/ordinal；取消后的迟到结果通过服务端创建的结构化采纳卡处理，任务本身保持 cancelled。新增任务分页与 coverage、原请求持久化、StrictMode/迟到响应归属保护、参数声明编辑；标注修复使用实际选中源 Version，沿用同一 Resource，并保留 source_version_id 与原资产。

原生记录来自本地协议 fixture，非真实供应商：生产 generation 初次/重启 `.runtime/desktop-release-a4f3c63f753a4894bb8e7b5eca482838/`，确认前0调用、批准后落库/画布、PNG遮罩与有序multipart、修复血缘、仅续下载、取消/采纳、重启0控制写均有记录；生产 providers `0b1d831cbf0a4782a0320cc55060f9cc`、questions `e581362e07ba4227a0204e57cfc7c098`、Canvas `12d363a320074f1cac77d07c6ab53945`、M2 `613f23dcd95f43dba0b2aa2ce0f5baed`、M1 `94dce5c9e979453293ba1a9d8c9d69f0` 为独立生产回归目录。debug 编辑器源码检查使用 `.runtime/desktop-debug-d45a30506889429c8eb54d73fbb29c48/`，不标成 production；物理 IME 候选窗和跨显示器 DPI 限制沿用 M3 记录。

收尾提交为 46555f7，报告 `.superpowers/sdd/m4.5-report.md`；本次全量检查通过并保留 `.runtime/m45-final-check.txt`：Go 全包（缓存）/vet、39 项组件检查、62 项 TS 检查、生产前端构建、Rust 及新 Gateway 进程冒烟；另以 `-count=1` 重跑 11 项生成 HTTP/SQLite/文件系统集成测试通过，日志 `.runtime/m45-generation-final.txt`。收尾未改产品代码；先前原生记录通过文件/二进制时间关联，但没有内嵌构建哈希，因此不声称本次重新跑过完整原生矩阵。新审查者已开始独立任务审查，随后还有整 M4 审查；提交与绿色检查不等于两个审查已通过。真实凭据支持的文生图、标注修复、模型效果与实际计费验收仍为 **未执行**；不阻止后续独立工程工作，不据此声明完整产品已完成。

M4.5 独立任务审查在 46555f7 发现三项 P2：已完成画布阻断原生成请求恢复、取消归档后的文件保护遗漏、迟到的低 ordinal 版本覆盖较新的 currentVersion。91d7d31 在共享逻辑修复，并以实际组件恢复/公共文件操作/HTTP+SQLite 顺序检查先复现后通过；14 项生成矩阵与普通 blocked 文件控制用例通过。当前全量 `.runtime/m45-fixwave-check.txt` 通过（43 组件＋62 TS、Go/vet、前端构建、Rust、新 M1进程冒烟）；生产构建和真实窗口初次/重启 `.runtime/desktop-release-3ec69bfdd52349b2b6ecc6aaac752978/` 通过，新增丢生成响应→批准完成→原请求恢复的实际点击路径，原 body、任务数、调用数和画布保持正确。`.runtime/m45-fixwave-evidence.json` 将提交与二进制、sidecar、前端 bundle、检查/构建/原生日志 SHA-256 关联，产品源码与提交相符。当前正由原独立审查者复审，尚未宣布 M4 整体通过或推送；真实调用仍未执行。

M4.5 在 91d7d31 的独立复审结论为 Spec compliant / Task quality Approved，R1–R3 均已关闭；复审核对全部修复补丁、RED/GREEN 日志和 8 项制品哈希。M4.1–M4.5 单任务工程门禁至此全部通过。下一门禁为 c8372f5 起的整 M4 分支审查；尚未合并/推送。真实调用未执行的状态保持不变。

M4 整阶段审查的三项 P2 与一项报价投影 P3 已由 4d0f1fa 修复，等待原整阶段审查者复审。实际 RED/GREEN 覆盖部分200响应体续取、失败/取消中的新旧目标重命名与同名冲突、交付后/未提交取消/明确拒绝/不可变坏结果的保护释放、257标签的报价/准入拒绝及256边界、零价/普通报价/新 attempt 的权威积分投影。共享读取保留网络 I/O 与过大/坏 base64 的区别；目标保护按当前 attempt 的实际恢复消费者决定；`delivery.ValidateOutput` 在报价/准入与投递复用；报价投影与卡片在同一事务保持 revision 一致。

最终代码全量 `.runtime/m4-final-check.txt` 通过（Go/vet、39 项生成集成子用例、43 组件＋62 TS、生产前端、Rust、新 M1 进程冒烟）；另有未缓存覆盖包与边界专项记录。生产构建 `.runtime/m4-final-release-build.txt` 通过45.54秒；实际相邻 gateway 与本次构建输入副本 SHA-256 一致。真实窗口 `.runtime/desktop-release-77d895567385410b857a2537f429e359/` 初次／进程重启通过，覆盖原响应丢失恢复、部分200下载续取、批准前已知报价投影、标注修复血缘、取消/采纳、重启0生成/恢复/回执控制写；没有把只读 canvas/content POST 误称为零HTTP请求。`.runtime/m4-final-fix-evidence.json` 关联提交与12项构建/运行制品哈希；源码与已验证提交相符。早期构建、测试与原生快照的限制保留在本地报告，不冒充最终代码。真实供应商调用仍未执行，尚未宣布整体通过或推送。

## M4 工程整体验收（2026-10-18）

独立整阶段复审在 c531dfa（产品代码4d0f1fa）结论 **Ready to merge: Yes**，R1–R3 与两项P3全部关闭，无遗留可操作发现。审查者读取完整修复补丁与报告，追踪共享读取、目标恢复/采纳和报价同事务接线，独立复算全部12项制品大小/哈希及两个sidecar一致性；未以重复绿色测试代替审查。最终全量、生产构建、当前真实窗口初次/重启、RED/GREEN边界与证据限制见前述记录；核心五表和000001/000002保持不变，无新增业务依赖。实际运行产品与4d0f1fa源码相符，之后仅文档状态更新。

依据已确认总计划§6，无真实凭据时继续独立工程工作。故此结论表示M4工程交付可整合，不表示真实供应商兼容、生成质量或付费验收已通过；真实文生图及标注修复仍为 **未执行**。M5–M7、真实端到端视频、签名安装与发布尚待完成；完整产品目标未完成。Git远程交付以随后实际ref核对为准。

### M5.1 受管 opencode 与 LLM 配置（独立复审通过）
M4 已快进整合并推送 main263ffdc50a5c79bbf110a9c80e62b5dcca457c14，远程 ref 已实际核对；M5 在 codex/m5-agent-chat 从该基线实施。固定官方 opencode1.19.0 档案已下载至项目忽略目录 `.tools/` 并校验官方 SHA-256；这只是制品获取证据，不代表运行时隔离、鉴权或模型推理已验证。M5.1 正接入隔离目录、私有启动凭据、双 sidecar 生命周期和可配置 LLM，MCP、聊天、技能/流水线接续；真实 LLM 调用未执行。后续实际结果另行记录。

实施中进展：实现者已运行固定 binary 的版本、健康及 `/doc`，完成配置默认拒绝工具、保留插值语法拒绝、DPAPI 配置与回执恢复、Origin、鉴权和 Rust 端口冲突定向检查。正在完成原生双子进程与隔离验收、全量检查和报告；尚未提交或通过 M5.1 独立审查。这些结果不代表真实 LLM 推理验收。M5.1 新增独立 `000008_llm_settings` 支撑迁移，后续计划编号顺延为 `000009_agent_sessions`、`000010_pipeline`，核心五表与既有迁移保持不变。

M5.1 已提交 `dba3462`，尚未整合/推送。最终生产程序初次/恢复/占用端口检查通过（`.runtime/desktop-release-8ce32f8f1e7748d8b4c864f75e444d41`），旧图像/视频供应商设置及恢复回归通过（`.runtime/desktop-release-6eb02d72abb14477b8d2d4af8c22b8b0`）；正常退出无自有 gateway/opencode 残留，不触碰原占用监听。实际固定运行时鉴权、恶意配置/技能隔离、有效权限和 `/doc` 检查通过；无真实推理。全量检查在最后一处状态探测修复之前运行，该修复之后另有覆盖测试、重建和原生验证，范围见实施报告。已启动完整 `263ffdc..dba3462` 独立审查，结论未出；M5.2–M7 尚未实施。

独立审查发现 I1：agent 配置目录/ACL 准备失败会在 gateway 启动前中止整个工作台。修复提交 `dcac89e` 将失败限制在 agent，私有子进程环境传递失败标记，保留三方法控制协议和权限失败关闭规则。修前原生失败已复现；修后不可用 `agent-host` 场景仍能读取健康/项目，未启动或探测模型主机（`.runtime/desktop-release-d160032cf6c04d6e88d9f4d9839bf4a0`）。Go/vet/Rust、生产重建和正常初次/重启/端口冲突/退出回归通过（`.runtime/desktop-release-91dee088a4614d9ca5a20e4806b3046b`）；当前三份制品哈希已实际复核。独立修复复审中，M5.1 尚未验收；真实推理仍未执行。

M5.1 独立复审在 `dcac89e` 通过（Spec compliant / Quality Approved），I1 已关闭，无剩余可操作发现。当前三份制品与 I1 哈希清单一致。此验收限于受管运行时、配置/鉴权/隔离和正常生命周期；MCP 工具、原生 question 对话、每轮调用预算和真实推理仍留待后续验证。M5.2 从该提交实施，M5 整体尚未完成或推送。

### M5.2 官方 MCP、共享准入与调用预算（独立复审通过）

实现 `47c5bfa`，审查修复 `b589590`；独立规格/质量复审通过，两项发现全部关闭：对象编辑工具的 RawMessage 推断成字节数组，及 MCP 投递误用 user 来源。当前 12 个已实现工具使用官方 SDK1.4.1、精确 Origin/私有令牌、共享 Go 服务与冻结输入；metadata create/update/replay/嵌套拒绝、MCP agent/HTTP user/unknown 跨入口保留首次来源均有真实 HTTP/SDK 检查。核心五表与迁移000001/000002未改，新迁移000009保存会话、轮次、调用账本和项目规则。

全量检查（Go/vet、43项 Node、62项 TS、前端构建、Rust3项、M1进程冒烟）在修前实现通过。固定 opencode1.19.0 实际本地 HTTP/SSE 模型 fixture 证明：精确12个 design_design_*+question 目录、两次并行 MCP 调用、原生 question ID/callID 映射与重复事件复用同卡，持久预算3次，模型 HTTP2次；不是供应商真实推理。每轮最多40次，41次在副作用/卡片前拒绝；MCP 无原生 callID 时保守计数实际执行请求，重放业务 requestId 不免计数。工具等待穿过 billing，普通 waiting_user/unknown/终态结束；agent retry 返回 waiting_user，由应用人工操作，不伪造批准。

修前生产构建/原生 agent 初次与恢复、端口冲突、正常退出通过（`.runtime/desktop-release-d2a379234ae74b17a34d2da6caf199b3`）；原图像生成链初次/恢复回归通过（`.runtime/desktop-release-21596aa2a2b24860982213ef537cf2e2`）。当前修复另有 MCP/server/delivery 覆盖测试、vet、新 Go binary 及实际固定 opencode 目录/并行/question 检查；这些不冒充重建后的完整桌面包。M5 收尾必须重建包含 `b589590` 的生产包并整体验证。

源码确认 opencode 外层处理器另有5次重试，SDK retries=0 不能独立防重发，其 Bun fetch 也不继承 Go 出站保护。生产推理入口尚未开放；M5.3 先实现受控推理转发、冻结凭据/目的地及已转发请求防重复，再开放持久聊天。10秒受理、事件恢复、原生人工回执转发与排队仍待 M5.3；真实 LLM 推理及真实供应商调用均未执行，M5整体未完成或推送。

### M5.3 持久聊天与受控推理（独立审查修复中）

实现提交 `e21f370`：持久会话/消息队列、10秒受理宽限窗与全局事件、取消、原生提问的人类回执桥、冻结供应商配置及受控推理转发。新增 `000010_chat_bridge` 支撑迁移，后续流水线迁移顺延 `000011`；核心五表和既有迁移不改。最终未缓存全量 Go、agent/questions/events 覆盖检查、vet 和独立 gateway 构建通过；当前生产桌面包尚未重建，不据此声明 Tauri 集成通过。

固定 opencode1.19.0 的实际本地 HTTP/SSE 模型 fixture 已验证：5xx/重定向/断流各仅一次供应商 POST、重定向目标零访问；人工 question 回执后允许真实下一步工具结果请求，模型请求共两次。MCP fixture 通过生产 NewHost 启动，证明私有转发、精确工具目录、并行工具和预算接线。它们是实际运行时搭配合成响应的测试，不是真实模型推理；Responses/Anthropic 只有 Go 协议检查，尚无原生真实调用验收。

独立审查发现三项 Important：终态后一次关闭失败会遗失清理工作并阻塞后续轮次；取消受理未知消息时跳过可用快照对账；重启后丢失的原生提问仍可操作。正在同一实现者的一轮集中修复中，要求实际 RED/GREEN、原请求不重发、旧 epoch 关闭后才推进新项目及旧卡失效。M5.3 尚未验收，M5.4–M7 待推进，M5 整体尚未整合或推送；真实 LLM/付费供应商调用均未执行。

M5.3 的三项 Important 已在 `6d7571c`、`2e46d93` 修复，原独立审查者最终判定 **Spec compliant / Quality Approved**，无剩余发现。清理失败会保留原 epoch 并恢复私有控制操作；取消受理未知消息继续核对原消息/父消息快照，清理完成前不启动下一项目；原生提问即使在卡片提交后、映射写回前崩溃，也能从已完成的项目/会话范围幂等回执找回原卡片，再原子关联、失效并发事件。既有人工回执保留，原请求可读回，不会重建卡片或重发 native reply。

针对性 RED/GREEN 覆盖空闲/断连接/关闭 epoch 三处故障、非本轮快照拒绝、卡片映射为空的两个回执状态、跨项目/会话保护、失效历史不再反复写库/唤醒，以及稍后出现的原 ask 回执仍可恢复。最终未缓存全量 Go 检查通过（agent33.165秒/server44.766秒），相关包、vet、构建通过；两个旧测试的固定等待在并行负载下失效，已改为明确受理通道，失败日志保留。当前独立 gateway SHA-256为 `272ed430a29f857aea67f5a6eaf93acc8003139219b26e4951cceb33d54f48c7`。

这些恢复测试使用真实 Go/SQLite 控制逻辑与受管 HTTP fixture；之前的固定原生运行时协议证据保留，不冒充本轮原生重启验收。当前桌面包重建、完整聊天 WebView 和最终工具/文本链仍待 M5.4/M5.5；真实 LLM/供应商调用、原生 Responses/Anthropic 调用未执行。M5.4 从 `2e46d93` 开始，M5 整体验收与推送尚未完成。

### M5.4 TipTap、引用与聊天界面（独立复审通过）

实现 `67473f5`、审查修复 `70869c8`；原独立审查者最终判定 **Spec compliant / Quality Approved**，两项 Important 均关闭。实际 TipTap 文档收集嵌套有序资产引用，显式快照图像标注及源版本；现有服务端队列、单一 SSE、历史读取和人类确认卡继续共用。未知发送/撤回保留原 body 与 requestId，切换项目、刷新和重启不自动重发。安全文本渲染、IME、键盘 mention/确认、标注、忙时排队及原请求恢复均有组件和真实窗口证据。

审查发现并先复现后修复：已有文本/仅标注草稿时发起撤回编辑会卡住；排队消息已出队时明确的撤回409被永久当作未知。现在在任何写入前保护现有草稿；仅对已知未生效的撤回拒绝释放其自有恢复记录，其他409/幂等冲突/丢失响应仍保留原记录。新发送仍需人工动作和新请求号。修复只改前端，无后端、API、核心表或新依赖变更；严格匹配既有错误文本的上限和未来结构化原因升级路径已写入注释。

当前完整 `scripts/check.ps1` 通过：55组件、68 TypeScript、全部 Go/vet、3 Rust、M1进程冒烟；生产桌面包重建通过42.40秒。实际窗口 `desktop-release-7803cdcb21104c48b66c25435189c1a0` 验证草稿保护零撤回写入、真实服务端出队后的409、恢复编辑、仅明确新发送和物理重启零自动写入，退出无自有子进程残留。更广的 mention/原生question/断线/原请求恢复窗口证据对应实现提交 `67473f5`，由当前组件和针对性窗口回归补充，不冒充所有路径均在修复后重新运行。父控制器实际查看聊天截图，并独立复算原40项与修复38项源码、制品、日志和截图的大小/SHA-256，两个清单均一致。

当前程序 SHA-256 `E70BD440628B2AFDA4656C372357A5907BB40F61B7D3609FB85A7658FB7A91BA`，Go两副本均为 `269D897ED43B39D33886288D8E3AF511AC9FE8E2BEB02C02B1E8C571EEC7A189`。前端入口约902.93kB（gzip291.21kB）的构建警告保留为非阻塞 Minor，未声称性能优化或无警告。窗口使用真实 Tauri/Go/固定opencode及合成供应商，真实 LLM、付费图像/视频和原生 Responses/Anthropic 验收仍未执行。M5.5 从 `70869c8` 实施，M5整体验收、推送和完整产品目标尚未完成。

### M5.5 工具、独立文本任务与画布集成（2026-10-18，等待独立审查）

实现提交 9798d18：补齐18项 design 工具、中文 media-agent 提示词、受管文档修订和人类回执绑定的项目规则；素材搜索明确限定本地图片及用户登记声音目录。文本生成复用任务、费用卡、投递和受保护推理通道，使用独立、完全禁用工具的 opencode session。媒体元数据使用自有固定 ffprobe 分发；不调用原应用二进制。000011_text_inference 支撑迁移保存独立推理准入，原五张业务表与000001/000002不变，后续分镜迁移顺延至000012。

最终 scripts/check.ps1 通过：全部Go/vet、56项组件、68项TypeScript、前端构建、3项Rust及M1进程冒烟；测试 fixture 端口分配修正后另跑 server 包和 vet 通过。当前生产构建47.04秒；入口约903.87kB的体积警告保留。真实固定 opencode1.19.0＋本地模拟供应商验证18项工具目录、嵌套文本零工具、费用/规则人类回执、agent来源版本及画布节点。生产 tools 初次/恢复、generation 回归、chat-fix 初次/修复/物理重启、固定 ffprobe WAV与拒绝播放列表检查通过，无自有子进程残留。

本地报告 .superpowers/sdd/m5.5-report.md 保存实际命令、失败记录及证据限定；.runtime/m55/manifest.json 关联62项源码、9项制品、70项证据。控制器复算全部141项大小与SHA-256一致。读取型 POST /api/canvas/content 被旧聊天冒烟误计为控制写入，修正仅过滤该精确读取路径，保留所有聊天和画布修改断言。此前构建锁、测试环境启动和 fixture 断言失败均保留记录，不算通过。

当前处于独立单任务审查，之后仍有整M5审查；未声明M5已验收或推送。真实凭据支持的LLM/图像/视频调用、原生Responses/Anthropic推理、签名安装更新仍为未执行。本地协议与真实桌面进程验证不代表真实供应商效果、能力或费用验收。

M5.5 独立任务审查在9798d18发现两项Important：普通资源节点抢先匹配精确生成占位节点；当前资源选择与单独输出身份混淆，导致同资源多候选遗漏或重复画布变更。904f314在共享PlaceDelivery修复：精确占位优先、普通节点仅复用当前单输出、候选与未放置旧输出按Version分配节点。原回执重放不重新放置已删除节点；不改协议/迁移/依赖。

实际行为RED→GREEN覆盖6种节点/输出组合、归档unknown→数据库重开→原投递完成、原actor与版本保留、删除后重放不复活。修复代码全量检查通过，生产构建41.92秒；实际窗口generation初次/重启 ddb9c54d04f847f7b8507d99f4938223通过，同资源双候选只提交一次、保留两个占位ID/布局和所有旧节点、重启零控制写入，两个图像节点实际加载。截图保留旧节点重叠事实，没有宣称6个节点均独立可见。控制器查看截图并复算修复清单65源码＋9制品＋14证据共88项大小/SHA-256全部一致。初次M5.5工具/聊天证据早于此修复，以当前共享投递专项和生成窗口回归补充。当前等待原审查者复审，尚未整M5批准/推送；真实调用仍未执行。

M5.5 在904f314复审通过（Spec compliant / Task quality Approved），I1/I2关闭，无遗留Critical/Important。原审查者独立复核修复88项清单一致；定向命令的tasks包无匹配用例提示如实保留，随后全量任务包已运行通过。M5.1–M5.5单任务工程门禁全部通过；接下来整M5审查。真实调用保持未执行，未用模拟供应商代替真实验收。

### M5 整阶段审查修复（2026-10-19，等待最终复审）

整M5审查范围263ffdc..cef5e53发现一项Important：Chat.run创建轮次后，工具初始化或dispatch绑定失败会丢失清理责任，导致全局聊天队列卡住。9f497a6仅修改chat.go与一个测试文件，发送前确定未提交的消息保存单一错误终态，随后复用原idle/drain/disconnect/epoch关闭屏障；只重试终态持久化和私有清理，不重发提示词或供应商请求，不扩大真实unknown受理的恢复条件。

五种实际行为RED→GREEN覆盖断连/epoch关闭/连接失败、已生效控制响应丢失、SQLite绑定失败；附加关机重启、原请求零提交、跨项目屏障、旧作用域拒绝和原有正证据unknown取消控制通过。最终全量检查通过（56组件、68TS、Go/vet、3Rust、M1），生产构建46.43秒；当前生产chat-fix初次/修复/重启bfae96d91f4942219e7e1e983a0eedf0通过，正常回收两子进程。故障注入为Go/HTTP控制器fixture；固定opencode生产窗口检查为正常聊天集成，两者不混称原生故障注入。首轮窗口因8001暂时不可绑定未启动聊天，保留fcdbcf...失败记录；只读诊断后端口可用，未终止无关进程或改网络设置。

控制器查看当前安全文本/重启草稿截图，并复算66源码＋9制品＋19证据共94项大小/SHA-256一致。以前工具、文本、画布证据保留其对应提交，未宣称全部在本次二进制重跑。现有903.87kB入口构建警告保留为非阻塞项。等待原整阶段审查者对9f497a6复审；尚未推送M5、未开始M6，真实供应商调用仍未执行。

M5 最终工程门禁在9f497a6复审为 Ready to merge: Yes，无遗留Critical/Important。按已授权流程合并与推送；全量检查、构建、原生窗口证据对应同一产品源码，后续仅验收文档修改，不重复运行已通过且未变更的测试。非阻塞体积警告与真实调用/签名发布限制保留，按总计划§6继续M6工程，不宣称完整产品已完成。

M5 远程交付核验（2026-10-19）：快进合并至main并push成功，git ls-remote origin main实际为bb20c6f683d8c95b0eb2b9bb408f02e1a7de83af，与本地一致。M6从该提交在codex/m6-storyboard-pipeline实施；未重复M4.2或已关闭任务。

### M6.1 技能接入实现（2026-10-19，等待独立审查）

1e022a8已提交两份完整业务技能、受管引用、安装保留用户改稿与随包版本登记、技能列表/内容及分页、聊天选择/预览、自动触发与显式关闭。技能正文和引用按revision/hash冻结在原chat.send幂等回执，旧请求先重放，排队/活动/重启继续不读取后改文件；原生工具权限不扩大，无新增依赖/迁移，分镜仍预留000012。

全量scripts/check.ps1通过：Go/vet、57组件、69TS、3Rust与M1；生产构建43.35秒，现有入口体积警告保留。固定opencode＋真实生产WebView、本地合成供应商三阶段5881ef79a3da49b38a08e36e47fb5572通过：口播自动选择、图片方法排队、用户改引用后继续、物理重启、显式重新采用和关闭。六次实际供应商消息的完整方法前缀与各自持久回执逐字相等；未额外推理，正常回收两子进程。首轮脚本因文本片段数组被JSON转义而断言失败，实际前缀已存在，保留d7eece...失败记录；修正脚本后通过，产品未因该失败改动。

控制器查看真实技能选择/预览截图skills-visual.png；该补充只读重开，零POST/供应商调用。早期截图停在画布，不作为技能视觉验收。控制器复算清单25份提交及工作源码双哈希、5制品、25证据，共80项SHA-256全部相符。既有生成脚本从CRLF规范文档生成media_prompt.go，与未生成HEAD的LF字节不同；实际构建输入已另存/哈希，报告明确区分，不冒称二进制来自逐字未生成HEAD。详情本地m6.1-report.md和m6.1-evidence-manifest.json。

当前独立M6.1规格/质量审查中，未宣布任务或M6整阶段验收。真实LLM方法遵循和付费供应商验收仍未执行；NSIS实际安装未执行。分镜revision、批准失效与Prompt合同由M6.2–M6.4接续，技能方法说明不冒充后端门禁。

M6.1 在b859a62复审通过（Spec compliant / Task quality Approved），无Critical/Important，前端coverage缺口已关闭。实际新增用例先失败后通过，组件14/14与tsc/Vite通过；复用既有allPages，未新增分页实现。原生生产程序证据仍对应1e022a8，未冒称包含该前端后续修正；后续M6最终生产构建再纳入。现有体积警告保留，真实LLM方法遵循仍未执行。继续M6.2，M6整阶段不提前标完成。
