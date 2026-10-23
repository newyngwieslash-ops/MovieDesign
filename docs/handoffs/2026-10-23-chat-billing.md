# MovieDesign H20 聊天费用门禁验收

日期：2026-10-23，Asia/Shanghai。接续分支 `codex/h01-signed-update-runtime`，基线 `9b0c7e9`。本次仅接续已确认的H20适配及相关回归，不重做M1–M7。此前关闭取证与真实图像准备见[验收接续](2026-10-23-acceptance-followups.md)。

## 范围与当前状态

用户明确批准补齐普通聊天首轮计费门禁：既有billing卡互斥绑定task或chat message，冻结模型、调用次数及总估算；没有人工回执就不发送LLM请求。实现复用原questions、receipts、quotes和inference journal，迁移000016仅在chat_dispatch加两个支撑字段，不改旧迁移或五核心表。详细字段、续报价和预算语义见[协议](../contracts.md#h20-frozen-implementation-contract)。

13:12工程提交 `896168cb91c24b7ee20e2a3941a403ed0bb6a068`，13:14已推送，实际远端`codex/h01-signed-update-runtime`为同一SHA，main仍`f9432ae6dd485f51441d7f6202e127107ef1966e`。独立首审三项及后续限定修复已复审通过；最终全量检查、生产构建与七种桌面模式均有完整通过记录。**首次技能重启卡住的根因仍未明；原样复跑通过不能作为修复证明，可靠性项继续未决。** 下文保留中间失败和对应修复，不把真实生成或签名安装计为通过。

## 保留的验证与首审事实

- 实现者定向四包、vet、TypeScript、实际React 27项检查通过；固定opencode1.19.0与合成上游完成费用批准后普通question续接，supplier/relay各2次。真实供应商调用为0。
- 父级首次`scripts/check.ps1`退出1，`.runtime/h20-full-check.log`：重启恢复测试未通过。其`prompts != 1`断言没有实际计数，且未先确认首次提交便关闭worker，不能直接解释为unknown重发。原失败日志保留。
- 独立首审为**Spec FAIL / Quality CHANGES REQUIRED**。两项真实Relay缺口：Responses工具`output`图片数组绕过纯文本校验、`user content:null`未本地拒绝；各使合成供应商收到1个POST。第三项是上述重启测试的前置同步与诊断不足。顶层JSON null候选经实际CanonicalJSON入口核对不可达，已撤回，不算产品缺陷。
- 合成smoke适配覆盖六个实际聊天发送脚本；只在自身监听的合成供应商、冻结fixture模型、本次显式Send原requestId和message/card严格匹配后点击费用按钮。恢复、重载和原请求重放不新增批准。30个离线VM场景通过，已经注册到全量检查；它们不等于实际桌面验证。

独立报告和原始RED/GREEN证据保存在被Git忽略的`.superpowers/sdd/2026-10-22-handoff-followups/`；公开文档保留结论与边界，不包含凭据或用户素材。修复完成后在下节追加限定复审、最终测试、构建制品与实际提交结果，不覆盖首审失败。

## 最终证据

以下fix1–fix4段落按执行顺序保留当时状态；最终结论以末尾“当前源码最终结果”为准。首次独立审查FAIL、各次全量和桌面失败均未删除。

fix1仅修改共享请求校验及两个覆盖测试文件。新增44种协议载荷检查：30种畸形/多模态输入要求供应商POST为0，14种合法文本/工具请求要求1次成功；旧实现通过overlay产生实际业务RED，修复后连同原审查两例及已有Relay/unknown恢复检查退出0（`.runtime/h20-fix1-focused-green.log`，35.091s）。固定native普通question续接仍通过，supplier/relay各2次，费用回执前0次；agent vet退出0。父级已核对三文件SHA与日志，限定复审和最终全量仍在执行，尚不将其记为最终PASS。

重启回归先显式批准首条并等待实际POST=1、持久accepted状态；重启后等待实际snapshot读取，再核对POST仍1、状态unknown，随后完成只读终态调和。删去该处300ms任意等待，没有放宽请求数断言。原全量失败缺少计数，其历史精确原因仍未证实，不追认为已发现零POST或重复POST。

fix1的限定复审三项均ADDRESSED，未发现新问题；最终全量`.runtime/h20-fix1-full-check.log`退出0、生产构建`.runtime/h20-fix1-production-build.log`退出0。源码与制品对应保存在`.runtime/h20-source-artifacts.json`。这些结果仅对应fix1冻结，不覆盖后续UI修复。

首个生产聊天运行`eb45cc34c71a43b6b2aaadaeab4f4ce3`：initial通过，recovery等待“已受理”失败；已批准卡和1个合成供应商请求存在，聊天列表仍显示queued。实际调用链确认`setState`只改SQL，而ChatPanel仅由内容/终态事件刷新；原事件合同没有新增accepted事件。已安排fix2复用GET对当前未终结消息只读刷新，保持终态停止、切会话/卸载清理及零新增POST。失败JSON保留，未放宽smoke断言。该运行未进入后续其他模式；fixture窗口按准确身份正常退出303ms，无强杀，8001/4096释放。父级挂起命令经中断退出1，不将其当正常完整smoke通过。

fix2限定复审与全量`.runtime/h20-fix2-full-check.log`通过，生产构建`.runtime/h20-fix2-production-build.log`退出0。重跑`48d737b748774e22aa16bde28f3fd4e1`已显示“已受理”，随后验收脚本的全局计数断言失败：第二条未批准Send取基线0时，上一条已批准hold请求尚未到达供应商，稍后1次入站被错误归给第二条。唯一入站的最后user原文对应上一条`hold referenced`，不是`queued original`；这是fixture时序误关联，无第二条越权调用证据。该窗口正常关闭9151ms，无强杀；父级挂起命令中断退出1，原失败保留。

fix3仅调整两个验收脚本：三处用既有`waitFixture`确认上一条已批准hold请求实际开始，保持hold、队列并发、严格零增长、撤回和原请求恢复断言不变。旧实际序列三例RED、新序列与原30场景GREEN，限定复审Spec/Quality PASS。最终`scripts/check.ps1`退出0（`.runtime/h20-fix3-full-check.log`）。应用源码与fix2生产制品不变，已逐一核对exe/gateway/JS/CSS哈希；新的七模式实际桌面验证仍在执行。

| 桌面模式 | 当前最终运行状态 | 证据日志 |
| --- | --- | --- |
| chat | initial/recovery/restart PASS | `.runtime/h20-fix4-production-chat.log` |
| skills | 首次restart失败；原样完整复跑三阶段PASS，根因未明 | `.runtime/h20-fix4-production-skills.log`、`h20-fix4-production-skills-repeat.log` |
| storyboard | initial/recovery/restart PASS | `.runtime/h20-fix4-production-storyboard.log` |
| tools | initial/recovery PASS | `.runtime/h20-fix4-production-tools.log` |
| pipeline | initial/recovery/restart PASS | `.runtime/h20-fix4-production-pipeline.log` |
| video | initial/recovery/restart PASS | `.runtime/h20-fix4-production-video.log` |
| updates | initial/recovery PASS，零真实安装 | `.runtime/h20-fix4-production-updates.log` |

当前生产制品（`.runtime/h20-fix4-source-artifacts.json`）：exe SHA256 `77E61F31719DCD873834377B8551A01DC167542B3C05473F93282E42EAB4CCF7`；gateway `EECFCB5049869DE6DC51D87F710425D362F2867E52479F4DC0AB5D0B06D60A1B`；JS `1796661CF50AB6E4A60722F21911FF339C134FDE0F355E7A0FDE6FCAE62E961E`。这些是`--no-bundle`制品，不是正式签名安装包；fix2旧哈希与日志保留为历史证据。

第三次运行`fe92545d79c641618ca88f18dfe17fd9`已通过引用/标注、撤回及普通问题回答，随后拒绝问题的`waitDone`超时。只读核对固定opencode原生库与网关journal：用户拒绝会使原生本轮停止，完成快照仍带`finish:"tool-calls"`，question部件为error且有人类拒绝回执；没有第二个拒绝轮供应商POST。网关排除了所有tool-calls完成，因此错误保留accepted、占住队列。fix4正在共享快照归一化处修复，需精确匹配当前轮/parent/callID、已送达人类拒绝与原生完成，使用已有cancelled终态和清理屏障，不能把任意工具错误或idle当终态。配套fixture改为验证精确原消息cancelled和零额外POST，不主动取消来掩盖产品缺陷。该失败窗口正常退出8729ms，无强杀；原命令中断退出1、证据保留。此后各模式尚未执行。

### 当前源码最终结果（13:12）

fix4在共享快照路径识别精确当前轮、parent/message/callID、已送达人类拒绝和原生完成证据，进入原有cancelled与清理屏障。14种精确/越界证据场景、实际固定native拒绝、恢复与清理检查通过；限定独立复审Spec PASS / Quality PASS。native单测未配置完整MCP，只证明真实WaitIdle及turn所有权；最终生产chat三阶段补证实际MCP断开、epoch收尾、拒绝后续聊和重启原请求不再推理。fixture只验证精确拒绝消息和零额外POST，没有主动cancel掩盖异常。

- 最终`scripts/check.ps1`实际退出0：`.runtime/h20-fix4-full-check.log`。覆盖Go全包/vet、React 29项行为检查、37个smoke门禁场景所在测试文件、编辑器/画布/任务/流水线检查、TypeScript/Vite、Rust、M1进程/Origin、合成SQL血缘和发布dry-run。Rust两组分别12/17通过，各保留1个既有忽略。Vite约947.71kB包警告仍保留，未做无测量拆包。
- 最终`pnpm --dir desktop desktop:build`实际退出0：`.runtime/h20-fix4-production-build.log`。冻结源码与制品一一核对，回归期间没有改源码或放宽超时。
- 上表七种模式各有完整通过记录：chat `48cd2e122b264c65a42713dfa2d6fe3a`；skills复跑 `ac4e6e16f5164929b9c88026f78aad0c`；storyboard `aec735307b6046f6be754cd751ebd707`；tools `513c6156dca74b04bfc80fa3978eade8`；pipeline `31ccb92c95a74269b30e46daf2a4314b`；video `a049a4f80b9941a4a8b296bd0ae27581`；updates `7b0d21ad0f1a4daf8b85e59b79932618`，均位于`.runtime/desktop-release-<ID>/`。这些使用真实Tauri/WebView/opencode和隔离数据，供应商为受控合成HTTP服务。
- 最终19次正常关闭为1101–1598ms，保留原10秒等待和进程/端口断言，无强杀。13:08清理时8001/4096及自有进程为空，WinNAT Running、未改服务；`.runtime/h20-fix4-final-cleanup.json`。这是随后正常用户应用重开之前的隔离收尾快照。
- 已审30文件按精确清单提交为`896168c`并已推送，`git ls-remote`核对实际远端同SHA；main未动。先前工作区统计/换行标记保留，不批量重置或暂存。本轮文档以独立提交随后交付。

**未决可靠性项：** 首次skills运行`a65923ea9a1e4f18b999482437e64ab4` initial/recovery通过，但restart第四条已批准消息持续queued、供应商0请求，没有新turn/journal；前三turn已关闭、冻结消息/原回执/报价/人工批准相符。失败时未保存完整agent状态，不能确定是host未就绪还是其他调度问题。窗口按精确身份正常退出213ms；原失败、外层中断退出1均保留。

同配置事后纯GET探针恢复就绪；原样新隔离数据完整复跑三阶段通过，连续安全状态采样保存在`.runtime/h20-skills-repeat-status.jsonl`。**没有源码修改、没有新增等待或延长超时，所以不能说该问题已修复。** 独立限定分诊`h20-runtime-triage.md`认为现有证据不足以要求具体代码补丁、不阻断已审代码提交，但阻断把异常关闭为已修复。若再现，保留原requestId和同一时刻dispatch/turn/epoch/host/工具/worker证据，不自动重发或重新批准。此前另一单次关闭超时仍原因未知。两项均保留，不作无条件稳定性或发布通过声明。

## 真实验收边界

H08–H12真实LLM、图像/标注编辑、15秒口播配乐视频、供应商取消/重试费用及真实生成项目血缘仍为**UNEXECUTED**。已保存连接不代表可用推理；早期检查未发现7863/18317监听。用户指出服务应正常后，12:31在真实宿主范围重新检查：18317的TCP与无认证首页GET已通、HTTP200，纠正此前图像缺服务的判断；7863仍无监听且连接被拒绝。未读取响应正文、密钥或调用模型；图像API真实生成尚待费用卡。未保存视频配置。需用户在应用内核对实际连接、能力与报价，并本人通过费用卡批准。

11:19权威图像任务仍等待billing、费用卡过期；11:21原应用正常退出1319ms，无强杀，无供应商请求。13:10以最新构建重开正常用户应用，PID32980；只读查询原task `f067dbaf…`仍waiting_user/billing、原卡`da9d5f25…`expired，未自动续价或批准。新启动证据位于`.runtime/h20-live-acceptance-20261023_1310/`，受管exec12844；原启动记录不覆盖。已明确提示用户在应用内本人续报价并批准/拒绝，密钥不经聊天。

旧卡不能被静默换配置或自动重试。正式签名材料配对、签名Release、干净Windows账户安装升级，以及物理DPI/IME验收仍为**UNEXECUTED**；不以dry-run、公开签名向量、合成媒体或隔离窗口代替。
