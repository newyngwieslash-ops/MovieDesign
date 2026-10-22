请接手继续开发 MovieDesign，并完成 PRD 剩余实现、真实验收和正式发布。请实际推进工作，不仅给建议或重复写计划。

工作目录：E:/AI_Rea_Things/MovieDesign
原应用与恢复源码：E:/AI_Rea_Things/MinimaxDesignThings1015，只读，禁止修改或复用账号/用户数据。
GitHub：https://github.com/newyngwieslash-ops/MovieDesign

第一步请完整阅读：
E:/AI_Rea_Things/MovieDesign/docs/handoffs/2026-10-20_200842+0800/handoff.md

然后依次读取 docs/spec.md、docs/contracts.md 对应章节、docs/superpowers/plans/2026-10-15-moviedesign-complete.md 的 Global Constraints、M7 分计划、docs/acceptance.md、docs/releasing.md。原产品细节查 analysis-docs，按01、02、04、05、06/07顺序。交接列出的实际源码证据优先于文档中过度概括的“全部完成”。

交接快照是 main@f9432ae6dd485f51441d7f6202e127107ef1966e，当时本地、origin/main 和实际远端 main 一致。先核实当前状态；如果之后已有新提交，保留它们并增量接续。交接时32个tracked文件有修改标记但 git diff / staged diff 均为空，快照在同目录 repository-state.json。禁止 reset、clean、批量覆盖/暂存这些既有状态。新功能/修复分支使用 codex/ 前缀。

M1–M7已有工程提交及历史离线/桌面审查，不要从M4.2、M6.3或空仓库重做。旧.superpowers/sdd/progress.md停留M6.5的尾部已过时。真实LLM、图像、视频生成和正式签名安装升级仍未执行。历史WebView检查运行了真实Tauri/opencode，但上游是本地协议fixture，不能把它写成真实供应商验收。

请按handoff中H01–H16清单接续，先做以下离线可完成的修复：
1. H01：真实更新链路未接线。Rust updater_check_and_gate目前只返回策略，configure_runtime_updater只创建builder，验签函数主要被单测调用，前端安装按钮无onClick。接通插件manifest检查、下载、验签、安装与进度；执行前复核manual_only/策略/活动状态。坏签名、版本错配和不可信仓库必须拒绝。
2. H02/H03：统一原生实际版本与发布Tag/manifest/二进制版本；修硬编码0.1.0和数字prerelease比较；对齐CI pnpm10与packageManager12.8.1；校验正式签名公钥、私钥输入、版本和signature产物协议。当前配置公钥与公开测试向量相同，不能假定生产签名材料已就绪。
3. H04：视频时长当前固定±500ms，与计划按编码时间基和一帧核验不一致。读取实际帧率/时间基，落实技术检查，保留整条生成与人工主观质量验收。
4. H05：更新策略当前还重试network/timeout，而已确认计划限定5xx-only，查实证与契约后统一代码/测试/文档，不自行改变合同。
5. H06：修restart.bat无条件git checkout覆盖media_prompt.go、目录前缀进程误匹配及错误成功提示。限制自有进程，优先正常关闭，保留真实源码修改。
6. H07：同步修正文档和计划勾选，区分已有单元/模拟验证与实际安装升级；保留历史审查事实。五核心表包括composition_members而非projects；导出UTF-8已在4ec6b98修过，不重派。

之后按H08–H13完成真实LLM双技能、文生图/标注修复、一句话15秒口播配乐视频、真实取消/失败重试/分镜变更门禁、真实项目SQL完整血缘/备份恢复，以及正式签名Release和干净Windows账户安装升级。H14/H15是硬件/IME/DPI、历史恢复提示、媒体文案及真实首token/流式体验边界。最后H16做当前差异的独立审查、全量验证、提交推送和四阶验收表收口。

已确认事项不用再问：
- MovieDesign使用Tauri2、React19/TS/Vite、Go1.25.8、modernc SQLite、官方MCP SDK1.4.1、opencode1.19.0；不降级/重选栈。
- BYOK和费用确认，不建钱包或独立账号服务；支持本地/云端自定义连接，当前模板openai-images、ark-video，LLM三种已有协议，不是任意脚本/插件框架。
- 前端永远只访问gateway；HTTP/MCP共享业务服务；Origin精确校验含OPTIONS/MCP，MCP额外私有令牌；原生文件/命令工具禁用，18 design工具与允许的question，每轮40次上限。
- 所有授权用结构化人类确认卡；已有回执按绑定版本复用，不能用聊天同意代替，agent不能批准自己。
- 写操作requestId；unknown原输入原键恢复，禁止自动重新生成/换模型；区分billing等待与普通waiting_user，unknown不是终态。
- Prompt展示/存储/提交字节一致；分镜变更新revision使下游批准失效；默认整条重生成；独立内容独立Resource，同内容修订才新Version。
- 五核心表与已提交迁移不可改写，当前到000014；新增支撑另编号；事务内只用传入Tx，禁止网络/文件/重入DB getter。
- Windows x64首发；GitHub Releases签名更新、manual_only/24h/LKG，密钥不进Git/日志/聊天；本地取消不等于停止供应商计费。
- 不建设浏览器、插件/技能市场、协作、热更新、第三家隐式TTS/音乐模型或视频片段拼接。

应用Ponytail和现有AGENTS约束：先理解实际调用链，复用现有代码/标准库/已安装依赖，修共享根因，避免无请求抽象/依赖/重构。按任务需要使用systematic-debugging、writing-plans、subagent-driven-development、requesting-code-review和verification-before-completion；先读适用技能。若采用子代理，分配清楚文件责任，说明共享工作区且不能撤回其他人的修改。

开始先核实Git并给一个简短执行顺序，然后立即从H01复现并实施。每项非平凡修复留下能实际失败的最小回归检查；迭代跑定向检查，在交付关口跑scripts/check.ps1、当前生产构建和适用的真实WebView smoke。没有代码变化或明确新风险，不重复绿灯套件。独立审查新差异，修完Critical/Important后提交并推送到上述仓库，保留已授权的阶段交付流程，无需重复请求确认。

缺少真实端点/能力、凭据、正式签名材料或安装环境时，继续其余不依赖项；只针对真正缺少的信息询问。凭据让用户在应用/安全环境配置，不要要求贴到聊天。真实付费调用仍须应用费用卡逐次批准，绝不静默回退fixture或自行付费试探。无法完成的真实验收准确记UNEXECUTED，不宣称完整交付。当前无凭据并不阻塞H01–H07等工程修复。

请用中文简洁同步进展，报告改动、验证、提交和未执行边界。更新handoff/acceptance作为证据，不把测试数量、旧审查结论或dry-run当作真实生成/正式发布。
