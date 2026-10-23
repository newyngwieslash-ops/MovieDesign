# MovieDesign 真实验收接续

> 13:12接续结论：H20已提交`896168c`，通过限定独立复审、当前全量/构建，七种桌面模式各有完整通过记录；首次skills重启排队超时原因未明，原样复跑通过不作为已修复。最新正常用户应用13:10已重开，图像原卡仍过期、无批准；图像18317当前可连，LLM7863最近复查仍拒绝。真实生成/签名安装仍UNEXECUTED。下文保留H19与H20首审当时事实，当前详细证据见[H20验收](2026-10-23-chat-billing.md)。

时间：2026-10-23，Asia/Shanghai。接续基线 `eb4f2c3`，分支 `codex/h01-signed-update-runtime`；不重做 M1–M7。此前恢复验收见 [桌面恢复记录](2026-10-23-recovery.md)。

## 默认用户目录的只读核对（10:05）

通过 `node:sqlite` 的 `readOnly:true` 打开 MovieDesign 自己的默认数据库，仅查询指定的非秘密字段与数量。未读取密钥存储、完整配置或其他应用的数据；未发送供应商请求。

- 已保存一个 `openai-compatible` LLM 配置，有已保存凭据及声明价格。
- 已保存一个启用的 `openai-images` 图像配置，有已保存凭据及声明价格。图像编辑能力尚未声明，`reference_roles` 未设置；不能擅自启用编辑或认定远端支持蒙版。
- 未保存视频供应商配置；项目、任务、Version 数量均为 0。
- 证据：`.runtime/h19-acceptance-20261023/default-profile-readiness.json`。配置存在不证明远程模型可达、权限可用、价格准确或生成已通过。

因此不重复索取已有 LLM/图像密钥，也不将隔离 smoke 配置当作用户连接。密钥继续仅在应用设置中填写，不进入聊天、Git 或验收日志。

## H08 首轮聊天授权缺口

独立只读审查已追踪 `ChatPanel → Chat.Send → Chat.run → Turns.Begin → Relay.admit → supplier POST`：现有普通聊天首轮没有应用确认卡前置。原生 `question` 只能在首轮推理后产生；其普通 choice 不能替代生成 task 的 billing 批准。

这是相对本次用户“真实调用必须经过应用确认卡”要求的新验收前置缺口，不改写历史 M5 已实现范围。当前没有发送真实聊天。已有 `text:configured-llm` 经任务费用卡批准后可验证有限真实 LLM 文档生成，但禁用工具，不能冒充双技能、mention 或聊天工具验收；桌面现有生成表单仅支持图像。

用户随后已明确批准扩展现有 `/api/question` 的 billing：可互斥绑定 `chat_message_id` 或 `task_id`，并绑定冻结模型、有限调用次数及总估算。H20 已安排单一实现 worker；不把普通 choice 卡转换为计费授权，待服务端门禁/实际确认卡实现并独立审查后才进行真实聊天。H08 真实推理仍为 **UNEXECUTED**。

审查证据：`.superpowers/sdd/2026-10-22-handoff-followups/h08-authorization-audit.md`；仅静态审查，未运行模型或桌面。

## 剩余真实验收

| 任务 | 下一项实际操作 | 当前真实验收 |
| --- | --- | --- |
| H08 | 先解决首轮聊天确认卡前置，再由用户在应用内批准真实双技能流程；不同协议分别取证 | UNEXECUTED |
| H09 | 用已有图像配置准备单次真实生成费用卡，用户本人批准后核对结果；编辑前由用户确认远端编辑/蒙版能力并配置 `image,mask` | UNEXECUTED |
| H10 | 在应用内配置实际支持 15 秒及口播/音乐要求的视频连接，按意图、脚本、分镜、Prompt、Preflight、费用卡顺序生成整条视频 | UNEXECUTED |
| H11 | 在实际任务和人工批准范围内验证取消、失败后显式重试、分镜变更重新批准；核对真实账单，不把本地取消当退款 | UNEXECUTED |
| H12 | 从真实生成项目导出完整脱敏血缘，并在安全空闲/关闭后备份恢复；保留 DPAPI 的 Windows 账户边界 | UNEXECUTED |
| H13 | 匹配正式公私钥后，在干净 Windows 账户验证签名包首次安装和更新，才发布正式 Release | UNEXECUTED |
| H14 | 用户在物理设备上验证跨屏 DPI、中文 IME 候选/Delete、焦点与键盘操作 | UNEXECUTED |

本轮进程未提供 `TAURI_SIGNING_PRIVATE_KEY` 或其路径，不据此推断机器其他位置没有签名材料。未执行正式签名、安装、升级或发布；已有受控 sink、测试签名和 dry-run 仍只证明各自工程范围。

## H19 最终工程交付（10:36）

代码 `80b3a23` 仅修改关闭取证脚本和注册回归。实际应用生命周期、Rust/Go 源码及生产 exe/gateway/JS/CSS 哈希均保持 H17 的已审查制品；没有为同一制品重复构建。

