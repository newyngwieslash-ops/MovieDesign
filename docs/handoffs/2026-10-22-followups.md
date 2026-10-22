# MovieDesign 接续记录（2026-10-22，Asia/Shanghai）

基线：`f9432ae6dd485f51441d7f6202e127107ef1966e`；分支：`codex/h01-signed-update-runtime`。遵循 [交接要求](2026-10-20_200842+0800/next-session-prompt.md) 与 [H01–H16 计划](../superpowers/plans/2026-10-22-handoff-followups.md)。保留交接前工作区修改，原应用目录只读，未重做 M1–M7。

## 已实施与验证

| 项目 | 改动与结果 | 独立审查 | 提交 |
| --- | --- | --- | --- |
| H05 | 共享策略拉取仅对 HTTP 5xx 重试；网络、超时、读取失败不重试。公开 Evaluate 十例覆盖请求次数、退避和 25h LKG；定向 RED→GREEN、updates 全包测试、vet 通过 | Spec compliant / Approved，无遗留发现 | `e60b89b` |
| H06 | BAT 改为带引号的 PowerShell 文件入口；精确自有路径与 PID/创建时间校验；优先正常关闭；保留源码、非零错误；新增 17 组可执行 fixture 检查通过 | 首审自然退出竞态已修复；限定复审 I1 ADDRESSED / Approved，无新增发现 | `63c16f8`、`bc92da8` |
| H01 | 已接通真实插件调用、确认卡及排他准入；修复权威原安装回执、明确缺席恢复及 gateway→agent 清理顺序；Go/Rust/实际 TSX 定向回归与生产构建通过 | 首审 4 项 Important 已在限定复审全部关闭；Spec compliant / Approved；契约时机措辞 Minor 已校正 | `d3d2f6e`、`e2e8282` |
| H02 | 共享 SemVer 语法、数字 prerelease 和半开区间，75 个公开接口表子例 RED→GREEN，包测试/vet 通过 | Spec compliant / Approved，无遗留发现 | `e111430` |
| H03 | 版本/Tag/PE 一致性、测试公钥拒绝、签名产物校验、pnpm 对齐与生成文件换行确定性；31 组检查及实际公开向量验签通过 | Spec compliant / Approved；正式材料另验 | `8273eb7` |
| H04 | 实际帧/时间基、空音轨、矛盾元数据和有界探测；技术证据幂等写 events；真实 ffprobe 回归与包测试/vet 通过 | Spec compliant / Approved，无遗留发现 | `c1ac3ae` |
| H07 | 校正历史“全部完成”表述、五核心表术语、重试合同、MCP 公共名称与 opencode 内部前缀区别；补四阶门槛与当前证据 | 随整分支及最终限定差异审查；后续仅更新实际验证记录 | `9505c6a`、`e83e591`，本文件另随最终文档提交 |
| H15a | 权威任务状态控制原批准恢复；保留历史审计、未知请求与项目隔离；中性文案；修复首个在途 GET 吞掉更新事件的竞态，连续事件/晚到旧快照回归通过 | 首审 1 项 Important 已在限定复审关闭；Spec compliant / Approved；生产原生验证另记 | `18a8710`、`9bc10b9` |
| H15b | 安全前缀持久化后转发；原生空快照用可信日志补全；恢复文本身份统一；解码凭据检测覆盖特殊字符及大数值；新增支撑迁移 000015，旧迁移未改 | 首审两项 Important 已在两轮限定复审全部关闭；Spec compliant / Approved；合成上游不等于真实模型 | `a5fdaa1`、`da2599d`、`3cc5e0f` |
| H16 最终修复 | 原生实际版本达到权威目标后，允许明确结束本地恢复提示，保留 install_started 审计；确定的纯文本帧不触发任务/确认查询取消 | 整分支首审 I1 Important、M1 Minor 均 ADDRESSED；唯一限定复审 Spec PASS / Quality Approved，无新增发现 | `1d0923b` |
| H17 端口诊断与启动隔离 | 区分占用/权限拒绝/其他绑定失败；修复配置后台在启动尚未完成或失败后仍探测原生端口；共享客户端检查私有启动证据 | 首审 1 Important 在限定复审关闭；Spec PASS / Quality PASS，无新发现；系统实际解除保留及完整桌面恢复尚未执行 | `b992eee`、`e0400aa` |

