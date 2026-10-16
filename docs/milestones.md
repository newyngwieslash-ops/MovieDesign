# 里程碑验收

完整后续计划见[总计划与六份分计划](superpowers/plans/2026-10-15-moviedesign-complete.md)。M2–M3 已通过全量检查与独立整体验收，M4 正在实施，M5–M7 待实施；计划中的测试命令不是已执行结果，各阶段证据另行记录。

| 里程碑 | 范围 | 状态 |
| --- | --- | --- |
| M1 | Tauri、gateway、SQLite、空工作台、生命周期 | 验证通过 |
| M2 | 对象模型 CRUD、幂等投递、unknown 重放 | 验证通过 |
| M3 | 八图形编辑器、历史、标注、持久创作画布 | 验证通过 |
| M4 | 图像模型、能力契约、任务状态机、计费与事件 | 实施中，M4.1–M4.2 已验证 |
| M5 | opencode、MCP、提示词、聊天与确认卡 | 待实施 |
| M6 | 两个技能、分镜批准、Prompt 锁定与 Preflight | 待实施 |
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

### M4.3 图像能力与报价（2026-10-16，独立审查中）
实现提交 ded0dca：已知 OpenAI Images 协议模板的 JSON 生成/有序 multipart 编辑、受限参数 schema、受管遮罩、整数报价和冻结输入、图像结果持久化及安全收集。生成 HTTP 新请求/重放均为 202 {task_id}，人工计费门禁仍关闭，最终 Delivery/界面由 M4.4–M4.5 接续。
实现者最终全量 Go 测试及 vet 通过；控制端在该提交重建纯 Go gateway 并实际启动验证健康、Origin、端口冲突、重启、关闭及父进程 EOF，证据 .runtime/smoke-7076129473e74c3bbffea5f312324894。独立规格/质量审查尚未完成。所有图像协议调用均为本地 httptest fixture；没有真实供应商、付费生成或最终媒体投递验收。
M4.3 独立复审在 f033c6f 通过（Spec compliant / Quality Approved）。ded0dca→b5d600f 修复属性 schema 大小写、精确数值与遮罩回执窗口；f033c6f 修复普通上传日志误认遮罩，通过原意图中的服务端子 UUID 绑定归属。定向恢复/数值/归属测试、全量未缓存 Go（b5d600f）、最终 providers/server/tasks 覆盖测试与 vet 通过。旧内部格式的已完成回执可重放；未完成且缺归属元数据的意图明确拒绝，不推断归属或自动重新生成。未完成的真实调用与最终媒体投递限制保持不变。
