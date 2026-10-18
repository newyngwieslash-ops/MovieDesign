import {useEffect,useRef,useState} from 'react';
import {useQuery} from '@tanstack/react-query';
import {EditorContent,useEditor} from '@tiptap/react';
import StarterKit from '@tiptap/starter-kit';
import {allPages,api,APIError,newRequestID,type Page} from '../../gateway';
import {subscribeEvents} from '../../events';
import {AssetMention,type MentionAsset} from './AssetMention';
import {AssistantMessage} from './AssistantMessage';
import {historyState,reduceEvent,messageBody,readPending,updatePending,pendingKey,type Pending,type Input,type Session,type Message,type DocNode} from './messages';
import type {Annotation} from '../editor/annotations';
import type {Skill,SkillInfo} from './messages';

export type AnnotationSource = {projectID:string;assetId:string;sourceVersionID:string;name:string;annotations:Annotation[]};
const emptyDoc: DocNode = {type:'doc',content:[{type:'paragraph'}]};
const labels: Record<string,string> = {queued:'已排队',submitting:'提交中',accepted:'已受理',done:'已完成',error:'失败',cancelled:'已取消',unknown:'结果未知'};
// Chat endpoints cap pages at 100; the asset helper uses the separate 200-row contract.
async function chatPages<T>(project:string,path:string,signal:AbortSignal): Promise<T[]> {
 const items:T[]=[];let offset=0;
 for(;;){const page=await api<Page<T>>(project,`${path}?offset=${offset}&limit=100`,{signal});items.push(...page.items);if(page.coverage.exhausted)return items;if(page.coverage.next_offset<=offset)throw Error('聊天分页进度无效');offset=page.coverage.next_offset;}
}
export function ChatPanel({projectID,annotationSource}:{projectID:string;annotationSource?:AnnotationSource}) {
 const [saved]=useState(()=>{try{return {create:readPending(localStorage.getItem(pendingKey(projectID,'create')),projectID,'create'),send:readPending(localStorage.getItem(pendingKey(projectID,'send')),projectID,'send'),cancel:readPending(localStorage.getItem(pendingKey(projectID,'cancel')),projectID,'cancel'),error:''};}catch(e){return {create:null,send:null,cancel:null,error:(e as Error).message};}});
 const [pending,setPending]=useState({create:saved.create,send:saved.send,cancel:saved.cancel});
 const [storageError,setStorageError]=useState(saved.error),[error,setError]=useState(''),[busy,setBusy]=useState(false);
 const [sessions,setSessions]=useState<Session[]>([]),[session,setSession]=useState(saved.send?.sessionID??saved.cancel?.sessionID??'');
 const [state,setState]=useState(()=>historyState(projectID,session,[]));
 const [annotations,setAnnotations]=useState<unknown[]>([]),[draftText,setDraftText]=useState('');
 const [skillID,setSkillID]=useState('__continue__');
 const skills=useQuery({queryKey:['skills'],queryFn:({signal})=>api<{items:SkillInfo[]}>(null,'/api/skills',{signal})});
 const preview=useQuery({queryKey:['skills',skillID],enabled:skillID!==''&&skillID!=='__continue__',queryFn:({signal})=>api<Skill>(null,`/api/skills?name=${encodeURIComponent(skillID)}`,{signal})});
 const lock=useRef(false),alive=useRef(true),suggesting=useRef(false),composing=useRef(false),refresh=useRef<()=>void>(()=>{}),sendDraft=useRef<()=>void>(()=>{});
 const assets=useQuery({queryKey:['project',projectID,'assets','all',200],queryFn:({signal})=>allPages<MentionAsset>(projectID,'/api/assets',signal)});
 const assetList=useRef<MentionAsset[]>([]);assetList.current=assets.data??[];
 const editor=useEditor({extensions:[StarterKit.configure({link:false}),AssetMention(projectID,()=>assetList.current,v=>{suggesting.current=v;})],content:emptyDoc,
  editorProps:{attributes:{role:'textbox','aria-label':'聊天消息','aria-multiline':'true'},handleDOMEvents:{compositionstart:()=>{composing.current=true;return false;},compositionend:()=>{composing.current=false;return false;}},handleKeyDown:(_view,event)=>{if(event.isComposing||composing.current||event.keyCode===229)return false;if(event.key==='Enter'&&!event.shiftKey&&!suggesting.current){event.preventDefault();sendDraft.current();return true;}return false;}},
  onUpdate:({editor})=>setDraftText(editor.getText()),
 });
 useEffect(()=>{alive.current=true;return()=>{alive.current=false;};},[]);
 useEffect(()=>{
  let disposed=false,reading=false,dirty=false;const controller=new AbortController();
  const load=async()=>{
   if(reading){dirty=true;return;}reading=true;
   do {dirty=false;try{const list=await chatPages<Session>(projectID,'/api/chat/sessions',controller.signal);if(disposed)return;setSessions(list);if(!session&&list.length){setSession(list[0].id);return;}
    if(session){const messages=await chatPages<Message>(projectID,`/api/chat/sessions/${session}/messages`,controller.signal);if(disposed)return;setState(historyState(projectID,session,messages));}setError('');
   }catch(e){if(!disposed)setError((e as Error).message);}}while(dirty&&!disposed);
   reading=false;
  };
  refresh.current=()=>void load();setState(historyState(projectID,session,[]));
  let unsubscribe=()=>{};
  try{unsubscribe=subscribeEvents(projectID,event=>{
   if(event.type!=='chat_message'&&event.type!=='chat_done')return;
   // GET has no event watermark: a delayed delta may already be in a history
   // snapshot. Invalidate on deltas instead of appending them a second time.
   if(!reading&&(event.payload as {type?:string})?.type!=='content_delta')setState(previous=>reduceEvent(previous,event));
   void load();
  },()=>void load(),session||undefined);}catch(e){setError(`事件连接不可用：${(e as Error).message}`);}
  void load();return()=>{disposed=true;controller.abort();unsubscribe();};
 },[projectID,session]);
 function persist(original:Pending,next:Pending|null,previous:Pending|null=original){
  try{updatePending(localStorage,pendingKey(projectID,original.kind),previous,next);setPending(v=>({...v,[original.kind]:next}));return true;}catch(e){setStorageError((e as Error).message);return false;}
 }
 async function execute(original:Pending,existing=false){
  if(lock.current||storageError)return;lock.current=true;setBusy(true);setError('');
  try{
   // Validate even a newly composed intent before storage/network. No mutation
   // happens on mount, reconnect, project selection or process restart.
   readPending(JSON.stringify(original),projectID,original.kind);
   if(!persist(original,original,existing?original:null))return;
   const path=original.kind==='create'?'/api/chat/sessions':`/api/chat/sessions/${original.sessionID}/${original.kind==='send'?'messages':'cancel'}`;
   const result=await api<Session|Message>(projectID,path,{method:'POST',body:original.body});
   if(!alive.current)return;
   if(original.kind==='cancel'&&original.edit){refresh.current();return;}
   if(!persist(original,null))return;
   if(original.kind==='create')setSession(result.id);
   if(original.kind==='send'){editor?.commands.clearContent();setAnnotations([]);setDraftText('');setSkillID('__continue__');}
   refresh.current();
  }catch(e){if(alive.current){
   // ponytail: this exact public text identifies the sole known non-applied 409;
   // use a typed reason if the contract expands. All other 409s retain identity.
   const rejectedWithdrawal=e instanceof APIError&&e.status===409&&e.code==='invalid_input'&&e.message==='Only queued messages can be withdrawn'&&original.kind==='cancel'&&!!JSON.parse(original.body).message_id;
   if(e instanceof APIError&&(rejectedWithdrawal||e.status>=400&&e.status<500&&e.status!==408&&e.status!==409&&e.status!==429)){persist(original,null);setError(e.message);}
   else setError(`结果待确认：${(e as Error).message}。请查询历史或使用同一原请求恢复；不会自动重发。`);
   refresh.current();}}
  finally{lock.current=false;if(alive.current)setBusy(false);}
 }
 function compose(){
  if(!editor||!session||busy||storageError||pending.send||pending.cancel||!editor.getText().trim())return;
  void execute({schema:1,projectID,kind:'send',sessionID:session,body:messageBody(newRequestID(),editor.getJSON(),editor.getText(),annotations,skillID==='__continue__'?undefined:skillID)});
 }
 sendDraft.current=compose;
 const hasDraft=!!editor&&!editor.isEmpty||annotations.length>0;
 function cancel(message?:Message,edit=false){
  if(!session||pending.cancel||pending.send||busy||storageError||message&&message.state!=='queued')return;
  if(edit&&hasDraft){setError('请先发送或清空当前草稿及标注，再撤回并编辑排队消息。');return;}
  void execute({schema:1,projectID,kind:'cancel',sessionID:session,body:JSON.stringify({requestId:newRequestID(),...(message?{message_id:message.id}:{})}),...(edit?{edit:structuredClone(message!.content)}:{})});
 }
 function restoreEdit(){
  const p=pending.cancel;if(!p?.edit||p.sessionID!==session||!state.messages.some(m=>m.id===JSON.parse(p.body).message_id&&m.state==='cancelled'))return;
  if(hasDraft){setError('请先发送或清空当前草稿及标注，再载入撤回消息。');return;}
  editor?.commands.setContent(p.edit.document);setAnnotations(structuredClone(p.edit.annotations));setDraftText(p.edit.text);setSkillID(p.edit.skill_id??'__continue__');
  persist(p,null);
 }
 function reconcileSend(){
  const p=pending.send;if(!p||p.sessionID!==session)return;
  const found=state.messages.find(m=>m.requestId===JSON.parse(p.body).requestId);
  if(found&&JSON.stringify(found.content)===p.body){if(persist(p,null)){editor?.commands.clearContent();setAnnotations([]);setDraftText('');}}else refresh.current();
 }
 function attach(){
  if(!annotationSource||annotationSource.projectID!==projectID||!editor)return;
  const source=annotationSource;setAnnotations(structuredClone(source.annotations.map(a=>({...a,sourceVersionID:source.sourceVersionID}))));
  editor.chain().focus().insertContent([{type:'mention',attrs:{path:`asset:${source.assetId}`,label:source.name}},{type:'text',text:' '}]).run();
 }
 const selectedSend=pending.send,selectedCancel=pending.cancel;
 const editable=!busy&&!pending.send&&!pending.cancel&&!storageError;
 const adopted=state.session===session?state.messages.at(-1)?.skill:undefined;
 const method=skillID==='__continue__'?adopted:skillID?preview.data:undefined;
 useEffect(()=>{editor?.setEditable(editable);},[editor,editable]);
 return <section className="chat-panel" aria-label="创作聊天">
  <header><div><h2>创作聊天</h2><p>输入 @ 引用素材。Enter 发送，Shift + Enter 换行。忙时消息由服务端排队。</p></div><button disabled={busy||!!storageError||!!pending.create} onClick={()=>void execute({schema:1,projectID,kind:'create',sessionID:'',body:JSON.stringify({requestId:newRequestID(),title:`创作对话 ${new Date().toLocaleString('zh-CN')}`})})}>新建对话</button></header>
  <label>当前对话<select aria-label="当前对话" value={session} disabled={busy} onChange={e=>setSession(e.target.value)}><option value="">请选择对话</option>{sessions.map(s=><option key={s.id} value={s.id}>{s.title} · {s.state}</option>)}</select></label><button onClick={()=>refresh.current()}>刷新聊天历史</button>
  <p className="chat-latency">供应商响应完成后显示增量快照；等待不代表失败。聊天取消不会取消独立的媒体生成任务。</p>
  <label>创作方法<select aria-label="创作方法" value={skillID} disabled={!editable} onChange={e=>setSkillID(e.target.value)}><option value="__continue__">沿用已采用方法；尚未采用时按需求匹配</option><option value="">停用创作方法</option>{skills.data?.items?.map(s=><option key={s.name} value={s.name}>{s.name} · {s.version}{s.modified?' · 本地改稿':''}</option>)}</select></label>
  <p>已采用：{adopted?`${adopted.name} · ${adopted.version} · ${adopted.revision.slice(0,12)}`:'无'}。选择方法随下一条消息生效，不代表批准后续方案或费用。</p>
  {method?.body&&<details><summary>查看制作方法与受管引用</summary><pre>{method.body}</pre>{Object.entries(method.references).map(([name,body])=><section key={name}><h3>{name}</h3><pre>{body}</pre></section>)}{method.modified&&<p>本地改稿已保留；随包版本另存于技能目录的 .bundled。当前对话继续使用受理时的快照；重新选择方法才采用当前文件。</p>}</details>}
  {(skills.error||preview.error)&&<p role="alert">{skills.error?.message||preview.error?.message}</p>}
  <div className="chat-history" aria-label="聊天记录">{state.messages.map(m=><article className="chat-turn" data-message-id={m.id} key={m.id}><header><strong>你</strong><span>{labels[m.state]??m.state}</span></header><pre>{m.content.text}</pre>{m.content.references?.length>0&&<p>素材：{m.content.references.join('、')}</p>}{m.content.annotations?.length>0&&<details><summary>发送时的标注（{m.content.annotations.length}）</summary><pre>{JSON.stringify(m.content.annotations,null,2)}</pre></details>}{m.error&&<p role="alert">{m.error}</p>}{m.state==='queued'&&<div><button disabled={!editable||hasDraft} onClick={()=>cancel(m,true)}>撤回并编辑</button><button disabled={!editable} onClick={()=>cancel(m)}>撤回消息</button>{hasDraft&&<p>请先发送或清空当前草稿及标注，再撤回并编辑。</p>}</div>}<AssistantMessage parts={state.parts[m.id]??[]} /></article>)}</div>
  {(['create','send','cancel'] as const).map(kind=>{const p=pending[kind];return p&&<div className="chat-pending" key={kind}><strong>待确认的{kind==='create'?'创建':kind==='send'?'发送':'撤回/取消'}原请求</strong><p>请求号：{JSON.parse(p.body).requestId} {p.sessionID&&`· 对话 ${p.sessionID}`}</p>{kind==='send'&&<pre>{(JSON.parse(p.body) as Input).text}</pre>}<button disabled={busy||!!storageError} onClick={()=>void execute(p,true)}>恢复原{kind==='create'?'创建':kind==='send'?'发送':'取消'}请求</button>{kind==='send'&&<button disabled={busy||p.sessionID!==session||!!storageError} onClick={reconcileSend}>核对历史受理记录</button>}{kind==='cancel'&&p.edit&&<button disabled={busy||!!storageError||p.sessionID!==session||!state.messages.some(m=>m.id===JSON.parse(p.body).message_id&&m.state==='cancelled')} onClick={restoreEdit}>载入已撤回草稿</button>}</div>;})}
  <div className="chat-composer"><div className="chat-format"><button disabled={!editable} onClick={()=>editor?.chain().focus().toggleBulletList().run()}>列表</button><button disabled={!editable} onClick={()=>editor?.chain().focus().toggleBlockquote().run()}>引用段落</button><button disabled={!editable} onClick={()=>{editor?.commands.clearContent();setAnnotations([]);setDraftText('');}}>清空草稿</button></div><EditorContent editor={editor}/>
   {annotationSource?.projectID===projectID&&annotationSource.annotations.length>0&&<button disabled={!editable} onClick={attach}>附加当前标注 · {annotationSource.name} · 版本 {annotationSource.sourceVersionID}</button>}
   {annotations.length>0&&<details open><summary>草稿标注来源（{annotations.length}）</summary><pre>{JSON.stringify(annotations,null,2)}</pre><button disabled={!editable} onClick={()=>setAnnotations([])}>移除草稿标注</button></details>}
   <button disabled={!session||!editable||!draftText.trim()} onClick={compose}>{busy?'发送中':'发送消息'}</button><button disabled={!session||busy||!!storageError||!!selectedSend||!!selectedCancel} onClick={()=>cancel()}>取消本次对话及队列</button>
  </div>{(error||storageError||assets.error)&&<p role="alert">{storageError||error||assets.error?.message}</p>}
 </section>;
}