H05 的 RED 在实际公开服务上复现网络/读取失败三次请求、超时/取消额外退避；修复为共享错误分类的三个布尔值。保留请求超时 3 秒、最多三次、250/500ms 退避、24h 缓存与 LKG。原产品会重试网络错误，本实现按已确认计划有意收窄，不声称该限制来自原产品。

H06 首审额外 fixture 复现：身份校验后、CloseMainWindow 前主进程自然退出，错误返回 1。修复只在已固定的进程操作边界接受“确认已退出”，不吞掉仍存活进程的权限错误或 CIM 查询失败。新增回归覆盖 close/wait/kill 三个窗口、句柄释放和后续模式执行。

## 当前边界

- H06 的 17 组检查使用过程/进程 fixture；真实 CIM 查询在受限环境遭拒。没有实际停止或启动用户进程，没有把测试中的 `Release launched` 当作 gateway/窗口就绪证明。
- H01 的本地签名测试向量只用于验证插件行为，不能证明生产签名材料可用。正式签名、GitHub Release 安装包发布与干净 Windows 账户安装升级：**UNEXECUTED**。
- 真实 LLM、文生图、标注修复、15 秒口播配乐视频、真实账单与生成取消/重试：**UNEXECUTED**。需要应用内可用连接及人类费用卡批准，未静默替换成 fixture 或自行付费。
- H01–H07、H15/H16 及 H17 诊断/启动隔离通过独立审查；正式签名与实际模型等外部验收并未因此通过。H08–H14 与真实首 token 验收仍待执行。当前源码 `e0400aa` 的整仓检查和生产构建通过，完整生产恢复仍 BLOCKED，详见下方 22:14 记录；21:26 等章节保留为各自提交的历史检查点。
- 19:00 后本机 Windows 的保留端口范围 4042–4141 包含 4096，实际 bind 返回 WinError10013，非已有监听者。未修改系统网络配置或终止无关进程。H01 修复后的当前生产构建通过，更新初始安全 UI 检查执行后，标准冒烟在“Owned opencode process not found”失败；对应物理重启验证未执行。隔离固定 opencode 1.19.0 测试使用系统临时端口，认证 dispose 后仍存活，证明资源清理不等于进程正常退出；生产端口保持 4096。

## 回归命令与本轮证据

H05 已运行：

```powershell
go -C gateway test ./internal/updates -run 'Test.*Retry|Test.*Cache|Test.*Fallback' -v
go -C gateway test ./internal/updates
go -C gateway vet ./internal/updates
```

H06 已运行（包含审查修复后的 17 组）：

```powershell
powershell -NoProfile -File scripts/check-restart.ps1
```

本机完整实现报告与审查记录保存在 `.superpowers/sdd/h05-report.md`、`h05-review.md`、`h06-report.md`、`h06-review.md`。该目录为忽略的协调材料；本文件记录可随 Git 交付的结论与限制。后续回归和审查结果应追加在此，不改写原始交接快照。

阶段推送已用 `git ls-remote` 核实 `origin/codex/h01-signed-update-runtime` 为 `9bc10b9e7395f0eacd9ab6d404e2c6f481bd1fdc`，包括 H01–H06 与 H15a 的已审查提交；`main` 仍是原基线 `f9432ae6dd485f51441d7f6202e127107ef1966e`。该次推送不包含之后提交的 H15b 和补充文档；最终推送另行记录。

## 当前源码回归与独立审查（20:39 更新）

