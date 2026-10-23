# H17 Port Diagnosis and Desktop Recovery Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox syntax for tracking.

**Goal:** 准确区分 4096 绑定失败原因，在不改变既有端口/安全契约的前提下完成可执行的桌面重启恢复验收。

**Architecture:** Rust 仍预留并启动自有 opencode；固定非敏感启动错误写入已有私有 error.txt，由 Go Status 映射为用户说明。失败状态继续禁止探测未知监听者。系统 WinNAT 操作只在取得新的明确授权后执行，始终恢复操作前服务状态。

**Tech Stack:** 现有 Rust std::io、Go、固定 Tauri/opencode 与已有测试；不新增依赖、RPC 或业务 API。

## Global Constraints

- “前端永远不直接访问 opencode”，“所有写操作带 requestId”，“禁止自动重试”。
- “所有批准来自结构化卡片；agent 不得代点计费/方案/分镜批准。”不执行真实供应商/安装；仍 UNEXECUTED。
- gateway 127.0.0.1:8001；opencode 127.0.0.1:4096；不更换生产端口。
- 原应用目录只读，保留既有工作区状态；不 reset/clean/批量暂存。
- 不终止无关进程；保持私有凭据隔离、恰好三个 stdio JSON-RPC 方法。
- 已审查代码不重做。新基线 f8b19ffc19d0959bb627090bec32d1bcfa27acf3；父级独占 Git/docs。

## Task 1: Preserve and display startup bind failure categories

**Files/ownership:** implementer only `desktop/src-tauri/src/opencode.rs`, `gateway/internal/agent/host.go`, `gateway/internal/agent/host_test.go`; reuse existing inline Rust tests. Parent owns docs, system diagnostics and Git.

**Interfaces:** `claim_port(u16) -> Result<TcpListener,String>` stays. Existing `port_conflict` means AddrInUse only; add private fixed `port_permission_denied` for PermissionDenied and `port_bind_failed` for other bind failures. `start` preserves those exact fixed markers, never raw sensitive error/config. `Status` maps them to accurate Chinese messages and always returns before any readiness HTTP probe. Existing unknown/start_failed/exited handling remains.

- [x] RED: three Rust error kinds and Go six-marker table fail before correction. Actual occupied listener retained. Additional real NewHost/worker/client-copy regression catches pending and post-failure probes; see implementer report for genuine assertion RED and prior invalid fixture setup distinction.
- [x] Implement minimum shared mapping. After first review, shared native client transport requires completed handoff/no error/exact private started evidence; no protocol, port or dependency change. Permission text describes denied bind/possible OS reservation without identifying an owner.
- [x] GREEN: Rust lib12/integration17 pass (existing fixture ignored per target); full agent package63.384s and vet pass. SHA256 frozen and independently checked. No physical recovery inferred.
- [x] First commit b992eee; one Important from independent review fixed in e0400aa. Scoped original-reviewer re-review Spec PASS / Quality PASS, no findings. Current complete scripts/check.ps1 and production build exit0.

## Task 2: Actual system/desktop verification and delivery

**Parent ownership:** `.runtime/h17-port-diagnosis/`, existing smoke scripts unchanged, docs/acceptance.md, docs/handoffs/2026-10-22-followups.md and this plan.

- [x] Reproduce before state: no 4096 listener; IPv4 and IPv6 active exclusion 4042–4141; bind4096 and4095 return10013;4142 binds. DynamicTCP1024/13977, WinNAT/HNS running, Docker backend active. Save before.json; do not infer which process originally changed the range.
- [x] 2026-10-23 用户明确允许短暂停止 WinNAT 并立即恢复；当前先重新检查环境，4096 已能绑定、保留范围移至7179以上，因此无需停止服务或发起UAC。旧待答复记录保留为历史事实；未进行系统网络/服务修改。
- [x] 授权后先复查端口：当前4096能绑定，无需执行服务stop/start；WinNAT始终Running，未改Docker/WSL/HNS/动态端口/保留项/防火墙。受控系统中断脚本未执行，不将此写成“已通过停服务修复端口”。
- [x] 2026-10-23原标准updates退出0（initial/recovery），chat等待空body修正后退出0（initial/recovery/restart）；真实生产进程、隔离数据、本地合成上游，实际安装/真实模型调用0。准确phase与清理证据见当天恢复记录。
- [x] 当前完整updates/chat恢复通过，0c05dd5最终全量回归退出0，独立审查Spec PASS / Quality PASS；四阶验收及源码/生产制品对应另存10月23日记录，未解释首次退出超时和外部UNEXECUTED仍保留。提交/远端SHA由交付终检记录，不强推/不合入main。

