import type {CanvasDocument,Command} from '../canvas/canvas.ts';
export type Task = {id:string;request_id:string;revision:number;prompt:string;status:string;actions:string[];progress:{phase:string;percent:number|null};problem?:{source:string;code:string;message:string;upstream?:{code:string;message?:string}};outputs:{output_key:string;state:string;resource_id:string;composition_id:string;version_id?:string;asset_id?:string}[]};
export type Intent={path:string;body:string};
export const generationKey=(p:string)=>`moviedesign.generation.v1.${p}`;
export function readControlIntent(raw:string|null,kind:'generation'|'resolve'|'mask',owner=''):Intent|null{
 if(raw===null)return null;
 const value=JSON.parse(raw),id=(v:unknown)=>typeof v==='string'&&/^[A-Za-z0-9_-]{1,128}$/.test(v);
 if(!value||typeof value!=='object'||Object.keys(value).sort().join(',')!=='body,path'||typeof value.body!=='string')throw Error('控制请求恢复记录损坏');
 const b=JSON.parse(value.body);if(!b||typeof b!=='object'||!id(b.requestId))throw Error('原请求身份损坏');
 let valid=false;
 if(kind==='resolve')valid=value.path===`/api/tasks/${owner}/resolve`&&Object.keys(b).sort().join(',')==='action,expected_revision,requestId'&&['cancel','retry'].includes(b.action)&&Number.isSafeInteger(b.expected_revision)&&b.expected_revision>0;
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