- H15b 首审发现恢复的合成/原生身份重复，以及 JSON 转义凭据漏检。修复分别增加真实 startup→reconcile 顺序回归、特殊字符回显及合法大数值先于凭据的回归，保留每次 RED/失败记录；两轮限定复审全部关闭，无遗留 Critical/Important。
- `scripts/check.ps1` 在最终工程源码 `3cc5e0f` 退出 0，日志 `.runtime/h16-final-check.log`：Go 全包测试与 vet、启动器/发布保护、实际 TSX 与编辑器/状态检查、TypeScript/Vite、Rust、M1 独立进程、合成项目 SQL 血缘导出、发布 dry-run。M1 证据 `.runtime/smoke-93bf7eed1f424931a96e2a8cc14cca37`；SQL 仍是合成项目，未冒充 H12 真实项目。
- 早期全量通过记录 `.runtime/h16-check.log`（`a5fdaa1`）、`.runtime/h16-fix1-check.log`（`da2599d`）仅对应各自源码。旧测试夹具在轮次尚未创建时插外键的失败保留，现用既有 `waitChat` 等待实际状态，不改生产超时。
- 最终源码的固定 opencode 合成三场景、生产构建与当前 WebView 正在执行；整分支独立总审查尚未完成。本段不预先宣称这些通过。

## 最终工程回归与独立审查（21:26 更新）

整分支审查范围 `f9432ae..e83e591`。发现一项 Important：实际运行版本已达到目标后，旧安装意图使后续检查永久禁用；一项选定 Minor：每个流式文本事件取消任务查询。`1d0923b` 同波修复后，原审查者唯一限定复审 `e83e591..1d0923b` 判定两项均 **ADDRESSED**，**Spec PASS / Quality Approved**，无新 Critical/Important/Minor。报告 `.superpowers/sdd/h16-whole-review.md`、`h16-final-fix-report.md`、`h16-final-fix-review.md`。

更新结束动作只消除当前本地提示：权威原安装回执、原生实际版本和排他操作门禁均需通过；原 `install_started` 状态和 I/D/candidate ID 保留，不推断历史安装成功、不重复安装。后续更新必须重新检查候选和人工批准。纯文本分流保留 task_changed、工具/原生 question、未知帧、重连的强制刷新，以及首个在途 GET 的新鲜度回归。另调查的 MP4 movie-clock 舍入疑点用真实 361 帧/24fps 文件未复现；不放宽 H04 技术容差。

| 当前证据 | 实际结果与范围 |
| --- | --- |
| `.runtime/h16-final-fix-{i1,m1,native}-red.log` | 保留两组件原业务失败及原生目标判断断言失败；不是用编译错误充当 RED |
| `.runtime/h16-final-fix-ui-final.log`、`h16-final-fix-native-final.log`、`h16-final-fix-types-final.log` | 34 UI 通过；Rust 15 通过/1 既有忽略；TypeScript 退出 0。12 次纯文本事件期间 task GET 1 次、取消 0 次、流中完成 1 次；其他业务事件仍刷新 |
| `.runtime/h16-delivery-check.log` | 最终工程源码 `1d0923b` 的 `scripts/check.ps1` 退出 0：Go 全包（未变包可用缓存）/vet、102 项组件恢复检查、69 项编辑器/状态检查、TS/Vite、Rust、M1、合成 SQL 血缘、发布 dry-run。M1 目录 `.runtime/smoke-a330cdc03e8a49ef9fa773f5da13e02f` |
| `.runtime/h16-current-native.log`、`h16-current-native-trace.json` | `3cc5e0f` 的当前 Go/agent 源码运行固定 1.19.0 合成原生 stream-question/disconnect/retry 三场景通过；此次最终修复未改这些源文件，未重复套件。question 合法后续步骤 supplier 2 次；断流 1 次；原生 retry relay 2 次/supplier 1 次。非真实供应商 |
| `.runtime/h16-delivery-production-build.log` | 当前 `pnpm --dir desktop desktop:build` 退出 0，release 1m11s；保留 945.78KB Vite 包警告，无未测量的拆包重构 |
| `.runtime/h16-delivery-updates-smoke.log` | 受限首轮退出 1：WebView 未启动、CIM 拒绝访问。已按精确 PID/父 PID/创建时间/可执行文件核对，只对该测试自有进程有界清理，记录使用强制兜底，不称正常退出 |
| `.runtime/h16-delivery-updates-elevated.log`、`.runtime/desktop-release-cda01c0b077540abbab7af52e834ad20/updates-initial.json` | 提升权限后初始 WebView 安全检查通过：原生 0.1.0/win32、拒绝伪造版本、manual_only 拦截、私有接口 403、仅一个明确种入的测试意图、实际安装 0 次、JS 异常 0。标准冒烟随后 `Owned opencode process not found` 退出 1；系统保留范围 4042–4141 包含生产 4096，完整重启恢复 **UNEXECUTED/BLOCKED**。initial 的种入回执不代表后续恢复已执行 |
| `.runtime/h16-delivery-owned-cleanup.json` | 两个本次隔离 profile 进程为空，8001/4096 监听为空，首轮自有 PID 已消失；未杀无关进程、未改变系统保留端口、未放宽生产 4096 契约 |
| `.runtime/h16-delivery-source-artifacts.json` | 最终工程源码、实际生成文件和生产 exe/JS/CSS SHA256 对应清单；保留此前 `.runtime/h16-final-source-artifacts.json` 作为旧提交检查点，不混用旧制品 |