- 每次启动独立原生 stderr、关闭请求/返回/原10秒等待耗时、首次等待后的已落盘字节边界、超时准确自有子进程快照；采集收尾最多500ms，不等后代管道EOF。
- 自检实际执行从生产脚本 AST 加载的函数。保留了原超时/缺证据的 RED、最终全量暴露的 CLIXML fixture RED、释放次级错误覆盖原错误的 RED；fix1保持精确字节断言并保留原超时异常，覆盖 reader/sink 两故障分支。
- 独立首审两项 Important，经原审查员限定复审 I1/I2 均 ADDRESSED，Spec PASS / Quality PASS。首审 FAIL 历史不覆盖。
- 最终 `scripts/check.ps1` 退出0：`.runtime/h19-fix1-full-check.log`。最终生产 updates 退出0：`.runtime/h19-fix1-production-updates.log`，运行 `a271f553013f4427abc5f12ea05d1681` 的 initial/recovery 均通过；正常退出1293/1347ms，各stderr83bytes且capture完整。
- 10:36清理证据 `.runtime/h19-acceptance-20261023/final-cleanup.json`：隔离测试自有进程和8001/4096监听均为空、WinNAT Running、serviceChanged:false。同目录 `source-artifacts.json` 保存最终源码与制品哈希。

**原应用首次关闭超时本轮未复现，根因仍未知。** 当前修复的是取证缺口及其回归，不声称应用关闭根因已修复。真实签名安装调用为0。

## H09 真实应用费用卡准备（10:39）

上述隔离回归收尾后，实际打开默认 MovieDesign 用户目录，新建「H09 真实图像验收 2026-10-23」并通过真实图像表单准备单次狐狸参考图生成。未使用本地模型 fixture，也未改已有供应商或价格。浏览器调试仅连接本应用环回端口，准备脚本关闭网络/存储事件收集，不记录密钥或请求正文。

10:39快照显示任务 `f067dbaf…` 为 `waiting_user` / attempt `billing` / `awaiting_approval`，卡片 pending；按已有 Local-image/1020 声明报价显示 `0.01 MDY`、估算1积分，非已核实账单。脚本没有批准费用，JS异常0；已提示用户本人在应用卡上批准/拒绝，过期后可显式续报价。证据为同目录 `image-card.json`/`image-card.png`。这只完成真实任务准备，不能记作生成通过。

10:53只读查询权威任务仍为 `waiting_user/billing/awaiting_approval`，费用卡已 `expired`，没有生成验收通过记录；证据 `image-status-latest.json`。不会自动续报价、批准或重试。用户可以在原卡点击“继续并重新报价”，核对后本人批准。

真实应用保留打开供用户操作，因此10:36的“隔离进程/端口为空”不是此后当前状态。应用PID51760，受管exec会话48059，启动记录为 `real-app-launch.json`；不得为了下一轮构建测试强杀正在使用的真实任务。后续先核对权威任务/人工回执，安全收尾后再关闭或重启；不自动重试或代点批准。

11:00仅核对已保存端点的 origin 和本机监听：LLM 为 `http://127.0.0.1:7863`，图像为 `http://localhost:18317`，两个端口当时均无监听。已明确提示用户在应用内启动或更正连接并核对模型/报价，未索取密钥或试探真实生成。旧图像任务冻结了旧连接，连接修正后须取消未批准的原意图、显式准备新卡；不能把旧任务或 unknown 自动换模型重试。

11:19再次只读核对原图像任务仍为 `waiting_user/billing`、原卡 `expired`，证据 `pre-build-real-status.json`。11:21按启动记录核对 PID、路径及启动时刻后请求正常关闭，1319ms退出、受管exec退出0，8001/4096无监听；无强杀、无费用批准或供应商请求，证据 `pre-build-close.json`。应用关闭用于构建/隔离回归，不表示真实生成通过。

12:31用户指出两连接应正常，父级在真实宿主可见范围重新检查，排除仅依赖沙箱监听枚举：图像18317当前有监听，IPv4/IPv6 TCP可连，`GET http://localhost:18317/`首页返回200；此前图像“无监听”不再代表当前状态。LLM7863仍未发现监听，`GET http://127.0.0.1:7863/`被拒绝。证据`.runtime/h20-actual-provider-port-recheck.json`/`h20-actual-provider-http-recheck.json`仅记录端口及无认证首页响应头状态，未读取密钥/响应正文或发推理请求。首页可用尚不证明图像API路径、模型能力或真实生成已验收。

## H20 首审与整合回归（进行中）

新增聊天门禁已形成冻结实现及迁移000016；Go定向四包、实际React 27项检查及固定opencode的合成普通question续接有通过记录，不能覆盖其后的失败。六个确实发送聊天的桌面smoke已适配明确报价和本次消息绑定的合成费用卡，另有30个无网络检查；实际桌面验收尚未执行。

首个父级全量检查 `.runtime/h20-full-check.log` 退出1：`TestRestartReconcilesWithoutResendingUnknownAndQueuedWithdrawal` 未通过，断言没有输出实际请求数，也未确保第一条已经发出便关闭worker；目前不能据此认定产品重复发送unknown消息。独立首审另外在真实Relay与合成供应商边界复现了Responses工具`output`图片数组、`user content:null`均产生1个POST的问题。结论为Spec FAIL / Quality CHANGES REQUIRED，已进入三项统一修复，原失败与首审历史保留。修复、限定复审、最终全量和生产桌面验证完成前，H20不记交付、不提交推送。
