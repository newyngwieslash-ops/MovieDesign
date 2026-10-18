import {useEffect,useRef,useState} from 'react';
import {api,APIError,newRequestID} from '../../gateway';
import {updatePending} from '../chat/messages';
type Panel={id:string;timeMs:number;framing:string;action:string;transition:string};
type Shot={id:string;startMs:number;endMs:number;panelIds:string[];narrativeRole:string;startState:string;endState:string};
type Storyboard={panels:Panel[];shots:Shot[];timeline:{durationMs:number;shotIds:string[];audioIntent:string}};
type Revision={pipelineId:string;revision:number;currentRevision:number;storyboard:Storyboard;semanticHash:string;documentVersionId:string;approvalStatus:string;promptStatus:string};
type Pending={projectID:string;pipelineID:string;versionID:string;revision:number;body:string};
const key=(project:string,pipeline:string)=>`moviedesign.storyboard.pending.v1.${project}.${pipeline}`;
export function readPending(project:string,pipeline:string):Pending|null {
 const raw=localStorage.getItem(key(project,pipeline));if(raw===null)return null;
 try {const p=JSON.parse(raw),b=JSON.parse(p.body),d=b.typedDocument,r=b.output.resources;
  if(Object.keys(p).sort().join(',')!=='body,pipelineID,projectID,revision,versionID'||p.projectID!==project||p.pipelineID!==pipeline||typeof p.versionID!=='string'||!p.versionID||!Number.isSafeInteger(p.revision)||p.revision<1||typeof p.body!=='string'||Object.keys(b).sort().join(',')!=='content,format,mode,output,requestId,typedDocument'||typeof b.requestId!=='string'||!(/^[A-Za-z0-9_-]{1,128}$/).test(b.requestId)||b.mode!=='replace'||b.format!=='md'||typeof b.content!=='string'||d.kind!=='storyboard'||d.pipelineId!==pipeline||d.expectedRevision!==p.revision||!d.storyboard||!Array.isArray(r)||r.length!==1||r[0].mode!=='new_version'||r[0].baseVersionId!==p.versionID)throw Error();return p;
 }catch{throw Error('分镜恢复记录损坏或归属不匹配；请保留本地数据并停止写入。')}
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
    owned={projectID,pipelineID:revision.pipelineId,versionID,revision:revision.revision,body};updatePending(localStorage,storageKey,null,owned);setPending(owned);
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
  <strong>分镜 revision {revision.revision}</strong><p>未批准 · 人类批准功能待接入；Prompt {revision.promptStatus}</p>
  <p>变更：{previous?(changed.join('、')||'无语义变化'):revision.revision===1?'首次分镜':'读取前版中'}</p>
  <h4>Master Timeline · {s.timeline.durationMs} ms</h4><p>音轨意图：{s.timeline.audioIntent}</p>
  {s.timeline.shotIds.map(id=>{const shot=s.shots.find(x=>x.id===id)!;return <div key={id}><h4>Logical Shot {id} · {shot.startMs}–{shot.endMs} ms</h4><p>叙事职责：{shot.narrativeRole}<br/>起始：{shot.startState}<br/>结束：{shot.endState}</p>{shot.panelIds.map(pid=>{const p=s.panels.find(x=>x.id===pid)!;return <p key={pid}><b>Panel {pid} · {p.timeMs} ms</b><br/>景别：{p.framing}<br/>主体动作：{p.action}<br/>转场依据：{p.transition}</p>})}</div>})}
  {revision.currentRevision!==revision.revision?<p>历史版本只读；当前 revision {revision.currentRevision}</p>:draft===null?<button disabled={busy||!!pending||storageError||!text} onClick={()=>setDraft(text)}>编辑分镜 Markdown</button>:<><label>分镜 Markdown 候选<textarea aria-label="分镜 Markdown 候选" disabled={busy||!!pending||storageError} value={draft} onChange={e=>setDraft(e.target.value)} rows={16} style={{width:'100%',boxSizing:'border-box'}}/></label><button disabled={busy||!!pending||storageError} onClick={()=>void save()}>校验并保存新 revision</button><button disabled={busy} onClick={()=>setDraft(null)}>取消编辑</button></>}
  {pending&&<button disabled={busy||storageError} onClick={()=>void save(true)}>重试原分镜请求</button>}{error&&<p role="alert">{error}</p>}
 </section>;
}