### 仍需执行的完整接续项

- **H01/H16 生产恢复**：在生产 4096 可用的交互 Windows 环境重跑标准更新 WebView/restart，验证完整自有进程生命周期和原请求恢复。当前只初始安全检查通过，不宣称整体离线运行 PASS。
- **H08**：应用内安全配置真实可用 LLM，验证两个技能、真实 asset mention、队列/取消/SSE、结构化门禁及三协议的适用原生推理；真实首 token 延迟仍需测量。本地 HTTP/实际 opencode 合成测试不替代模型效果。
- **H09/H10**：经应用费用卡批准，真实文生图与标注编辑、单次整条 15 秒口播配乐视频、人工主观质量验收和同 Resource 新 Version；无隐式第三家 TTS/音乐模型或片段拼接。
- **H11**：经人工批准的真实取消、失败重试、分镜变更使旧批准失效；核对供应商调用与账单，不把本地取消当停止计费。
- **H12**：基于真实选定项目导出脱敏 SQL 全血缘，空闲/关闭后备份恢复及 DPAPI 账户边界；已有合成库导出不替代。
- **H13**：正式匹配密钥、干净 Windows runner 构建验签、签名 Release、干净账户安装/升级及数据/回执保留；当前全部 **UNEXECUTED**，公开测试向量不得发布冒充正式包。
- **H14/H15 真实体验**：物理跨屏 DPI、真实 IME 候选/Delete/焦点/可访问性、真实模型流式延迟；Vite 包大小在测得启动成本后再决定优化。

上述真实生成、实际签名/安装、真实项目恢复及硬件验证均 **UNEXECUTED**，不是本次修复的通过项。所有真实调用仍需应用内确认卡；密钥不得发到聊天或 Git。原 Oct20 交接继续保留，后续从本接续状态推进，不重做已完成的工程修复。

## H17：4096 实际排查与当前交付（22:14 更新）

