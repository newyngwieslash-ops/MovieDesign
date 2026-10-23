import {canDecide, type Task} from '../tasks/tasks';
import { useEffect, useRef, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { APIError, api, newRequestID, type Page } from '../../gateway';
import { subscribeEvents } from '../../events';
import { readIntent, clearIntent, type Intent } from './pending';
import type { Part } from '../chat/messages';
import type { ComponentProps } from 'react';

export function DecisionFrame({title, children, className='confirmation-card', ...props}: ComponentProps<'section'> & {title: string}) {
 return <section className={className} aria-label={title} {...props}><h3>{title}</h3>{children}</section>;
}

type Info = { header: string; question: string; options: {label: string; description: string}[]; multiple?: boolean; custom?: boolean };
export type Question = { id: string; project_id: string; session_id: string; task_id?: string; chat_message_id?: string; kind: 'billing'|'choice'|'storyboard'|'delivery'|'project_rules'|'creative'; creativeContent?:string; creative?:{pipelineId:string;gate:'intent'|'script'|'storyboard';revision:number;documentVersionId:string;semanticHash:string}; revision: number; task_revision?: number; state: string; native_send_state?: 'pending'|'sending'|'sent'|'unknown'; questions: Info[]; provider?: string; model?: string; reason?: string; impacts?: string[]; quote?: {basis?: string; quote_id: string; currency: string; estimated_amount: number; minor_unit_scale: number; estimated_credits: number; expires_at: number; version: string; source: string} };
export function ConfirmationCard({question: q, refresh}: {question: Question; refresh: () => void}) {
 const storageKey = `moviedesign.question.pending.v1.${q.project_id}.${q.id}`;
 const [saved] = useState(() => {try {return {intent:readIntent(localStorage,storageKey,q),error:''}} catch(e) {return {intent:null,error:(e as Error).message}}});
 const [intent, setIntent] = useState<Intent|null>(saved.intent);
 const [storageError,setStorageError] = useState(saved.error);
 const alive=useRef(true);
 useEffect(()=>{alive.current=true;return ()=>{alive.current=false}},[]);

 const [answers, setAnswers] = useState<string[][]>(() => q.questions.map(() => []));
 const [custom, setCustom] = useState<string[]>(() => q.questions.map(() => ''));
 const [busy, setBusy] = useState(false), [message, setMessage] = useState(''), [now, setNow] = useState(Date.now());
 const locked = useRef(false);
 useEffect(() => { const timer = setInterval(() => setNow(Date.now()), 1000); return () => clearInterval(timer); }, []);
 const expired = !!q.quote && q.quote.expires_at <= now;

 const resolved = ['approved','answered','rejected','superseded','stale'].includes(q.state);
 const taskQuery = useQuery({queryKey:['project',q.project_id,'tasks',q.task_id],queryFn:({signal})=>api<Task>(q.project_id,`/api/tasks/${q.task_id}`,{signal}),enabled:q.state==='approved'&&!!q.task_id&&(q.kind==='billing'||q.kind==='delivery')&&(!!intent||!!storageError),retry:false});
 const task = !taskQuery.isError&&!taskQuery.isFetching&&taskQuery.data?.id===q.task_id ? taskQuery.data : undefined;
 const chatQuery = useQuery({queryKey:['project',q.project_id,'chat-billing',q.session_id,q.chat_message_id],queryFn:async({signal})=>{let offset=0;for(;;){const page=await api<Page<{id:string;session_id:string;state:string}>>(q.project_id,`/api/chat/sessions/${encodeURIComponent(q.session_id)}/messages?offset=${offset}&limit=100`,{signal});const found=page.items.find(m=>m.id===q.chat_message_id&&m.session_id===q.session_id);if(found)return found;if(page.coverage.exhausted)return null;if(page.coverage.next_offset<=offset)throw Error('聊天分页进度无效');offset=page.coverage.next_offset;}},enabled:q.state==='approved'&&!!q.chat_message_id,retry:false});
 const chatTerminal=!chatQuery.isError&&!chatQuery.isFetching&&!!chatQuery.data&&['done','error','cancelled','rejected'].includes(chatQuery.data.state);
 const canRenewChat=!!q.chat_message_id&&q.state==='approved'&&expired&&!chatQuery.isError&&!chatQuery.isFetching&&chatQuery.data?.state==='queued';
 const terminal = !!q.chat_message_id ? chatTerminal : !!task&&['completed','failed','cancelled'].includes(task.status)&&(q.kind==='billing'||!['adoption_pending','collecting'].includes(task.attempt_state??''));
 const canResume = q.kind==='billing'&&intent?.path===`/api/question/${q.id}/reply?session_id=${encodeURIComponent(q.session_id)}`&&JSON.parse(intent.body).expected_revision===q.revision-1&&task?.status==='accepted'&&task.attempt_state==='approved'&&task.billing?.charge_status==='not_submitted'&&!task.upstream_id&&q.task_revision!==undefined&&task.revision===q.task_revision+1;
 useEffect(() => { if (intent&&resolved&&(q.state!=='approved'||terminal||(canRenewChat&&intent.path.includes('/reply?')))) { try {if(localStorage.getItem(storageKey)===JSON.stringify(intent)){clearIntent(localStorage,storageKey,intent);setIntent(null);}}catch(e){setStorageError((e as Error).message)} } },[resolved,q.state,storageKey,intent,terminal,canRenewChat]);
 async function send(original: Intent) {
  if (locked.current || storageError) return; locked.current = true; setBusy(true); setMessage('');
  const encoded=JSON.stringify(original);
  try {
   const stored=localStorage.getItem(storageKey);
   if(stored!==null && stored!==encoded)throw new Error('已存在不同的原请求，请刷新并恢复原记录。');
   localStorage.setItem(storageKey,encoded);setIntent(original);
  }catch(e){setStorageError((e as Error).message);locked.current=false;setBusy(false);return}
  try {
   await api(q.project_id, original.path, {method:'POST', body:original.body});
   if(!alive.current||localStorage.getItem(storageKey)!==encoded)return;
   if((q.kind!=='billing'&&q.kind!=='delivery')||original.path.includes('/reject')){clearIntent(localStorage,storageKey,original);setIntent(null)}
   refresh();
  } catch (error) {
   if(!alive.current)return;
   try {
    if(localStorage.getItem(storageKey)!==encoded)return;
    if(error instanceof APIError && error.status<500){clearIntent(localStorage,storageKey,original);setIntent(null);setMessage(error.message);refresh()}
    else setMessage('结果待确认。可查询状态，或明确重发同一原请求；不会重新估价或重复生成。');
   }catch(e){setStorageError((e as Error).message)}
  } finally { locked.current = false; if(alive.current)setBusy(false); }
 }

 function decide(reject: boolean) {
  if(!canDecide(q.state,expired,busy,!!intent,!!storageError,reject))return;
  const selected = q.creative ? [[q.questions[0].options[0].label]] : q.kind === 'billing' ? [[q.questions[0].options[0].label]] : answers.map((a,i) => custom[i].trim() ? [...a,custom[i]] : a);
  void send({path:`/api/question/${q.id}/${reject ? 'reject' : 'reply'}?session_id=${encodeURIComponent(q.session_id)}`, body:JSON.stringify({requestId:newRequestID(), answers:reject ? [] : selected, expected_revision:q.revision})});
 }
 function renew() {
  void send({path:'/api/question',body:JSON.stringify({requestId:newRequestID(),kind:'billing',session_id:q.session_id,...(q.chat_message_id?{chat_message_id:q.chat_message_id,id:q.id,revision:q.revision}:{task_id:q.task_id,task_revision:q.task_revision})})});
 }
 const detailsBody = <>
  {q.creative&&<details data-artifact-version={q.creative.documentVersionId}><summary>审阅完整{({intent:'意图',script:'脚本',storyboard:'分镜'} as const)[q.creative.gate]} · revision {q.creative.revision}</summary><pre style={{whiteSpace:'pre-wrap',maxHeight:360,overflow:'auto'}}>{q.creativeContent}</pre><small>Version：{q.creative.documentVersionId} · SHA-256：{q.creative.semanticHash}</small></details>}
  {q.quote && <><p>供应商：{q.provider} · 模型：{q.model}</p><p>预计费用：{q.quote.currency} {(q.quote.estimated_amount / 10 ** q.quote.minor_unit_scale).toFixed(q.quote.minor_unit_scale)} · 积分估算：{q.quote.estimated_credits}</p><p>报价来源：{q.quote.source} · 版本：{q.quote.version}</p><p>有效期至：{new Date(q.quote.expires_at).toLocaleString()}</p></>}
  {q.quote?.basis && <p>{q.quote.basis}</p>}
  {q.impacts?.map((impact,i) => <p key={i}>{impact}</p>)}
  <fieldset disabled={busy || !!intent || !!storageError || !['pending','expired'].includes(q.state)}>
   {q.questions.map((info,n) => <div key={n}><p>{info.question}</p>{q.kind !== 'billing' && !q.creative && <>{info.options.map(option => <label key={option.label}><input type={info.multiple ? 'checkbox' : 'radio'} name={`${q.id}-${n}`} checked={answers[n]?.includes(option.label) ?? false} onChange={e => {setAnswers(previous => previous.map((a,i) => i !== n ? a : info.multiple ? e.target.checked ? [...a,option.label] : a.filter(v => v !== option.label) : [option.label]));if(!info.multiple)setCustom(previous => previous.map((a,i)=>i===n?'':a));}} />{option.label} — {option.description}</label>)}{info.custom && q.kind === 'choice' && <label>自定义回答<input value={custom[n]} maxLength={4096} onChange={e => {const value=e.target.value;setCustom(previous=>previous.map((a,i)=>i===n?value:a));if(!info.multiple)setAnswers(previous=>previous.map((a,i)=>i===n?[]:a));}} /></label>}</>}</div>)}
   <button disabled={!canDecide(q.state,expired,busy,!!intent,!!storageError,false)} onClick={() => decide(false)}>{q.creative ? q.questions[0].options[0].label : q.kind === 'billing' ? q.questions[0].options[0].label : '提交选择'}</button><button disabled={!canDecide(q.state,expired,busy,!!intent,!!storageError,true)} onClick={() => decide(true)}>拒绝</button>
  </fieldset>
 </>;
 return <DecisionFrame title={q.questions[0]?.header ?? '确认事项'} className={resolved ? 'confirmation-card is-resolved' : 'confirmation-card'} aria-label={`${q.kind === 'billing' ? '费用' : q.kind === 'storyboard' ? '分镜' : '选项'}确认`} data-question-id={q.id} data-state={q.state} data-compact={resolved ? 'true' : 'false'}>
  {resolved ? <details className="resolved-card-details"><summary>展开已处理确认详情（{q.state}）</summary>{detailsBody}</details> : detailsBody}
  {(intent||storageError) && (!resolved || q.state === 'approved') && !terminal && <div><p>{storageError?'本地确认记录无法读取；请查询确认状态并保留本地数据。':q.state === 'approved' ? q.chat_message_id?'批准回执已保存；请查询原聊天消息状态。查询不会再次发送消息或推理。':canResume?'批准回执已保存；查询确认原任务尚未发送，可手动恢复。':'批准回执已保存；请查询原任务状态。查询不会重新估价或生成。' : '存在待确认的原请求。'}</p><button disabled={busy} onClick={()=>{if(q.state==='approved'){if(q.chat_message_id)void chatQuery.refetch({cancelRefetch:false});else if(q.task_id)void taskQuery.refetch({cancelRefetch:false});}refresh()}}>查询确认状态</button>{intent&&(q.state!=='approved'||canResume||(!!q.chat_message_id&&intent.path==='/api/question'))&&<button disabled={busy || !!storageError} onClick={() => void send(intent)}>{q.state === 'approved'&&!q.chat_message_id ? '恢复已批准的原任务' : '重发原确认请求'}</button>}</div>}
  {(intent||storageError)&&q.state==='approved'&&chatQuery.error&&<p role="alert">无法查询原聊天消息状态：{chatQuery.error.message}</p>}
  {(intent||storageError)&&q.state==='approved'&&taskQuery.error&&<p role="alert">无法查询原任务状态：{taskQuery.error.message}</p>}
  {(expired || q.state === 'expired') && (!resolved||canRenewChat) && <><p role="status">报价已过期，旧确认不可使用。</p><button disabled={busy || !!intent || !!storageError} onClick={renew}>继续并重新报价</button></>}
  {resolved && <p role="status">{q.state === 'approved' ? q.creative?'此版本已批准；尚未提交生成':q.kind==='delivery'?'已批准，已记录原结果采用意图':q.chat_message_id?'已批准，已记录本轮聊天推理授权':'已批准，已记录一次生成意图' : q.state === 'rejected' ? '已拒绝' : q.state === 'answered' ? '已提交选择' : '旧卡片不可使用'}{q.reason ? `：${q.reason}` : ''}</p>}
  {q.native_send_state && <p role="status">原生回答投递：{{pending:'等待人工回执',sending:'正在投递已保存的人工回执',sent:'已送达',unknown:'结果未知；已保存的人工回执不会自动重发'}[q.native_send_state]}</p>}
  {message && <p role="alert">{message}</p>}{storageError && <p role="alert">无法安全保存确认：{storageError}</p>}
 </DecisionFrame>;
}
export function ConfirmationCards({projectID,sessionID}: {projectID: string; sessionID?: string}) {
 const client=useQueryClient();
 const [cards,setCards] = useState<Question[]>([]), [error,setError] = useState('');
 const load = useRef<() => void>(() => {});
 const waitingNative = cards.filter(q=>['answered','rejected'].includes(q.state)&&['pending','sending'].includes(q.native_send_state??'')).map(q=>q.id).join(',');
 useEffect(()=>{if(!waitingNative)return;let remaining=15;const timer=setInterval(()=>{load.current();if(--remaining===0)clearInterval(timer);},1000);return()=>clearInterval(timer);},[waitingNative,projectID,sessionID]);
 useEffect(() => {
  let alive = true, serial = 0;
  const refresh = () => {
   // Cancel even the first GET: a snapshot begun before this event cannot satisfy it.
   void client.cancelQueries({queryKey:['project',projectID,'tasks']});
   void client.invalidateQueries({queryKey:['project',projectID,'tasks']});
   void client.cancelQueries({queryKey:['project',projectID,'chat-billing']});
   void client.invalidateQueries({queryKey:['project',projectID,'chat-billing']});
   const request = ++serial; void api<{items:Question[]}>(projectID,`/api/question${sessionID === undefined ? '' : `?session_id=${encodeURIComponent(sessionID)}`}`).then(result => {if(alive && request===serial){setCards(result.items.filter(q=>q.project_id===projectID&&(sessionID===undefined||q.session_id===sessionID)));setError('');}}).catch(e=>{if(alive)setError(e.message)});
  };
  load.current = refresh;
  let unsubscribe = () => {};
  try { unsubscribe = subscribeEvents(projectID, event => {
   const part = event.payload as Part | null;
   if(event.type==='chat_message'&&part?.part_type==='text'&&part.type==='content_delta'&&typeof part.content_delta==='string'&&!part.call_id&&!part.tool&&!part.state&&part.tool_input===undefined&&part.tool_output===undefined)return;
   if(event.type.startsWith('question_')||event.type==='task_changed'||event.type==='chat_message'||event.type==='chat_done')refresh();
  },refresh,sessionID); } catch { setError('无法保存重连游标，请刷新确认状态。'); }
  refresh();
  return () => { alive=false; unsubscribe(); };
 },[projectID,sessionID,client]);
 return <aside className="confirmation-cards" aria-label="待确认事项">{error && <p role="alert">{error}</p>}{cards.map(q=><ConfirmationCard key={q.id} question={q} refresh={()=>load.current()} />)}{(error||cards.some(q=>q.native_send_state&&q.native_send_state!=='sent')) && <button onClick={()=>load.current()}>刷新确认状态</button>}</aside>;
}
