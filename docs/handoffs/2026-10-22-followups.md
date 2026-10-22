# MovieDesign 接续记录（2026-10-22，Asia/Shanghai）

基线：`f9432ae6dd485f51441d7f6202e127107ef1966e`；分支：`codex/h01-signed-update-runtime`。遵循 [交接要求](2026-10-20_200842+0800/next-session-prompt.md) 与 [H01–H16 计划](../superpowers/plans/2026-10-22-handoff-followups.md)。保留交接前工作区修改，原应用目录只读，未重做 M1–M7。

## 已实施与验证

| 项目 | 改动与结果 | 独立审查 | 提交 |
| --- | --- | --- | --- |
| H05 | 共享策略拉取仅对 HTTP 5xx 重试；网络、超时、读取失败不重试。公开 Evaluate 十例覆盖请求次数、退避和 25h LKG；定向 RED→GREEN、updates 全包测试、vet 通过 | Spec compliant / Approved，无遗留发现 | `e60b89b` |
| H06 | BAT 改为带引号的 PowerShell 文件入口；精确自有路径与 PID/创建时间校验；优先正常关闭；保留源码、非零错误；新增 17 组可执行 fixture 检查通过 | 首审自然退出竞态已修复；限定复审 I1 ADDRESSED / Approved，无新增发现 | `63c16f8`、`bc92da8` |
| H01 | 真实 updater 执行链、应用级确认卡、排他安装准入正在接续；未宣称完成 | 待实施者 READY 后独立审查 | 未提交 |
| H07 | 校正历史“全部完成”表述、五核心表术语、重试合同、MCP 公共名称与 opencode 内部前缀区别 | 随最终差异审查 | 未提交 |

H05 的 RED 在实际公开服务上复现网络/读取失败三次请求、超时/取消额外退避；修复为共享错误分类的三个布尔值。保留请求超时 3 秒、最多三次、250/500ms 退避、24h 缓存与 LKG。原产品会重试网络错误，本实现按已确认计划有意收窄，不声称该限制来自原产品。

H06 首审额外 fixture 复现：身份校验后、CloseMainWindow 前主进程自然退出，错误返回 1。修复只在已固定的进程操作边界接受“确认已退出”，不吞掉仍存活进程的权限错误或 CIM 查询失败。新增回归覆盖 close/wait/kill 三个窗口、句柄释放和后续模式执行。

## 当前边界

- H06 的 17 组检查使用过程/进程 fixture；真实 CIM 查询在受限环境遭拒。没有实际停止或启动用户进程，没有把测试中的 `Release launched` 当作 gateway/窗口就绪证明。
- H01 的本地签名测试向量只用于验证插件行为，不能证明生产签名材料可用。正式签名、GitHub Release 安装包发布与干净 Windows 账户安装升级：**UNEXECUTED**。
- 真实 LLM、文生图、标注修复、15 秒口播配乐视频、真实账单与生成取消/重试：**UNEXECUTED**。需要应用内可用连接及人类费用卡批准，未静默替换成 fixture 或自行付费。
- H02/H03 版本和发布准备、H04 帧/时间基时长核验、H08–H15 的其余验收继续按计划推进。当前整分支全量回归、当前生产窗口和 H16 总体独立审查尚未完成；历史绿灯不代替当前验证。

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

H05/H06 三个提交已推送到 `origin/codex/h01-signed-update-runtime`（远端分支至 `bc92da8`）；`main` 保持原基线，不含工作区中未完成的 H01 改动。