本轮从 `f8b19ff` 接续，生产端口未改变。现场证据 `.runtime/h17-port-diagnosis/before.json`：8001/4096 无监听，IPv4/IPv6 活跃保留范围均有 4042–4141，4096/4095 实际 bind 返回 WinError10013，范围外 4142 可绑定；动态 TCP 范围为 1024 起、13977 个端口。WinNAT/HNS 正在运行，Docker Desktop 后台活跃。**这些证据确认系统保留造成绑定拒绝，不能证明哪个组件最初配置该范围，也不能预先保证停止 WinNAT 会释放它。** Microsoft 的 [10013 说明](https://learn.microsoft.com/en-us/troubleshoot/windows-server/networking/error-10013-wsaeacces-is-returned) 和 [动态端口文档](https://learn.microsoft.com/en-us/troubleshoot/windows-client/networking/tcp-ip-port-exhaustion-troubleshooting) 仅作诊断依据。

`b992eee` 修正所有绑定错误都显示“已占用”的问题。独立审查随后追到 `Chat.run → Host.Ready`：配置过 LLM 的后台会绕过 Status 中的失败检查，甚至在原生启动前就探测 4096。`e0400aa` 在真正的 NewHost HTTP 客户端共享边界修复：私有交接未消费、任何失败标记、启动证据缺失/损坏或存储不确定时都不发请求。控制、聊天、文本和 SSE 的 client 副本共用此检查。Rust 只在成功启动自有 Child 后发布无敏感值的 `started.txt`，最后始终删除 `launch.dpapi`。没有保留凭据作为失败兜底，没有新增 RPC/API 或备用生产端口。

独立首审 `.superpowers/sdd/h17-port-review.md` 的 1 Important，经原审查者限定复审 `.superpowers/sdd/h17-port-fix1-review.md` 关闭；**Spec PASS / Quality PASS，Critical/Important/Minor 均无遗留**。实现报告 `.superpowers/sdd/h17-port-recovery-report.md` 保存全部原始断言失败、修正后的命令/结果和三源码 SHA256；初始无效 fixture 设置失败没有冒充行为 RED。

| 证据 | 当前结果 |
| --- | --- |
| Go agent 全包/定向与 Rust 检查 | 实际 NewHost、非 nil Relay worker、Ready 和 request/control/SSE 副本，在 pending/六种失败标记/缺失或损坏成功证据/存储异常下零 HTTP 接触；成功交接后恰好两个认证就绪读取。Go agent 63.384s、vet 退出0；Rust lib12/integration17 通过，各1个既有 fixture 忽略 |
| `.runtime/h17-port-diagnosis/full-check.log` | `b992eee` 的整仓检查退出0，属于修复后台缺口之前的历史快照，不能替代最终源码结果 |
| `.runtime/h17-port-diagnosis/final-check.log` | `e0400aa` 整仓检查退出0：Go全包/vet、实际组件/编辑器、TS/Vite、Rust、M1、合成SQL血缘、发布dry-run；M1目录 `.runtime/smoke-3770933d88b04247b00a96c508c22ee3` |
| `.runtime/h17-port-diagnosis/production-build.log` | 当前 `desktop:build` 退出0，release56.45s；现有945.78KB包警告保留 |
| `.runtime/h17-port-diagnosis/updates-blocked.log`、`.runtime/desktop-release-43b3f79b5baf4ea3961e9d8b47df920e/updates-initial.json` | 当前真实Tauri/WebView初始更新安全检查通过，实际安装0次；标准冒烟仍在 `Owned opencode process not found` 退出1，完整重启恢复未执行。没有跳过失败断言来取得绿灯 |
| `.runtime/h17-port-diagnosis/native-failure-and-cleanup.json` | 实际原生失败标记为 `port_permission_denied`，`started.txt` 不存在，`launch.dpapi` 已删除；测试profile进程与8001/4096监听为空。WinNAT仍Running，系统服务未改变 |
| `.runtime/h17-port-diagnosis/source-artifacts.json` | 当前源码、生成文件和生产exe/JS/CSS的SHA256及验收范围；此前H16清单继续作为历史检查点 |

**下一步不是再写一轮端口回退代码。** 当前执行令牌不是管理员，短暂停止 WinNAT 可能影响正在运行的 Docker/WSL 网络，新的明确授权问题已经发出，尚未收到答复。准备的本机脚本 `.runtime/h17-port-diagnosis/run-bounded-winnat-verification.ps1` 要求显式授权与管理员令牌，停止前检查服务依赖与端口；只停止 WinNAT、检查4096后运行已有 updates/chat 恢复冒烟，并在 finally 恢复原运行状态。**脚本未执行、未启动UAC、未改动态端口范围/保留项/防火墙、未停止Docker/HNS/无关进程。** 若授权后4096仍不可用或恢复服务失败，记录实际错误并停止依赖项，不扩展成其他系统修改。

因此，H17 的应用诊断与防护工程已完成，**系统解除保留及完整桌面恢复仍 BLOCKED/UNEXECUTED**。待明确授权和管理员操作后，继续本计划 Task2；真实调用、真实账单、正式签名安装、真实项目备份恢复及物理IME/DPI仍按既有H08–H15清单UNEXECUTED。此结论不等于整个PRD交付完成。
