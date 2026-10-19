import {subscribeEvents} from '../../events';
import {useEffect,useRef,useState} from 'react';
import {api,APIError,newRequestID} from '../../gateway';
import {updatePending} from '../chat/messages';
type Panel={id:string;timeMs:number;framing:string;action:string;transition:string};
type Shot={id:string;startMs:number;endMs:number;panelIds:string[];narrativeRole:string;startState:string;endState:string};
type Storyboard={panels:Panel[];shots:Shot[];timeline:{durationMs:number;shotIds:string[];audioIntent:string}};
type RecoveryState={stage:string;recommendedAction:string;selectedAction?:string;targetStage?:string;receiptId?:string;requestId?:string;fromTaskId:string;fromContractId?:string;pipelineId?:string;storyboardVersionId?:string;promptVersionId?:string};
type Revision={pipelineId:string;revision:number;currentRevision:number;storyboard:Storyboard;semanticHash:string;documentVersionId:string;approvalStatus:string;promptStatus:string;recovery?:RecoveryState};
type Pending={projectID:string;pipelineID:string;versionID:string;revision:number;body:string};
const key=(project:string,pipeline:string)=>`moviedesign.storyboard.pending.v1.${project}.${pipeline}`;
function fields(value:unknown,names:string):asserts value is Record<string,unknown> {if(!value||typeof value!=='object'||Array.isArray(value)||Object.keys(value).sort().join(',')!==names)throw Error('Invalid recovery fields')}
const text=(value:unknown):value is string=>typeof value==='string'&&value.trim().length>0;
const identifier=(value:unknown):value is string=>typeof value==='string'&&/^[A-Za-z0-9_-]{1,128}$/.test(value);
const milliseconds=(value:unknown)=>Number.isSafeInteger(value)&&Number(value)>=0;
function storyboardShape(value:unknown):asserts value is Storyboard {
 fields(value,'panels,shots,timeline');const {panels,shots,timeline}=value;
 if(!Array.isArray(panels)||!panels.length||!Array.isArray(shots)||!shots.length)throw Error();
 const ids=new Set<string>(),panelIds=new Set<string>(),shotIds=new Set<string>();
 for(const p of panels){fields(p,'action,framing,id,timeMs,transition');if(!text(p.id)||ids.has(p.id)||!milliseconds(p.timeMs)||![p.framing,p.action,p.transition].every(text))throw Error();ids.add(p.id);panelIds.add(p.id)}
 for(const s of shots){fields(s,'endMs,endState,id,narrativeRole,panelIds,startMs,startState');if(!text(s.id)||ids.has(s.id)||!milliseconds(s.startMs)||!milliseconds(s.endMs)||![s.narrativeRole,s.startState,s.endState].every(text)||!Array.isArray(s.panelIds)||!s.panelIds.length||new Set(s.panelIds).size!==s.panelIds.length||!s.panelIds.every(id=>typeof id==='string'&&panelIds.has(id)))throw Error();ids.add(s.id);shotIds.add(s.id)}
 fields(timeline,'audioIntent,durationMs,shotIds');if(!milliseconds(timeline.durationMs)||!text(timeline.audioIntent)||!Array.isArray(timeline.shotIds)||timeline.shotIds.length!==shots.length||new Set(timeline.shotIds).size!==shots.length||!timeline.shotIds.every(id=>typeof id==='string'&&shotIds.has(id)))throw Error();
}
// Compare parsed candidate fields independent of JSON object order; replay keeps
// the original bytes. Backend remains authoritative for intervals/coverage.
function sameJSON(a:unknown,b:unknown):boolean {
 if(a===b)return true;if(!a||!b||typeof a!=='object'||typeof b!=='object'||Array.isArray(a)!==Array.isArray(b))return false;
 const x=a as Record<string,unknown>,y=b as Record<string,unknown>,keys=Object.keys(x);return keys.length===Object.keys(y).length&&keys.every(k=>Object.hasOwn(y,k)&&sameJSON(x[k],y[k]));
}
function validatePending(p:unknown,project:string,pipeline:string):Pending {
  fields(p,'body,pipelineID,projectID,revision,versionID');
  if(!identifier(project)||!identifier(pipeline)||p.projectID!==project||p.pipelineID!==pipeline||!identifier(p.versionID)||!Number.isSafeInteger(p.revision)||Number(p.revision)<1||typeof p.body!=='string')throw Error();
  const b:unknown=JSON.parse(p.body);fields(b,'content,format,mode,output,requestId,typedDocument');
  if(!identifier(b.requestId)||b.mode!=='replace'||b.format!=='md'||typeof b.content!=='string')throw Error();
  const d=b.typedDocument;fields(d,'expectedRevision,kind,pipelineId,storyboard');if(d.kind!=='storyboard'||d.pipelineId!==pipeline||d.expectedRevision!==p.revision)throw Error();storyboardShape(d.storyboard);
  const blocks=Array.from(b.content.matchAll(/```json\n([\s\S]*?)\n```/g));if(blocks.length!==1)throw Error();const candidate:unknown=JSON.parse(blocks[0][1]);storyboardShape(candidate);if(!sameJSON(candidate,d.storyboard))throw Error();
  const output=b.output;fields(output,'composition,resources');if(!Array.isArray(output.resources)||output.resources.length!==1)throw Error();
  const resource=output.resources[0];fields(resource,'baseVersionId,key,mode,name');if(resource.key!=='review'||resource.mode!=='new_version'||resource.name!=='storyboard_review.md'||resource.baseVersionId!==p.versionID)throw Error();
  const composition=output.composition;fields(composition,'name,placements,type');if(!text(composition.name)||Array.from(composition.name).length>256)throw Error();fields(composition.type,'id');if(!identifier(composition.type.id)||!Array.isArray(composition.placements)||composition.placements.length!==1)throw Error();
  const placement=composition.placements[0];fields(placement,'key,role');if(placement.key!==resource.key||placement.role!=='storyboard')throw Error();
  return p as Pending;
}
export function readPending(project:string,pipeline:string):Pending|null {
 const raw=localStorage.getItem(key(project,pipeline));if(raw===null)return null;
 try{return validatePending(JSON.parse(raw),project,pipeline)}catch{throw Error('分镜恢复记录损坏或归属不匹配；请保留本地数据并停止写入。')}
}
export function StoryboardReview({projectID,versionID,text}:{projectID:string;versionID:string;text:string}) {
 const [revision,setRevision]=useState<Revision|null>(null),[error,setError]=useState(''),[draft,setDraft]=useState<string|null>(null),[busy,setBusy]=useState(false),[previous,setPrevious]=useState<Revision|null>(null),[pending,setPending]=useState<Pending|null>(null),[storageError,setStorageError]=useState(false);
 const lock=useRef(false),epoch=useRef(0);
 useEffect(()=>{const abort=new AbortController();epoch.current++;setRevision(null);setError('');setDraft(null);setPrevious(null);setPending(null);setStorageError(false);setBusy(false);
  void (async()=>{try{
   const r=await api<Revision>(projectID,`/api/pipeline/storyboard?versionId=${encodeURIComponent(versionID)}`,{signal:abort.signal});if(abort.signal.aborted)return;setRevision(r);
   try{setPending(readPending(projectID,r.pipelineId))}catch(e){setStorageError(true);setError(String(e))}
   if(r.revision>1){const v=await api<{source_version_id:string|null}>(projectID,`/api/versions?id=${encodeURIComponent(versionID)}`,{signal:abort.signal});if(v.source_version_id){const old=await api<Revision>(projectID,`/api/pipeline/storyboard?versionId=${encodeURIComponent(v.source_version_id)}`,{signal:abort.signal});if(!abort.signal.aborted)setPrevious(old)}}
  }catch(e){if(!abort.signal.aborted&&!(e instanceof APIError&&e.status===404))setError(String(e))}})();return()=>{abort.abort();epoch.current++};
 },[projectID,versionID]);
 useEffect(()=>{let alive=true;const refresh=()=>{void api<Revision>(projectID,`/api/pipeline/storyboard?versionId=${encodeURIComponent(versionID)}`).then(r=>{if(alive)setRevision(r)}).catch(()=>{})};const stop=subscribeEvents(projectID,e=>{if(['question_resolved','task_changed'].includes(e.type))refresh()},refresh);return()=>{alive=false;stop()}},[projectID,versionID]);
 const propose=async()=>{if(!revision||lock.current)return;lock.current=true;setBusy(true);const generation=epoch.current;try{await api(projectID,'/api/question',{method:'POST',body:JSON.stringify({requestId:`review-${versionID}`,session_id:'pipeline',kind:'creative',creative:{pipelineId:revision.pipelineId,gate:'storyboard',revision:revision.revision,documentVersionId:versionID,semanticHash:revision.semanticHash}})});if(epoch.current===generation)setError('已提出此版本确认卡，请在待确认事项中批准。')}catch(e){if(epoch.current===generation)setError(String(e)+'；可再次点击，用同一请求核对确认卡。')}finally{lock.current=false;if(epoch.current===generation)setBusy(false)}};
 const save=async(retry=false)=>{
  if(!revision||lock.current||storageError)return;lock.current=true;setBusy(true);setError('');const generation=epoch.current,storageKey=key(projectID,revision.pipelineId);let owned:Pending|null=null;
  const current=()=>epoch.current===generation;
  try{
   owned=readPending(projectID,revision.pipelineId);
   if(retry&&!owned)throw Error('原请求已处理，请重新加载分镜');
   if(!retry){if(owned){setPending(owned);throw Error('已有未确认的分镜请求，请恢复原请求')}
    const match=draft?.match(/```json\n([\s\S]*?)\n```/);if(!match)throw Error('请保留 Markdown 中的 JSON 分镜区块');const storyboard=JSON.parse(match[1]) as Storyboard;
    const version=await api<{resource_id:string}>(projectID,`/api/versions?id=${encodeURIComponent(versionID)}`);
    const resource=await api<{composition_id:string}>(projectID,`/api/resources?id=${encodeURIComponent(version.resource_id)}`);
    const composition=await api<{name:string;type_id:string}>(projectID,`/api/compositions?id=${encodeURIComponent(resource.composition_id)}`);
    if(!current())return;
    const body=JSON.stringify({requestId:newRequestID(),mode:'replace',format:'md',content:draft,typedDocument:{kind:'storyboard',pipelineId:revision.pipelineId,expectedRevision:revision.revision,storyboard},output:{resources:[{key:'review',mode:'new_version',name:'storyboard_review.md',baseVersionId:versionID}],composition:{name:composition.name,type:{id:composition.type_id},placements:[{key:'review',role:'storyboard'}]}}});
    try{owned=validatePending({projectID,pipelineID:revision.pipelineId,versionID,revision:revision.revision,body},projectID,revision.pipelineId)}catch{throw Error('分镜候选或输出结构无效；请修正后再保存。')}
    updatePending(localStorage,storageKey,null,owned);setPending(owned);
   }
   const result=await api<{delivery:{status:string;problem?:{message:string}}}>(projectID,'/api/documents/write',{method:'POST',body:owned!.body});
   if(result.delivery.status==='unknown')throw Error('提交结果未知，请用原请求重试');
   if(!['completed','blocked'].includes(result.delivery.status))throw Error('无法确认交付结果，请保留原请求');
   updatePending(localStorage,storageKey,owned,null);if(current())setPending(null);
   if(result.delivery.status==='blocked')throw Error(result.delivery.problem?.message??'提交被阻止，请重新加载');
   if(current())setDraft(null);
  }catch(e){
   if(owned&&e instanceof APIError&&[400,404,409,413].includes(e.status)){try{updatePending(localStorage,storageKey,owned,null);if(current())setPending(null)}catch{ /* A newer intent belongs to another node; retain it. */ }}
   if(current())setError(e instanceof Error?e.message:String(e));
  }finally{lock.current=false;if(current())setBusy(false)}
 };
 if(!revision)return <>{error&&<p role="alert">{error}</p>}<pre>{text||'读取文档…'}</pre></>;
 const s=revision.storyboard,changed=previous?(['panels','shots','timeline'] as const).filter(k=>JSON.stringify(previous.storyboard[k])!==JSON.stringify(s[k])):[];
 return <section className="storyboard-review" aria-label="语义分镜审阅" style={{padding:12,userSelect:'text'}}>
  <strong>分镜 revision {revision.revision}</strong><p>{revision.approvalStatus==='approved'?'此版本已批准':'未批准'} · Prompt {revision.promptStatus}</p>{revision.currentRevision===revision.revision&&revision.approvalStatus!=='approved'&&<button disabled={busy||!!pending||storageError} onClick={()=>void propose()}>提出此版本批准卡</button>}<p>内容变更会使旧批准失效。已提交任务仍属于其冻结版本，请在任务中心明确决定是否取消。</p>
  {revision.recovery?.selectedAction==='modify_storyboard'&&revision.currentRevision===revision.revision&&<p className="storyboard-recovery" data-recovery-stage="semantic">已进入「修改分镜」语义回流阶段（原任务 {revision.recovery.fromTaskId}{revision.recovery.fromContractId?` · 原合同 ${revision.recovery.fromContractId}`:''}{revision.recovery.receiptId?` · 回执 ${revision.recovery.receiptId}`:''}）：编辑并保存将生成新分镜 revision，使当前分镜与下游 Prompt 批准失效，保留上游意图与脚本批准。</p>}
  <p>变更：{previous?(changed.join('、')||'无语义变化'):revision.revision===1?'首次分镜':'读取前版中'}</p>
  <h4>Master Timeline · {s.timeline.durationMs} ms</h4><p>音轨意图：{s.timeline.audioIntent}</p>
  {s.timeline.shotIds.map(id=>{const shot=s.shots.find(x=>x.id===id)!;return <div key={id}><h4>Logical Shot {id} · {shot.startMs}–{shot.endMs} ms</h4><p>叙事职责：{shot.narrativeRole}<br/>起始：{shot.startState}<br/>结束：{shot.endState}</p>{shot.panelIds.map(pid=>{const p=s.panels.find(x=>x.id===pid)!;return <p key={pid}><b>Panel {pid} · {p.timeMs} ms</b><br/>景别：{p.framing}<br/>主体动作：{p.action}<br/>转场依据：{p.transition}</p>})}</div>})}
  {revision.currentRevision!==revision.revision?<p>历史版本只读；当前 revision {revision.currentRevision}</p>:draft===null?<button disabled={busy||!!pending||storageError||!text} onClick={()=>setDraft(text)}>编辑分镜 Markdown</button>:<><label>分镜 Markdown 候选<textarea aria-label="分镜 Markdown 候选" disabled={busy||!!pending||storageError} value={draft} onChange={e=>setDraft(e.target.value)} rows={16} style={{width:'100%',boxSizing:'border-box'}}/></label><button disabled={busy||!!pending||storageError} onClick={()=>void save()}>校验并保存新 revision</button><button disabled={busy} onClick={()=>setDraft(null)}>取消编辑</button></>}
  {pending&&<button disabled={busy||storageError} onClick={()=>void save(true)}>重试原分镜请求</button>}{error&&<p role="alert">{error}</p>}
 </section>;
}
