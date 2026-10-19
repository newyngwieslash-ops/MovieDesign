# M6 技能与分镜流水线 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 完成两个业务技能及语义分镜→批准→Prompt编译→Preflight→任务→版本的持久闭环，任何语义修改正确使批准失效。

**Architecture:** gateway 保存结构化分镜、不可变revision、批准回执和精确Prompt合同；Markdown是可审阅产物，不能靠解析任意自然语言猜批准。HTTP/MCP所有执行入口共享Preflight。

**Tech Stack:** 现有Go/SQLite/React；Markdown技能和知识卡；SHA-256输入指纹；现有questions/delivery/tasks服务。

## Global Constraints

继承总计划。“展示与生成必须用同一字符串”，“任何 Panel/Shot/Timeline 变更→新revision→旧批准失效→prompt_status=pending”，“默认整条重生成”。M6可用模拟视频provider检查流程，真实视频验收归M7。

### Task M6.1：技能存储、消费与两份完整业务技能

**Files:**
- Create: `agent/skills/口播视频/SKILL.md`, `agent/skills/口播视频/meta.yaml`, `agent/skills/口播视频/references/storyboard.md`, `agent/skills/口播视频/references/preflight.md`。
- Create: `agent/skills/图片标注修复/SKILL.md`, `agent/skills/图片标注修复/meta.yaml`, `agent/skills/图片标注修复/references/annotations.md`。
- Create: `gateway/internal/agent/skills.go`, `gateway/internal/agent/skills_test.go`, `gateway/internal/server/skills.go`。
- Modify: `gateway/internal/agent/config.go`, `gateway/internal/agent/chat.go`, `gateway/internal/server/server.go`, `desktop/src-tauri/tauri.conf.json`。

**Interfaces:** `GET /api/skills`列表含name/description/trigger-words/version，`GET /api/skills?name=<name>`返回正文与受管references；`SelectSkill(turn,skillID)`由服务记录当前技能版本并注入prompt前缀，不开放原生filesystem read。安装到`<dataDir>/skills/<name>`，应用资源提供初始内容。

- [x] 测试缺frontmatter、重复name、路径穿越、引用越界、未知技能、升级覆盖用户改稿、单轮技能版本漂移。安装仅填充缺失文件；用户编辑过的版本保留，新随包版本单独登记，不无提示覆盖。
- [x] 技能正文固定“适用范围/不用于/输入/确认门禁序列/产物/失败回流”；不要只是几句system prompt。口播门禁为意图→脚本→分镜→精确Prompt与费用；标注修复为引用/区域意图→编辑方案→费用→生成版本。已确认内容用回执复用，不重复问已答项。
- [x] 口播技能要求Panel时间点/景别/主体动作/转场依据，LogicalShot时间范围/叙事职责/起止状态，MasterTimeline总时长/音轨意图；不使用独立短片拼接、不用于无授权素材或不能满足时长/音频能力的配置。图片技能不用于无参考资产的纯文生图，不把框选范围描述冒充精确mask承诺。
- [x] 两技能共同规定：上游语义变更使下游批准失效；参数变化保留语义批准但生成新Prompt合同和报价；失败必须报告原问题，由用户决定重试；unknown只重放/对账；不得自动评判自身成片质量并重生。
- [ ] `go -C gateway test ./internal/agent -run TestSkill -v`，真实LLM验证能读取所选技能方法并持续遵循，提交 `feat(M6.1): install scoped creative workflow skills`。

M6.1 在b859a62通过独立规格/质量复审；全量/原生方法注入证据对应1e022a8，后续仅前端分页修正以14项组件和类型/构建检查覆盖。真实LLM方法遵循验收未执行，不将模拟调用记为真实通过。

### Task M6.2：语义分镜文档、revision 与可审阅画布节点

**Files:**
- Create: `gateway/internal/pipeline/storyboard.go`, `gateway/internal/pipeline/storyboard_test.go`, `gateway/internal/server/pipeline.go`, `desktop/src/features/pipeline/StoryboardReview.tsx`。
- Create: `gateway/migrations/000012_pipeline.up.sql`, `gateway/migrations/000012_pipeline.down.sql`。
- Modify: `gateway/internal/mcp/documents.go`, `desktop/src/features/canvas/CanvasNode.tsx`, `gateway/internal/server/server.go`, `docs/contracts.md`。

**Interfaces:** `SaveStoryboard(ctx,scope,requestId,pipelineID,expectedRevision,Storyboard)(Revision,error)`；`Storyboard{Panels,Shots,Timeline}`结构化字段对应原文层级。时间统一整数毫秒，不把UI时间字符串存为真值。document_write增加显式typed document参数并在适配表登记；通用文档仍按原入口写入。

