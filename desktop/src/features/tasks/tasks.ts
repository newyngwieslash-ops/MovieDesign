import type {CanvasDocument,Command} from '../canvas/canvas.ts';
export type TaskRecovery = {stage:'parameter'|'semantic'|'upstream'|'unknown'|string;recommendedAction:string;selectedAction?:string;targetStage?:string;receiptId?:string;requestId?:string;fromTaskId:string;fromContractId?:string;pipelineId?:string;storyboardVersionId?:string;promptVersionId?:string;updatedAt?:number};
export type TaskProvider = {id:string;name?:string;kind?:string;protocol?:string;model_id?:string;remote_cancel_stages?:string[];remote_cancel_summary?:string};
export type TaskBilling = {currency?:string;estimated_amount?:number|null;minor_unit_scale?:number|null;estimated_credits?:number|null;actual_amount?:number|null;actual_credits?:number|null;charge_status?:string;billing_note?:string};
export type Task = {id:string;request_id:string;capability_id?:string;revision:number;attempt?:number;attempt_state?:string;upstream_id?:string;provider?:TaskProvider;billing?:TaskBilling;contract_id?:string;prompt:string;status:string;actions:string[];progress:{phase:string;percent:number|null};problem?:{source:string;code:string;message:string;upstream?:{code:string;message?:string}};recovery?:TaskRecovery;outputs:{output_key:string;state:string;resource_id:string;composition_id:string;version_id?:string;asset_id?:string}[]};
export type Intent={path:string;body:string};
export const generationKey=(p:string)=>`moviedesign.generation.v1.${p}`;
export function formatTaskBilling(b?:TaskBilling):string{
 if(!b)return '预计费用：未配置定价 · 实际费用：待确认';
 const scale=b.minor_unit_scale??2,cur=b.currency||'CNY';
 const est=typeof b.estimated_amount==='number'?`${cur} ${(b.estimated_amount/10**scale).toFixed(scale)}`:'未报价';
 const estCr=typeof b.estimated_credits==='number'?`（积分 ${b.estimated_credits}）`:'';
 const act=typeof b.actual_amount==='number'?`${cur} ${(b.actual_amount/10**scale).toFixed(scale)}${typeof b.actual_credits==='number'?`（积分 ${b.actual_credits}）`:''}`:'供应商接口未返回实时扣费，以账单结算为准';
 return `预计费用：${est}${estCr} · 实际费用：${act}${b.billing_note?` · ${b.billing_note}`:''}`;
}
export function describeTaskCancellation(task:Task):string{
 const stages=task.provider?.remote_cancel_stages??[];
 const summary=task.provider?.remote_cancel_summary||(stages.includes('queued')?'仅远端排队阶段（queued）支持 DELETE 取消；running 阶段远端不可中断':'同步请求发出后不支持远端中断；取消仅停止本地接收与投递');
 if(task.status!=='cancelled')return `取消边界：${summary}`;
 if(task.billing?.charge_status==='not_submitted')return `已在发送前本地取消（未调用供应商，实际费用 0）。${summary}`;
 if(task.billing?.charge_status==='remote_cancelled_queued'||task.problem?.code==='TASK_CANCELLED_QUEUED')return `已在远端排队阶段（queued）成功发送 DELETE 取消（实际费用 0）。${summary}`;
 return `本地停止自动投递；远端已进入执行阶段不可中断，供应商可能继续处理并收费；晚到结果保留待人工确认。${summary}`;
}
export function readControlIntent(raw:string|null,kind:'generation'|'resolve'|'mask',owner=''):Intent|null{
 if(raw===null)return null;
 const value=JSON.parse(raw),id=(v:unknown)=>typeof v==='string'&&/^[A-Za-z0-9_-]{1,128}$/.test(v);
 if(!value||typeof value!=='object'||Object.keys(value).sort().join(',')!=='body,path'||typeof value.body!=='string')throw Error('控制请求恢复记录损坏');
 const b=JSON.parse(value.body);if(!b||typeof b!=='object'||!id(b.requestId))throw Error('原请求身份损坏');
 let valid=false;
 if(kind==='resolve')valid=(value.path===`/api/tasks/${owner}/resolve`&&Object.keys(b).sort().join(',')==='action,expected_revision,requestId'&&['cancel','retry','query','modify_params','modify_storyboard'].includes(b.action)&&Number.isSafeInteger(b.expected_revision)&&b.expected_revision>0)||(value.path===`/api/tasks/${owner}/collection/retry`&&Object.keys(b).sort().join(',')==='expected_revision,requestId'&&Number.isSafeInteger(b.expected_revision)&&b.expected_revision>0);
 if(kind==='mask')valid=value.path==='/api/generate/mask'&&Object.keys(b).sort().join(',')==='asset_id,bounds,requestId'&&b.asset_id===owner&&Array.isArray(b.bounds)&&b.bounds.length>0&&b.bounds.every((r:Record<string,unknown>)=>r&&Object.keys(r).sort().join(',')==='height,width,x,y'&&Object.values(r).every(Number.isFinite));
 if(kind==='generation'){
  const o=b.output,c=o?.composition;valid=value.path==='/api/generate/image'&&Object.keys(b).sort().join(',')==='capability_id,count,output,params,prompt,references,requestId'&&typeof b.capability_id==='string'&&b.capability_id.startsWith('image:')&&typeof b.prompt==='string'&&b.params&&typeof b.params==='object'&&!Array.isArray(b.params)&&Object.values(b.params).every(v=>v===null||['string','boolean','number'].includes(typeof v))&&Array.isArray(b.references)&&b.references.every(id)&&Number.isSafeInteger(b.count)&&b.count>=1&&b.count<=10&&o&&Object.keys(o).sort().join(',')==='composition,resources'&&Array.isArray(o.resources)&&o.resources.length===b.count&&o.resources.every((r:Record<string,unknown>)=>r&&id(r.key)&&typeof r.name==='string'&&['create','new_version'].includes(String(r.mode))&&Object.keys(r).sort().join(',')===(r.mode==='create'?'key,mode,name':'baseVersionId,key,mode,name')&&(r.mode!=='new_version'||id(r.baseVersionId)))&&c&&Object.keys(c).sort().join(',')==='name,placements,type'&&typeof c.name==='string'&&c.type&&Object.keys(c.type).length===1&&((id(c.type.id))||(typeof c.type.label==='string'&&c.type.label.length>0))&&Array.isArray(c.placements)&&c.placements.length===b.count&&c.placements.every((r:Record<string,unknown>,i:number)=>r&&Object.keys(r).sort().join(',')==='key,role'&&r.key===o.resources[i].key&&typeof r.role==='string');
 }
 if(!valid)throw Error('原请求内容与当前作用域不匹配');return value;
}
export function clearControlIntent(storage:Pick<Storage,'getItem'|'removeItem'>,key:string,intent:Intent):boolean{if(storage.getItem(key)!==JSON.stringify(intent))return false;storage.removeItem(key);return true}
export async function sendIntent(storage:Pick<Storage,'getItem'|'setItem'>,key:string,intent:Intent,send:(intent:Intent)=>Promise<unknown>):Promise<unknown>{
 const encoded=JSON.stringify(intent),existing=storage.getItem(key);if(existing!==null&&existing!==encoded)throw Error('存在不同的待确认原请求');storage.setItem(key,encoded);return send(intent);
}
export function completedPatch(doc:CanvasDocument,tasks:Task[]):Command|null{
 const nodes:Extract<Command,{type:'canvas.patch'}>['nodes']=[];
 for(const node of Object.values(doc.nodes)){
  if(node.kind==='frame')continue;
  const pending=node.pendingGeneration;
  const task=tasks.find(t=>(t.status==='completed'||t.status==='cancelled')&&(pending?t.request_id===pending.requestId:node.kind==='composition'&&node.activeItem?.type==='task-output'&&node.activeItem.taskId===t.id));
  const output=task?.outputs.find(o=>o.state==='delivered'&&o.version_id&&o.output_key===(pending?.outputKey??(node.kind==='composition'&&node.activeItem?.type==='task-output'?node.activeItem.outputKey:'')));
  if(!output||node.kind==='composition'&&node.activeItem?.type==='version')continue;
  const {pendingGeneration:_,...base}=node;if(base.kind==='placeholder')delete (base as Partial<typeof base>).expectedKind;nodes.push({id:node.id,before:node,after:{...base,kind:'composition',compositionId:output.composition_id,activeItem:{type:'version',resourceId:output.resource_id,versionId:output.version_id!},draft:null}});
 }
 return nodes.length?{type:'canvas.patch',nodes,edges:[]}:null;
}
export function canDecide(state:string,expired:boolean,busy:boolean,saved:boolean,storageError:boolean,reject:boolean):boolean{return (state==='pending'||reject&&state==='expired')&&(!expired||reject)&&!busy&&!saved&&!storageError}