Official context: [Microsoft WSAEACCES 10013](https://learn.microsoft.com/en-us/troubleshoot/windows-server/networking/error-10013-wsaeacces-is-returned), [TCP ephemeral range](https://learn.microsoft.com/en-us/troubleshoot/windows-client/networking/tcp-ip-port-exhaustion-troubleshooting). These support the diagnostic distinction; they do not prove which component reserved this host's range.

### H18 actual lifecycle follow-up (2026-10-23)

- [x] 核实19826c6源码无规范化差异、原生产制品哈希吻合；当前4096可绑定、WinNAT保持Running。记录 `.runtime/h17-recovery-20261023/before.json`。
- [x] 原标准生产updates冒烟复现新实际失败：initial安全检查通过，但Stop-OwnedDesktop等待10秒后主程序未正常退出，完整恢复未到达；记录 `.runtime/h17-recovery-20261023/updates.log` 与隔离运行27dbc1e8f3e7433e8026df8c5d5c0147。finally清理后无自有进程/监听残留。
- [x] 已追踪关闭链与同10秒断言的带时间实际复现：初始/恢复关闭1829ms/3310ms通过；后续原标准updates正常退出通过。未建立首次超时确定根因，没有猜测性应用修复或放宽超时；原失败留存为间歇不确定项。若复发继续采集原生事件/持有句柄时序，不宣称根治。
- [x] `0c05dd5`仅修正smoke-chat的Page.reload空body等待并补实际表达式RED/GREEN。独立审查Spec PASS / Quality PASS，当前标准updates完整恢复与chat三阶段通过，最终scripts/check.ps1退出0；生产二进制/原生命周期/原断言哈希未变，未重复构建同一制品。真实供应商/正式签名安装仍UNEXECUTED。

## H17 independent review fix round 1

Read h17-port-review.md complete Important finding, including initial pending-start window. Requirements: shared native readiness/request path used by configured Chat.run must not contact any listener while the private encrypted launch handoff is still pending or after any recorded launch failure (including unknown marker/storage uncertainty). Preserve NewHost's private launch directory provenance; no public protocol/RPC/production-port change. Review the ordering of pending/failure checks against native start error-write and launch.dpapi consumption, to avoid a successful absence check racing failure transition. All directly relevant native Host callers must share the corrected boundary; no caller-by-caller guards or silently skipping this because it was pre-existing. Prefer existing stdlib/private handoff and Host client patterns, no new dependency/framework. Report proposed scope/design if files beyond original three are necessary. Parent authorizes only minimal necessary existing client/control/event/test path expansion if shared causal fix cannot fit Host; explain exact reason before editing. Regression must show controlled unrelated HTTP counter zero from actual Ready and non-nil-Relay background worker under pending and six failed markers, plus successful private native readiness still possible after successful handoff. Preserve real RED and run covering checks/agent package+vet because configured background flow changed; preserve 10s/40calls/unknown original identity and native permission gates. Append fix commands/results/hashes to the same h17-port-recovery-report.md, freeze READY. Parent finalcheck/build waits fixed source; base b992eee fullcheck PASS is historical only. No OS action or real provider call.

Parent design resolution: use shared private Host client transport gate so read/request/control/SSE and copied clients share it. Never retain launch.dpapi on failure: original consumed-and-removed exchange contract remains. A minimal nonsecret private success marker created only by Rust after its own child spawn succeeds is allowed as launch-state evidence, without adding RPC/public API. Missing/error/uncertain success evidence blocks contact; creating it failing means startup failure, never Host authority. Preserve original cleanup/owned Child handles. Explain exact marker/ordering in report so parent can document it. Do not implement encrypted-bootstrap retention as a fallback.
