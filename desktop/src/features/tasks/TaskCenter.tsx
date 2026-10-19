import {useEffect,useRef,useState} from 'react';
import {useQuery,useQueryClient} from '@tanstack/react-query';
import {api,APIError,newRequestID,allPages} from '../../gateway';
import {subscribeEvents} from '../../events';
import {sendIntent,readControlIntent,clearControlIntent,type Task,type Intent} from './tasks';

const statuses:Record<string,string>={accepted:'等待处理',running:'进行中',waiting_user:'等待确认',unknown:'供应商结果未知',completed:'已完成',failed:'未完成',cancelled:'本地已取消'};
const phases:Record<string,string>={pending:'排队',active:'执行',collecting:'收集并归档',paused:'暂停',done:'完成',stopped:'停止'};
const stageNames:Record<string,string>={parameter:'参数错误 (parameter)',semantic:'语义/创意冲突 (semantic)',upstream:'上游临时故障 (upstream)',unknown:'受理状态未知 (unknown)'};
const actionNames:Record<string,string>={modify_params:'修改参数',modify_storyboard:'修改分镜',retry:'原样重试',query:'继续查询',cancel:'取消任务'};
export function useTasks(projectID:string){return useQuery({queryKey:['project',projectID,'tasks'],queryFn:({signal})=>allPages<Task>(projectID,'/api/tasks',signal)})}
export function TaskCenter({projectID}:{projectID:string}){
 const client=useQueryClient(),tasks=useTasks(projectID),[error,setError]=useState('');
 useEffect(()=>{const refresh=()=>{void client.invalidateQueries({queryKey:['project',projectID]})};try{return subscribeEvents(projectID,e=>{if(e.type==='task_changed')refresh()},refresh)}catch(e){setError((e as Error).message)}},[projectID,client]);
 return <section className="task-center" aria-label="任务中心"><h2>图像任务</h2>{(error||tasks.error)&&<p role="alert">{error||tasks.error?.message}</p>}<button onClick={()=>void tasks.refetch()}>刷新任务状态</button>{tasks.data?.map(task=><TaskRow key={task.id} task={task} projectID={projectID} refresh={()=>void tasks.refetch()}/>)}</section>;
}
function TaskRow({task,projectID,refresh}:{task:Task;projectID:string;refresh:()=>void}){
 const key=`moviedesign.task.resolve.v1.${projectID}.${task.id}`;
 const [saved]=useState(()=>{try{const intent=readControlIntent(localStorage.getItem(key),'resolve',task.id);return {intent,error:''}}catch(e){return {intent:null,error:(e as Error).message}}});
 const [intent,setIntent]=useState<Intent|null>(saved.intent),[error,setError]=useState(saved.error),[busy,setBusy]=useState(false),lock=useRef(false),alive=useRef(true);
 useEffect(()=>{alive.current=true;return()=>{alive.current=false}},[]);
 async function send(original:Intent){if(lock.current||saved.error)return;lock.current=true;setBusy(true);try{await sendIntent(localStorage,key,original,async i=>{setIntent(i);return api(projectID,i.path,{method:'POST',body:i.body})});if(!alive.current)return;if(clearControlIntent(localStorage,key,original))setIntent(null);setError('');refresh()}catch(e){if(!alive.current)return;setError((e as Error).message);if(e instanceof APIError&&e.status<500){try{if(clearControlIntent(localStorage,key,original)){setIntent(null);refresh()}}catch(cleanup){setError((cleanup as Error).message)}}}finally{lock.current=false;if(alive.current)setBusy(false)}}
 const label=(action:string)=>action==='retry'&&task.problem?.source==='delivery'?'继续收集原结果':(actionNames[action]??action);
 return <article data-task-id={task.id} data-recovery-stage={task.recovery?.stage??''}><h3>{statuses[task.status]??task.status}</h3><p>{task.prompt}</p><p>{phases[task.progress.phase]??task.progress.phase}{task.progress.percent!==null?` · ${task.progress.percent}%`:''}</p>{task.problem&&<p role="status">{task.problem.message}{task.problem.upstream&&` · ${task.problem.upstream.code}: ${task.problem.upstream.message??''}`}</p>}{task.recovery&&<div className="task-recovery"><p>失败回流分类：{stageNames[task.recovery.stage]??task.recovery.stage} · 建议回流：{actionNames[task.recovery.recommendedAction]??task.recovery.recommendedAction}</p><p>操作差异说明：「修改参数」保留当前分镜 revision 与批准并在 Prompt 审阅卡编译新合同；「修改分镜」回流至分镜审阅生成新 revision 并使旧批准失效；「原样重试」仅重放冻结输入并需重新确认费用；「继续查询」只读对账上游状态且绝不重发生成。聊天回复不构成执行授权。</p>{task.recovery.selectedAction&&<p data-recovery-receipt={task.recovery.receiptId??''}>已记录回流：{actionNames[task.recovery.selectedAction]??task.recovery.selectedAction} → 阶段 {task.recovery.targetStage??task.recovery.stage} · 原任务 {task.recovery.fromTaskId}{task.recovery.fromContractId?` · 原合同 ${task.recovery.fromContractId}`:''}{task.recovery.receiptId?` · 回执 ${task.recovery.receiptId}`:''}</p>}</div>}{task.status==='unknown'&&<p>尚不能确认供应商是否已收费；查询状态不会重新生成。</p>}{task.status==='cancelled'&&<p>本地停止投递。供应商可能继续处理并收费；晚到结果保留待对账。</p>}{task.actions.filter(a=>['cancel','retry','query','modify_params','modify_storyboard'].includes(a)).map(action=><button key={action} disabled={busy||!!intent||!!saved.error} onClick={()=>void send({path:`/api/tasks/${task.id}/resolve`,body:JSON.stringify({requestId:newRequestID(),action,expected_revision:task.revision})})}>{label(action)}</button>)}{intent&&<button disabled={busy||!!saved.error} onClick={()=>void send(intent)}>确认原任务操作</button>}{error&&<p role="alert">{error}</p>}</article>;
}