- [x] 测试Panel引用缺失、shot区间反向/越界、timeline覆盖缺口、重复ID、时长不一致、并发编辑CAS冲突；文本中“已批准”不能创建approval。原文未要求所有Panel均匀分布，校验不得自行加艺术限制。
- [x] 先从05篇原结构固定字段、单位和必需关系，保存可机读schema到docs/contracts，不引入新的公开生成协议。`storyboard_revisions`唯一键`(pipeline_id,revision)`，保存结构化内容、semantic_hash、document_version_id、created_at；revision不可覆盖。
- [x] 每次有效Panel/Shot/Timeline改变保存新revision；无实质修改的幂等重放返回旧revision。保存新revision、置prompt_status=pending、失效下游批准/待提交合同在一个事务提交；已有运行任务保留其冻结revision，不能被后台编辑偷换输入。
- [x] 渲染确定性`storyboard_review.md`，通过document_write→delivery产生文档Version和画布文档内容；相同分镜修订沿用同Resource，项目对象身份不随文档标题改变。Markdown手工编辑作为新候选内容，必须结构化校验后才能成为有效分镜revision。
- [x] UI展示revision、批准状态、Panel→Shot→Timeline及变更；不能只放聊天摘要。`go -C gateway test ./internal/pipeline -run TestStoryboard -v`与真实WebView编辑/重启恢复通过，提交 `feat(M6.2): persist semantic storyboard revisions`。

M6.2 engineering gate approved at070886c after two bounded recovery-validation fixes. Current component20/20 and type/build pass; full/native evidence is qualified pre-frontend-fix4c3580c. True provider calls remain unexecuted.

### Task M6.3：revision 绑定批准与失效传播

**Files:**
- Create: `gateway/internal/pipeline/approval.go`, `gateway/internal/pipeline/approval_test.go`。
- Modify: `gateway/internal/questions/service.go`, `gateway/internal/server/pipeline.go`, `desktop/src/features/questions/ConfirmationCard.tsx`, `desktop/src/features/pipeline/StoryboardReview.tsx`。

**Interfaces:** `ApproveStoryboard(ctx,HumanReceipt)(Approval,error)`是questions回执后的内部调用；没有agent可调用的approve工具。Approval绑定project/pipeline/revision/semantic_hash/question_receipt_id。失效不是删除历史回执，而是该回执不能授权当前revision。

- [x] 构造revision1批准→修改Panel得revision2→提交旧卡/旧合同失败；覆盖改变Shot和Timeline、先修改后收到晚回执、双击批准、跨项目卡片、纯provider参数改变不使分镜批准失效。

```go
func ApprovalMatches(current Revision, approved Approval) bool {
    return approved.PipelineID == current.PipelineID &&
        approved.Revision == current.Number &&
        approved.SemanticHash == current.SemanticHash &&
        approved.ReceiptID != ""
}
```

- [x] `go -C gateway test ./internal/pipeline -run 'TestApproval|TestInvalidation' -v`，确保缺revision/hash检查时至少一个竞态用例失败。
- [x] 回执消费与当前revision比较在SQLite事务内；UI已经展示旧卡不代表仍有效。revision2创建时将旧卡置过时、阻止未提交任务继续；对已提交任务提示其属于旧revision，由用户显式决定取消，不擅自重复生成。
- [x] 意图、脚本门禁使用同一questions机制，保存artifact version/hash依赖；改脚本使依赖它的分镜批准和Prompt合同不可用于新执行。只改价格/分辨率等参数生成新报价/合同，分镜语义批准保留。
- [x] 同一确认组件呈现明确的批准对象和版本，批准按钮不使用模糊“继续聊天”。提交 `feat(M6.3): invalidate stale creative approvals atomically`。

M6.3 engineering gate approved at 4ee7b84 (full check, 78 rendered UI checks, production build, and native release desktop smoke 39110e0240bf4d55908f299f55ad0249 PASS). True provider calls remain unexecuted.


### Task M6.4：精确 Prompt 合同、Preflight 与全血缘

**Files:**
- Create: `gateway/internal/pipeline/prompt.go`, `gateway/internal/pipeline/preflight.go`, `gateway/internal/pipeline/lineage.go`, `gateway/internal/pipeline/preflight_test.go`, `desktop/src/features/pipeline/PromptReview.tsx`。
- Modify: `gateway/internal/tasks/service.go`, `gateway/internal/tasks/worker.go`, `gateway/internal/mcp/documents.go`, `gateway/internal/mcp/tools.go`, `gateway/internal/server/pipeline.go`。

