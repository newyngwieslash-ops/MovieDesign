import {GenerationComposer,type Generation} from '../tasks/GenerationComposer';
import {useTasks} from '../tasks/TaskCenter';
import {subscribeEvents} from '../../events';
import {useCallback,useEffect,useRef,useState,type PointerEvent} from 'react';
import {useQuery,useQueryClient} from '@tanstack/react-query';
import {allPages,api,APIError,newRequestID} from '../../gateway';
import {CanvasNode,type CanvasAsset,type CanvasVersion} from './CanvasNode';
import {emptyDocument,nextGenerationPositions,patch,pendingKey,readPending,visibleNodes,worldPosition,type CanvasDocument,type Command,type Content,type Node,type Pending,type Receipt,type View} from './canvas';

export function CanvasWorkspace({projectID,onEdit}:{projectID?:string;onEdit?:(id:string)=>void}) {
 if(!projectID)return <section className="canvas-workspace" aria-label="空创作画布"><div className="canvas-empty"><span className="eyebrow">A SPACE FOR YOUR NEXT IDEA</span><h2>留白，也是创作的开始</h2><p>创建或选择项目，开始整理你的创作。</p></div></section>;
 return <PersistentCanvas key={projectID} projectID={projectID} onEdit={onEdit??(()=>{})}/>;
}
function PersistentCanvas({projectID,onEdit}:{projectID:string;onEdit:(id:string)=>void}) {
 const queryClient=useQueryClient();
 const [document,setDocument]=useState<CanvasDocument>(emptyDocument),current=useRef(document);
 const [ready,setReady]=useState(false),[error,setError]=useState(''),[busy,setBusy]=useState(false),lock=useRef(false),alive=useRef(true);
 const [pending,setPending]=useState<Pending|null>(null),[storageError,setStorageError]=useState('');
 const [selected,setSelected]=useState(''),[choice,setChoice]=useState(''),[undo,setUndo]=useState<Command|null>(null),[redo,setRedo]=useState<Command|null>(null);
 const [view,setView]=useState<View>({x:30,y:30,scale:1}),[extent,setExtent]=useState({width:1000,height:600}),surface=useRef<HTMLDivElement>(null);
 const [preview,setPreview]=useState<Node|null>(null),gesture=useRef<{pointer:number;startX:number;startY:number;node?:Node;resize:boolean;view:View;next?:Node}|null>(null);
 const tasks=useTasks(projectID);
 const assets=useQuery({queryKey:['project',projectID,'assets','all',200],queryFn:({signal})=>allPages<CanvasAsset>(projectID,'/api/assets',signal)});
 const versions=useQuery({queryKey:['project',projectID,'versions','all',200],queryFn:({signal})=>allPages<CanvasVersion>(projectID,'/api/versions',signal)});
 const resources=useQuery({queryKey:['project',projectID,'resources','all',200],queryFn:({signal})=>allPages<{id:string;composition_id:string}>(projectID,'/api/resources',signal)});
 const compositions=useQuery({queryKey:['project',projectID,'compositions','all',200],queryFn:({signal})=>allPages<{id:string;name:string}>(projectID,'/api/compositions',signal)});
 const accept=useCallback((next:CanvasDocument)=>{if(alive.current&&next.revision>=current.current.revision){current.current=next;setDocument(next)}},[]);
 const refresh=useCallback(async()=>{const d=await api<CanvasDocument>(projectID,'/api/canvas/nodes');accept(d);if(alive.current)setReady(true)},[accept,projectID]);
 useEffect(()=>{const load=()=>{void refresh().catch(e=>{if(alive.current)setError(e.message)});void queryClient.invalidateQueries({queryKey:['project',projectID]})};try{return subscribeEvents(projectID,event=>{if(event.type==='canvas_changed')load()},load)}catch(e){setError((e as Error).message)}},[projectID,refresh,queryClient]);
 useEffect(()=>{alive.current=true;try{setPending(readPending(localStorage.getItem(pendingKey(projectID)),projectID));const raw=localStorage.getItem(`moviedesign.canvas.view.${projectID}`);if(raw){const v=JSON.parse(raw);if([v.x,v.y,v.scale].every(Number.isFinite)&&v.scale>=.1&&v.scale<=4)setView(v)}}catch(e){setStorageError((e as Error).message)}void refresh().catch(e=>{if(alive.current)setError(e.message)});return()=>{alive.current=false;gesture.current=null}},[projectID,refresh]);
 useEffect(()=>{const element=surface.current;if(!element)return;const resize=new ResizeObserver(([entry])=>setExtent({width:entry.contentRect.width,height:entry.contentRect.height}));resize.observe(element);return()=>resize.disconnect()},[]);
 useEffect(()=>{try{localStorage.setItem(`moviedesign.canvas.view.${projectID}`,JSON.stringify(view))}catch(e){setError((e as Error).message)}},[projectID,view]);
 async function send(p:Pending) {
  if(lock.current||storageError)return false;lock.current=true;setBusy(true);setError('');
  try {
   if(localStorage.getItem(pendingKey(projectID))!==JSON.stringify(p))throw Error('画布待确认记录已变化，请刷新窗口后继续');
   const receipt=await api<Receipt>(projectID,'/api/canvas/commands',{method:'POST',body:p.body});
   if(!alive.current)return;
   if(localStorage.getItem(pendingKey(projectID))!==JSON.stringify(p))throw Error('画布记录已变化，保留当前恢复记录');
   accept(receipt.document);localStorage.removeItem(pendingKey(projectID));setPending(null);
   if(p.mode==='undo'){setRedo(receipt.effects.undo);setUndo(null)}else{setUndo(p.mode?receipt.effects.undo:null);setRedo(null)}
   return true;
  }catch(e){if(!alive.current)return;setError((e as Error).message);if(e instanceof APIError&&e.status>=400&&e.status<500){try{if(localStorage.getItem(pendingKey(projectID))===JSON.stringify(p)){localStorage.removeItem(pendingKey(projectID));setPending(null)}await refresh();setUndo(null);setRedo(null);setError(`${e.message}。命令已拒绝；已刷新，请重新编辑。`)}catch(refreshError){setError((refreshError as Error).message)}}}
  finally{lock.current=false;if(alive.current)setBusy(false)}
 }
 function commit(command:Command,mode:NonNullable<Pending['mode']>='normal') {
  if(!ready||pending||lock.current||storageError)return;
  try{if(localStorage.getItem(pendingKey(projectID))!==null)throw Error('已有待确认画布命令，请刷新后继续');const p:Pending={schema:1,projectID,mode,body:JSON.stringify({requestId:newRequestID(),expectedRevision:current.current.revision,command})};localStorage.setItem(pendingKey(projectID),JSON.stringify(p));setPending(p);void send(p)}catch(e){setError((e as Error).message)}
 }
 async function prepareGeneration(input:Generation){
  const existing=Object.values(current.current.nodes).filter(n=>n.pendingGeneration?.requestId===input.requestId);
  if(existing.length===input.output.resources.length)return;
  if(!ready||storageError)throw Error('画布尚未就绪');
  // Admission outlives its pending nodes, including conversion and user deletion.
  if((await tasks.refetch({throwOnError:true})).data?.some(t=>t.request_id===input.requestId))return;
  if(!alive.current)throw Error('项目已切换，保留原请求');
  const saved=readPending(localStorage.getItem(pendingKey(projectID)),projectID);
  if(saved){if(JSON.parse(saved.body).requestId!==`generation-${input.requestId}`)throw Error('请先确认原画布命令');if(!await send(saved))throw Error('画布结果待确认');return}
  const positions=nextGenerationPositions(current.current,input.output.resources.length);const nodes=input.output.resources.map((r,i)=>{const after:Node={id:`generation-${input.requestId}-${i}`,kind:'placeholder',expectedKind:'image',draft:null,pendingGeneration:{requestId:input.requestId,outputKey:r.key},parentId:null,position:positions[i],size:{width:350,height:350},zIndex:Object.keys(current.current.nodes).length+i+1,hidden:false};return {id:after.id,before:null,after}});
  const p:Pending={schema:1,projectID,body:JSON.stringify({requestId:`generation-${input.requestId}`,expectedRevision:current.current.revision,command:{type:'canvas.patch',nodes,edges:[]}})};
  localStorage.setItem(pendingKey(projectID),JSON.stringify(p));setPending(p);if(!await send(p))throw Error('画布结果待确认');
 }
 useEffect(()=>{if(tasks.data)void refresh().catch(e=>{if(alive.current)setError(e.message)})},[tasks.data,refresh]);
 const rendered=preview?{...document,nodes:{...document.nodes,[preview.id]:preview}}:document;
 const visible=visibleNodes(rendered,view,extent.width,extent.height);
 const compositionIDs=[...new Set(visible.flatMap(n=>n.kind==='composition'?[n.compositionId]:[]))].sort();
 const content=useQuery({queryKey:['project',projectID,'canvas-content',compositionIDs,document.revision,versions.dataUpdatedAt,resources.dataUpdatedAt,compositions.dataUpdatedAt],queryFn:async({signal})=>{const items:Content[]=[];for(let i=0;i<compositionIDs.length;i+=200){const page=await api<{items:Content[]}>(projectID,'/api/canvas/content',{method:'POST',body:JSON.stringify({compositionIds:compositionIDs.slice(i,i+200)}),signal});items.push(...page.items)}return items;},enabled:compositionIDs.length>0});
 const disabled=!ready||busy||!!pending||!!storageError;
 const node=document.nodes[selected];
 function add(kind:'frame'|'placeholder'|'composition') {
  const [pos]=nextGenerationPositions(document,1,{x:(40-view.x)/view.scale,y:(50-view.y)/view.scale});const base={id:newRequestID(),parentId:null,position:pos,size:{width:kind==='frame'?500:350,height:kind==='frame'?400:350},zIndex:Object.values(document.nodes).reduce((m,n)=>Math.max(m,n.zIndex),0)+1,hidden:false};
  let next:Node;
  if(kind==='frame')next={...base,kind,title:'画框',collapsed:false};else if(kind==='placeholder')next={...base,kind,expectedKind:'image',draft:null};else{const version=versions.data?.find(v=>v.id===choice),resource=resources.data?.find(r=>r.id===version?.resource_id);if(!version||!resource){setError('请先导入素材并选择一个版本');return;}next={...base,kind,compositionId:resource.composition_id,activeItem:{type:'version',resourceId:resource.id,versionId:version.id},draft:null};}
  setSelected(next.id);commit(patch(null,next));
 }
 function start(e:PointerEvent,n?:Node,resize=false) {
  if(e.button!==0&&e.button!==1||disabled)return;e.preventDefault();e.stopPropagation();surface.current?.setPointerCapture(e.pointerId);gesture.current={pointer:e.pointerId,startX:e.clientX,startY:e.clientY,node:n,resize,view};if(n)setSelected(n.id);else setSelected('');
 }
 function move(e:PointerEvent) {const g=gesture.current;if(!g||g.pointer!==e.pointerId)return;const dx=e.clientX-g.startX,dy=e.clientY-g.startY;if(!g.node){setView({...g.view,x:g.view.x+dx,y:g.view.y+dy});return;}g.next=g.resize?{...g.node,size:{width:Math.max(100,g.node.size.width+dx/view.scale),height:Math.max(80,g.node.size.height+dy/view.scale)}}:{...g.node,position:{x:g.node.position.x+dx/view.scale,y:g.node.position.y+dy/view.scale}};setPreview(g.next)}
 function finish(cancel=false){const g=gesture.current;gesture.current=null;setPreview(null);if(cancel){if(g&&!g.node)setView(g.view);return;}if(g?.node&&g.next&&JSON.stringify(g.node)!==JSON.stringify(g.next))commit(patch(g.node,g.next))}
 function update(next:Node){if(node&&JSON.stringify(node)!==JSON.stringify(next))commit(patch(node,next))}
 function remove(){if(!node)return;const ids=new Set([node.id]);let changed=true;while(changed){changed=false;for(const n of Object.values(document.nodes))if(n.parentId&&ids.has(n.parentId)&&!ids.has(n.id)){ids.add(n.id);changed=true}}commit({type:'canvas.patch',nodes:[...ids].map(id=>({id,before:document.nodes[id],after:null})),edges:Object.values(document.edges).filter(e=>ids.has(e.source)||ids.has(e.target)).map(e=>({id:e.id,before:e,after:null}))});setSelected('')}
 function reparent(parentId:string|null){if(!node)return;const world=worldPosition(document,node),parent=parentId?worldPosition(document,document.nodes[parentId]):{x:0,y:0};update({...node,parentId,position:{x:world.x-parent.x,y:world.y-parent.y}})}
 return <section className="persistent-canvas" aria-label="持久创作画布" data-revision={document.revision}>
  <GenerationComposer projectID={projectID} prepare={prepareGeneration}/>
  <div className="canvas-toolbar"><h2>创作画布</h2><select aria-label="添加对象版本" value={choice} onChange={e=>setChoice(e.target.value)}><option value="">选择已导入版本</option>{versions.data?.map(v=><option key={v.id} value={v.id}>{assets.data?.find(a=>a.id===v.asset_id)?.name??v.id}</option>)}</select><button disabled={disabled||!choice} onClick={()=>add('composition')}>添加对象</button><button disabled={disabled} onClick={()=>add('frame')}>添加画框</button><button disabled={disabled} onClick={()=>add('placeholder')}>添加占位</button><button disabled={disabled||!undo} onClick={()=>undo&&commit(undo,'undo')}>撤销画布</button><button disabled={disabled||!redo} onClick={()=>redo&&commit(redo,'redo')}>重做画布</button><button onClick={()=>setView({x:30,y:30,scale:1})}>重置视图</button><span>{Math.round(view.scale*100)}%</span></div>
  {pending&&<div role="status" className="pending-import">画布结果待确认 · 保留原命令与请求号 <button disabled={busy||!!storageError} onClick={()=>void send(pending)}>继续确认画布</button></div>}
  {(error||storageError||content.error||assets.error||versions.error||resources.error||compositions.error)&&<p role="alert">{storageError||error||content.error?.message||assets.error?.message||versions.error?.message||resources.error?.message||compositions.error?.message}</p>}
  <div className="canvas-layout"><div ref={surface} className="object-surface" tabIndex={0} aria-label="画布视口" onPointerDown={e=>start(e)} onPointerMove={move} onPointerUp={()=>finish()} onPointerCancel={()=>finish(true)} onLostPointerCapture={()=>finish(true)} onKeyDown={e=>{if(e.target!==e.currentTarget)return;if(e.key==='Escape')finish(true);if(e.key==='Delete'&&!disabled)remove()}} onWheel={e=>{const box=e.currentTarget.getBoundingClientRect(),x=e.clientX-box.left,y=e.clientY-box.top;const scale=Math.max(.1,Math.min(4,view.scale*Math.exp(-e.deltaY*.001)));setView({x:x-(x-view.x)*scale/view.scale,y:y-(y-view.y)*scale/view.scale,scale})}}>
   <div className="object-world" style={{transform:`translate(${view.x}px,${view.y}px) scale(${view.scale})`}}>
    {visible.map(n=>{const vid=n.kind==='composition'&&n.activeItem?.type==='version'?n.activeItem.versionId:null;const asset=assets.data?.find(a=>a.id===versions.data?.find(v=>v.id===vid)?.asset_id);return <CanvasNode key={n.id} node={n} position={worldPosition(rendered,n)} selected={n.id===selected} asset={asset} content={n.kind==='composition'?content.data?.find(c=>c.compositionId===n.compositionId):undefined} projectID={projectID} onSelect={()=>setSelected(n.id)} onDrag={(e,resize)=>start(e,document.nodes[n.id],resize)} onEdit={onEdit}/>})}
   </div><span className="canvas-count">显示 {visible.length} / {Object.keys(document.nodes).length} 个节点 · 拖动空白平移，滚轮缩放</span>
  </div><aside className="canvas-properties" aria-label="节点属性">{node?<fieldset disabled={disabled}><legend>节点属性</legend><label>画框<select value={node.parentId??''} onChange={e=>reparent(e.target.value||null)}><option value="">无</option>{Object.values(document.nodes).filter(n=>n.kind==='frame'&&n.id!==node.id).map(n=><option key={n.id} value={n.id}>{n.kind==='frame'?n.title:n.id}</option>)}</select></label>
   {(['x','y'] as const).map(k=><label key={k}>{({x:"水平位置",y:"垂直位置",width:"宽度",height:"高度"})[k]}<input aria-label={`节点 ${k}`} key={`${node.id}-${node.position[k]}`} type="number" defaultValue={node.position[k]} onBlur={e=>{if(e.target.value!==''&&Number.isFinite(e.target.valueAsNumber))update({...node,position:{...node.position,[k]:e.target.valueAsNumber}})}}/></label>)}
   {(['width','height'] as const).map(k=><label key={k}>{({x:"水平位置",y:"垂直位置",width:"宽度",height:"高度"})[k]}<input aria-label={`节点 ${k}`} key={`${node.id}-${node.size[k]}`} type="number" min="1" defaultValue={node.size[k]} onBlur={e=>{if(e.target.valueAsNumber>0)update({...node,size:{...node.size,[k]:e.target.valueAsNumber}})}}/></label>)}
   <label>层级<input aria-label="节点层级" key={`${node.id}-${node.zIndex}`} type="number" defaultValue={node.zIndex} onBlur={e=>{if(Number.isSafeInteger(e.target.valueAsNumber))update({...node,zIndex:e.target.valueAsNumber})}}/></label>
   {node.kind==='frame'&&<><label>标题<input aria-label="画框标题" key={`${node.id}-${node.title}`} defaultValue={node.title} maxLength={4096} onBlur={e=>update({...node,title:e.target.value})}/></label><label><input type="checkbox" checked={node.collapsed} onChange={e=>update({...node,collapsed:e.target.checked})}/>折叠画框</label><label>颜色<select value={node.backgroundColor??''} onChange={e=>{const next={...node};if(e.target.value)next.backgroundColor=e.target.value;else delete next.backgroundColor;update(next)}}><option value="">默认</option>{['red','orange','yellow','green','cyan','blue','purple'].map(c=><option key={c}>{c}</option>)}</select></label></>}
   {node.kind==='composition'&&<label>采用版本<select aria-label="画布采用版本" value={node.activeItem?.type==='version'?node.activeItem.versionId:''} onChange={e=>{const v=versions.data?.find(v=>v.id===e.target.value);if(v)update({...node,activeItem:{type:'version',resourceId:v.resource_id,versionId:v.id}})}}><option value="">无</option>{Object.values(content.data?.find(c=>c.compositionId===node.compositionId)?.resources??{}).flatMap(r=>r.versions.map(v=><option key={v.versionId} value={v.versionId}>{r.name} · v{v.ordinal}</option>))}</select></label>}
   <button onClick={remove}>移除节点及子节点</button></fieldset>:<p>选择节点查看属性。素材导入后可添加到画布。</p>}</aside></div>
 </section>;
}
