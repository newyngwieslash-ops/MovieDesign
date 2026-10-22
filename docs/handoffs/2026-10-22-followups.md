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
| H07 | 校正历史“全部完成”表述、五核心表术语、重试合同、MCP 公共名称与 opencode 内部前缀区别 | 随最终差异审查 | `9505c6a`，补充待提交 |
| H15a | 权威任务状态控制原批准恢复；保留历史审计、未知请求与项目隔离；中性文案；修复首个在途 GET 吞掉更新事件的竞态，连续事件/晚到旧快照回归通过 | 首审 1 项 Important 已在限定复审关闭；Spec compliant / Approved；生产原生验证另记 | `18a8710`、`9bc10b9` |
| H15b | 安全前缀持久化后转发；原生空快照用可信日志补全；恢复文本身份统一；解码凭据检测覆盖特殊字符及大数值；新增支撑迁移 000015，旧迁移未改 | 首审两项 Important 已在两轮限定复审全部关闭；Spec compliant / Approved；合成上游不等于真实模型 | `a5fdaa1`、`da2599d`、`3cc5e0f` |

H05 的 RED 在实际公开服务上复现网络/读取失败三次请求、超时/取消额外退避；修复为共享错误分类的三个布尔值。保留请求超时 3 秒、最多三次、250/500ms 退避、24h 缓存与 LKG。原产品会重试网络错误，本实现按已确认计划有意收窄，不声称该限制来自原产品。

H06 首审额外 fixture 复现：身份校验后、CloseMainWindow 前主进程自然退出，错误返回 1。修复只在已固定的进程操作边界接受“确认已退出”，不吞掉仍存活进程的权限错误或 CIM 查询失败。新增回归覆盖 close/wait/kill 三个窗口、句柄释放和后续模式执行。

## 当前边界

- H06 的 17 组检查使用过程/进程 fixture；真实 CIM 查询在受限环境遭拒。没有实际停止或启动用户进程，没有把测试中的 `Release launched` 当作 gateway/窗口就绪证明。
- H01 的本地签名测试向量只用于验证插件行为，不能证明生产签名材料可用。正式签名、GitHub Release 安装包发布与干净 Windows 账户安装升级：**UNEXECUTED**。
- 真实 LLM、文生图、标注修复、15 秒口播配乐视频、真实账单与生成取消/重试：**UNEXECUTED**。需要应用内可用连接及人类费用卡批准，未静默替换成 fixture 或自行付费。
- H01–H06、H15a/H15b 工程修复已通过各自独立审查；正式签名与实际模型等外部验收并未因此通过。H08–H14 与真实首 token 验收继续按计划推进。最终源码 `3cc5e0f` 的整仓检查已通过，H16 整分支总审查和当前生产构建/运行另行记录；历史绿灯不代替当前验证。
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
