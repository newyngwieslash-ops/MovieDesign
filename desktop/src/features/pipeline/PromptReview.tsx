import {subscribeEvents} from '../../events';
import {useEffect,useState} from 'react';
import {api,APIError,newRequestID} from '../../gateway';
type Check={name:string;passed:boolean;code?:string;message:string};
type Ref={ordinal?:number;index?:number;assetId:string;versionId?:string;role:string;mediaKind?:string;mimeType?:string;sha256?:string};
type RecoveryLink={fromTaskId:string;fromContractId?:string;receiptId?:string;requestId?:string};
type RecoveryState={stage:string;recommendedAction:string;selectedAction?:string;targetStage?:string;receiptId?:string;requestId?:string;fromTaskId:string;fromContractId?:string;pipelineId?:string;storyboardVersionId?:string;promptVersionId?:string};
type Contract={id:string;status:string;capabilityId:string;promptSha256:string;promptBytes:number;approvalId:string;taskId?:string;params?:Record<string,unknown>;audio:{audioIntent:string;requiresAudio?:boolean;requiresNarration?:boolean;requiresMusic?:boolean;required?:boolean;narration?:boolean;music?:boolean;sfx?:boolean;mode?:string};references:Ref[];preflight:{passed:boolean;checks:Check[]};recovery?:RecoveryLink};
type Node={gate:string;revision:number;documentVersionId:string;sourceVersionId?:string;approvalStatus?:string};
type Lineage={pipelineId:string;intent?:Node;script?:Node;storyboard?:Node;prompt?:Node;contract?:Contract;outputs:{taskId:string;versionId:string;sourceVersionId:string}[]};
type PromptView={pipelineId:string;revision:number;currentRevision:number;storyboardRevision:number;currentStoryboardRevision:number;documentVersionId:string;sourceVersionId:string;approvalId:string;content:string;promptSha256:string;promptBytes:number;promptStatus:string;contract?:Contract;lineage?:Lineage;recovery?:RecoveryState};
export function PromptReview({projectID,versionID,text}:{projectID:string;versionID:string;text:string}) {
 const [view,setView]=useState<PromptView|null>(null),[lineage,setLineage]=useState<Lineage|null>(null),[error,setError]=useState(''),[paramDraft,setParamDraft]=useState(''),[busy,setBusy]=useState(false);
 useEffect(()=>{const abort=new AbortController();setView(null);setLineage(null);setError('');setParamDraft('');setBusy(false);
  void (async()=>{try{
   const v=await api<PromptView>(projectID,`/api/pipeline/prompt?versionId=${encodeURIComponent(versionID)}`,{signal:abort.signal});if(abort.signal.aborted)return;setView(v);setParamDraft(JSON.stringify(v.contract?.params??{}));if(v.lineage)setLineage(v.lineage);
   const l=await api<Lineage>(projectID,`/api/pipeline/lineage?versionId=${encodeURIComponent(versionID)}`,{signal:abort.signal});if(!abort.signal.aborted)setLineage(l);
  }catch(e){if(!abort.signal.aborted&&!(e instanceof APIError&&e.status===404))setError(String(e))}})();return()=>abort.abort();
 },[projectID,versionID]);
 useEffect(()=>{let alive=true;const refresh=()=>{void api<PromptView>(projectID,`/api/pipeline/prompt?versionId=${encodeURIComponent(versionID)}`).then(v=>{if(alive){setView(v);setParamDraft(p=>p||JSON.stringify(v.contract?.params??{}));if(v.lineage)setLineage(v.lineage)}}).catch(()=>{});void api<Lineage>(projectID,`/api/pipeline/lineage?versionId=${encodeURIComponent(versionID)}`).then(l=>{if(alive)setLineage(l)}).catch(()=>{})};const stop=subscribeEvents(projectID,e=>{if(['question_resolved','task_updated','task_changed','canvas_changed'].includes(e.type))refresh()},refresh);return()=>{alive=false;stop()}},[projectID,versionID]);
 if(!view)return <>{error&&<p role="alert">{error}</p>}<pre>{text||'读取文档…'}</pre></>;
 const c=view.contract,l=lineage??view.lineage;
 const hasNarration=Boolean(c?.audio.requiresNarration??c?.audio.narration),hasMusic=Boolean(c?.audio.requiresMusic??c?.audio.music);
 const audioMode=c?.audio.mode||(typeof c?.params?.audio_mode==='string'?c.params.audio_mode:hasNarration&&hasMusic?'narration+music':hasNarration?'narration':hasMusic?'music':'none');
 const compileParams=async()=>{
  if(!c||busy)return;setBusy(true);setError('');
  try{
   const parsed:unknown=JSON.parse(paramDraft||'{}');if(!parsed||typeof parsed!=='object'||Array.isArray(parsed))throw Error('参数必须为 JSON 对象');
   const reqID=(typeof newRequestID==='function'?newRequestID():crypto.randomUUID());
   await api(projectID,'/api/pipeline/compile',{method:'POST',body:JSON.stringify({requestId:reqID,pipelineId:view.pipelineId,promptVersionId:view.documentVersionId,expectedRevision:view.revision,prompt:view.content,capabilityId:c.capabilityId,references:c.references.map(r=>r.assetId),params:parsed,fromTaskId:view.recovery?.fromTaskId??'',fromContractId:view.recovery?.fromContractId??c.id})});
   const v=await api<PromptView>(projectID,`/api/pipeline/prompt?versionId=${encodeURIComponent(versionID)}`);setView(v);setParamDraft(JSON.stringify(v.contract?.params??{}));if(v.lineage)setLineage(v.lineage);
  }catch(e){setError(e instanceof Error?e.message:String(e))}finally{setBusy(false)}
 };
 return <section className="prompt-review" aria-label="最终生成 Prompt 契约审阅" style={{padding:12,userSelect:'text'}}>
  <strong>Prompt revision {view.revision}</strong>
  <p>契约状态：{c?.status??view.promptStatus} · 绑定分镜 revision {view.storyboardRevision} · 分镜批准 {view.approvalId}</p>
  <p>字节锁定：{view.promptBytes} bytes · SHA-256 {view.promptSha256}</p>
  {view.currentRevision!==view.revision&&<p>历史 Prompt 版本只读；当前 revision {view.currentRevision}</p>}
  {c&&<>
   <p>契约 ID：{c.id} · 目标能力：{c.capabilityId}{c.taskId?` · 任务：${c.taskId}`:''}</p>
   {c.capabilityId.startsWith('video:')&&<p data-video-adapter-status="m6-test-only">M6 视频适配器状态：当前仅支持离线审阅、批准与编译视频契约（测试环境仅使用显式 fixture，真实 ark-video 执行适配器将在 M7.1 接入）。</p>}
   {c.recovery&&<p data-contract-recovery={c.recovery.fromTaskId}>回流溯源：源自任务 {c.recovery.fromTaskId}{c.recovery.fromContractId?` · 原合同 ${c.recovery.fromContractId}`:''}{c.recovery.requestId?` · 请求 ${c.recovery.requestId}`:''}</p>}
   <p>音轨契约：{audioMode}（口播：{hasNarration?'是':'否'} · 配乐：{hasMusic?'是':'否'} · 意图：{c.audio.audioIntent}）</p>
   <p>有序参考资产（{c.references.length}）：{c.references.length?c.references.map((r,idx)=>`#${r.ordinal??r.index??idx} ${r.assetId}(${r.role}/${r.mediaKind||(r.mimeType?.startsWith('video/')?'video':r.mimeType?.startsWith('image/')?'image':r.mimeType)||'asset'})`).join(' → '):'无'}</p>
   <div><strong>Preflight 七项门禁：{c.preflight.passed?'全部通过':'阻断'}</strong><ul>{c.preflight.checks.map(k=><li key={k.name}>{k.name}：{k.passed?'通过':`阻断 (${k.code??'FAIL'})`} — {k.message}</li>)}</ul></div>
  </>}
  {c&&view.currentRevision===view.revision&&view.currentStoryboardRevision===view.storyboardRevision&&view.recovery?.selectedAction==='modify_params'&&<div className="prompt-recovery" data-recovery-stage="parameter"><p>已进入「修改参数」回流阶段（原任务 {view.recovery.fromTaskId}{view.recovery.fromContractId?` · 原合同 ${view.recovery.fromContractId}`:''}{view.recovery.receiptId?` · 回执 ${view.recovery.receiptId}`:''}）：保持当前获批分镜 revision {view.storyboardRevision} 不变，修改参数后编译新合同。</p><label>回流参数 JSON<textarea aria-label="回流参数 JSON" disabled={busy} value={paramDraft} onChange={e=>setParamDraft(e.target.value)} rows={3} style={{width:'100%',boxSizing:'border-box'}}/></label><button disabled={busy} onClick={()=>void compileParams()}>编译新参数契约</button></div>}
  {l&&<p>SQL 血缘链路：意图 r{l.intent?.revision??'-'} → 脚本 r{l.script?.revision??'-'} → 分镜 r{l.storyboard?.revision??'-'} → Prompt r{l.prompt?.revision??view.revision} · 已投递产物 {l.outputs.length}</p>}
  <pre data-prompt-sha256={view.promptSha256} data-prompt-bytes={view.promptBytes}>{view.content}</pre>
  {error&&<p role="alert">{error}</p>}
 </section>;
}
