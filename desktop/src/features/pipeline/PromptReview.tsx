import {subscribeEvents} from '../../events';
import {useEffect,useState} from 'react';
import {api,APIError} from '../../gateway';
type Check={name:string;passed:boolean;code?:string;message:string};
type Ref={index:number;assetId:string;versionId?:string;role:string;mediaKind:string;mimeType:string};
type Contract={id:string;status:string;capabilityId:string;promptSha256:string;promptBytes:number;approvalId:string;taskId?:string;audio:{audioIntent:string;required:boolean;narration:boolean;music:boolean;sfx:boolean;mode:string};references:Ref[];preflight:{passed:boolean;checks:Check[]}};
type Node={gate:string;revision:number;documentVersionId:string;sourceVersionId?:string;approvalStatus?:string};
type Lineage={pipelineId:string;intent?:Node;script?:Node;storyboard?:Node;prompt?:Node;contract?:Contract;outputs:{taskId:string;versionId:string;sourceVersionId:string}[]};
type PromptView={pipelineId:string;revision:number;currentRevision:number;storyboardRevision:number;currentStoryboardRevision:number;documentVersionId:string;sourceVersionId:string;approvalId:string;content:string;promptSha256:string;promptBytes:number;promptStatus:string;contract?:Contract;lineage?:Lineage};
export function PromptReview({projectID,versionID,text}:{projectID:string;versionID:string;text:string}) {
 const [view,setView]=useState<PromptView|null>(null),[lineage,setLineage]=useState<Lineage|null>(null),[error,setError]=useState('');
 useEffect(()=>{const abort=new AbortController();setView(null);setLineage(null);setError('');
  void (async()=>{try{
   const v=await api<PromptView>(projectID,`/api/pipeline/prompt?versionId=${encodeURIComponent(versionID)}`,{signal:abort.signal});if(abort.signal.aborted)return;setView(v);if(v.lineage)setLineage(v.lineage);
   const l=await api<Lineage>(projectID,`/api/pipeline/lineage?versionId=${encodeURIComponent(versionID)}`,{signal:abort.signal});if(!abort.signal.aborted)setLineage(l);
  }catch(e){if(!abort.signal.aborted&&!(e instanceof APIError&&e.status===404))setError(String(e))}})();return()=>abort.abort();
 },[projectID,versionID]);
 useEffect(()=>{let alive=true;const refresh=()=>{void api<PromptView>(projectID,`/api/pipeline/prompt?versionId=${encodeURIComponent(versionID)}`).then(v=>{if(alive){setView(v);if(v.lineage)setLineage(v.lineage)}}).catch(()=>{});void api<Lineage>(projectID,`/api/pipeline/lineage?versionId=${encodeURIComponent(versionID)}`).then(l=>{if(alive)setLineage(l)}).catch(()=>{})};const stop=subscribeEvents(projectID,e=>{if(['question_resolved','task_updated','canvas_changed'].includes(e.type))refresh()},refresh);return()=>{alive=false;stop()}},[projectID,versionID]);
 if(!view)return <>{error&&<p role="alert">{error}</p>}<pre>{text||'读取文档…'}</pre></>;
 const c=view.contract,l=lineage??view.lineage;
 return <section className="prompt-review" aria-label="最终生成 Prompt 契约审阅" style={{padding:12,userSelect:'text'}}>
  <strong>Prompt revision {view.revision}</strong>
  <p>契约状态：{c?.status??view.promptStatus} · 绑定分镜 revision {view.storyboardRevision} · 分镜批准 {view.approvalId}</p>
  <p>字节锁定：{view.promptBytes} bytes · SHA-256 {view.promptSha256}</p>
  {view.currentRevision!==view.revision&&<p>历史 Prompt 版本只读；当前 revision {view.currentRevision}</p>}
  {c&&<>
   <p>契约 ID：{c.id} · 目标能力：{c.capabilityId}{c.taskId?` · 任务：${c.taskId}`:''}</p>
   <p>音轨契约：{c.audio.mode}（口播：{c.audio.narration?'是':'否'} · 配乐：{c.audio.music?'是':'否'} · 意图：{c.audio.audioIntent}）</p>
   <p>有序参考资产（{c.references.length}）：{c.references.length?c.references.map(r=>`#${r.index} ${r.assetId}(${r.role}/${r.mediaKind})`).join(' → '):'无'}</p>
   <div><strong>Preflight 七项门禁：{c.preflight.passed?'全部通过':'阻断'}</strong><ul>{c.preflight.checks.map(k=><li key={k.name}>{k.name}：{k.passed?'通过':`阻断 (${k.code??'FAIL'})`} — {k.message}</li>)}</ul></div>
  </>}
  {l&&<p>SQL 血缘链路：意图 r{l.intent?.revision??'-'} → 脚本 r{l.script?.revision??'-'} → 分镜 r{l.storyboard?.revision??'-'} → Prompt r{l.prompt?.revision??view.revision} · 已投递产物 {l.outputs.length}</p>}
  <pre data-prompt-sha256={view.promptSha256} data-prompt-bytes={view.promptBytes}>{view.content}</pre>
  {error&&<p role="alert">{error}</p>}
 </section>;
}