**Interfaces:** `Compile(ctx,pipelineID,revision,prompt,capabilityID,params,references)(Contract,error)`接受获批分镜派生的最终字符串，保存后不可变；`Preflight(ctx,ContractID)(Report,error)`；`AuthorizeExecution(ctx,TaskInput)error`被HTTP与MCP的共享Submit调用。Contract含prompt_version_id、prompt_sha256、storyboard_revision、approval_id、capability/config快照、有序references、参数及检查结果。

- [ ] 测试同字不同换行、尾随空格、UTF-8字节、引用顺序、旧批准、超时长/画幅、未声明params、占位符、缺失asset、跨项目asset、展示后改prompt；任一变化不得沿用旧合同执行。

```go
func PromptDigest(prompt string) string {
    digest := sha256.Sum256([]byte(prompt))
    return hex.EncodeToString(digest[:])
}
func TestPromptBytesAreSignificant(t *testing.T) {
    if PromptDigest("镜头一\n") == PromptDigest("镜头一\r\n") { t.Fatal("newline normalized") }
    if PromptDigest("镜头一") == PromptDigest("镜头一 ") { t.Fatal("space trimmed") }
}
```

- [ ] 编译过程可以由agent按技能生成最终文本，但保存后gateway不再改写。`final_video_prompt.md`文件内容就是将提交的字符串，不额外加标题/frontmatter；合同元数据存支撑表。UI原文视图读取同一版本字节，复制/提交不从Markdown DOM重建文本。
- [ ] Preflight复用capabilities.Validate并检查当前批准、资产存在及类型/数量、参数白名单、总时长/画幅、音频需求、明确占位符列表；检查结果逐项可读。模板编译记录未解析变量列表，辅以限定占位符语法检测，不用“包含中括号即失败”的粗略规则。
- [ ] 所有流水线请求绑定pipeline/contract，不能通过通用execute去掉字段绕过门禁：按目标Composition与活跃流水线关联查验。普通非流水线图像任务仍可运行。worker临提交前再次核对批准与合同并完成原子准入；批准后的新编辑只影响尚未准入的执行。
- [ ] 保存依赖链：分镜文档Version→Prompt文档Version（source_version_id）→task/generation_inputs→输出Version（source_version_id指向Prompt）→asset；version_dependencies额外保存源分镜、参考图、合同。分镜/Prompt/媒体是独立内容Resource，不能因为派生而塞入同一Resource；同一媒体修订才在该媒体Resource建新Version。
- [ ] `go -C gateway test ./internal/pipeline ./internal/tasks ./internal/mcp -v`检查两入口等效门禁与SQL血缘join；提交 `feat(M6.4): lock prompt bytes and enforce preflight lineage`。

### Task M6.5：失败回流与一句话工作流集成

**Files:**
- Create: `gateway/internal/pipeline/recovery.go`, `gateway/internal/pipeline/workflow_test.go`, `scripts/smoke-pipeline.mjs`。
- Modify: `gateway/internal/tasks/service.go`, `desktop/src/features/tasks/TaskCenter.tsx`, `desktop/src/features/pipeline/StoryboardReview.tsx`, `desktop/src/features/pipeline/PromptReview.tsx`, `scripts/check.ps1`, `docs/milestones.md`。

**Interfaces:** `ClassifyFailure(problem)(parameter|semantic|upstream|unknown)`是建议回流位置，不是自动重试指令。用户选择修参数→新合同；用户改语义→新分镜revision；未知受理→查询/对账。每条恢复操作保留requestId、原task/contract引用和人类回执。

- [ ] 表驱动测试参数错误不改分镜revision但生成新合同；语义修改使旧批准失效；upstream临时错误也不自动重发；unknown不会进入生成器；用户retry原任务只重放冻结输入；改输入创建新任务/合同并保留旧记录。
- [ ] `go -C gateway test ./internal/pipeline ./internal/tasks ./internal/delivery -v`，运行以本地假provider支持视频协议的smoke-pipeline；fixture显著标记测试，不写为真实视频验收。
- [ ] UI错误卡明确“修改参数”“修改分镜”“原样重试”“继续查询”的差异，按钮由服务端actions决定；结构化卡选择后进入对应阶段。不以聊天“我帮你重新生成”作为执行授权。
- [ ] 测试一句话→意图/脚本确认→分镜文档节点→批准→精确Prompt→报价确认→任务→版本→画布，以及中途退出重启。批准后改分镜，旧Prompt必须无法执行；媒体新版本可继续进入M3编辑器并保持血缘。
- [ ] 跑 `./scripts/check.ps1`与真实LLM技能流程，记录M6视频适配器仍为测试环境；提交 `feat(M6.5): route failures through revisioned creative gates`，推送M6，进入M7真实视频验收。
