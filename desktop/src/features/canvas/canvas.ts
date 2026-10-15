export type ItemRef = {type:'version';resourceId:string;versionId:string}|{type:'task-output';taskId:string;outputKey:string};
export type PendingGeneration = {requestId:string;outputKey:string};
export type Point = {x:number;y:number};
export type View = Point & {scale:number};
export type Node = {id:string;parentId:string|null;position:Point;size:{width:number;height:number};zIndex:number;hidden:boolean} & ({kind:'frame';title:string;collapsed:boolean;backgroundColor?:string}|{kind:'composition';compositionId:string;activeItem:ItemRef|null;draft:null}|{kind:'placeholder';expectedKind:string;draft:null});
export type Edge = {id:string;source:string;target:string;sourceResourceId?:string};
export type CanvasDocument = {schemaVersion:1;revision:number;nodes:Record<string,Node>;edges:Record<string,Edge>};
export type Change<T> = {id:string;before:T|null;after:T|null};
export type Command = {type:'canvas.patch';nodes:Change<Node>[];edges:Change<Edge>[]}|{type:'canvas.batch';commands:Command[]};
export type Receipt = {requestId:string;appliedRevision:number;document:CanvasDocument;replayed:boolean;effects:{undo:Command|null;compositionIds:string[]}};
export type Pending = {schema:1;projectID:string;body:string};
export type Content = {compositionId:string;state:'missing'|'deleted'|'ready';revision?:number;title?:string;resources?:Record<string,{resourceId:string;name:string;kind:string;currentVersionId:string|null;availability:string;versions:{versionId:string;ordinal:number;availability:string}[]}>};
export const CANVAS_LIMITS = {nodes:10000,edges:20000,changes:30000,batch:64,content:200,references:64} as const;
export const emptyDocument = ():CanvasDocument => ({schemaVersion:1,revision:0,nodes:{},edges:{}});
export const pendingKey = (project:string) => `moviedesign.canvas.pending.v1.${project}`;
export function parseItemRef(value:unknown):ItemRef {
 if(!value||typeof value!=='object')throw Error('Invalid item ref');
 const r=value as Record<string,unknown>, keys=r.type==='version'?['type','resourceId','versionId']:r.type==='task-output'?['type','taskId','outputKey']:[];
 if(keys.length!==3||Object.keys(r).length!==3||keys.some(k=>typeof r[k]!=='string'||!(r[k] as string).trim()||(r[k] as string).length>256))throw Error('Invalid item ref');
 return value as ItemRef;
}
export function readPending(raw:string|null,projectID:string):Pending|null {
 if(raw===null)return null;
 const p=JSON.parse(raw) as Pending;
 if(p.schema!==1||p.projectID!==projectID||typeof p.body!=='string')throw Error('画布待确认记录损坏，请保留记录后修复');
 const r=JSON.parse(p.body);if(typeof r.requestId!=='string'||!Number.isSafeInteger(r.expectedRevision)||r.expectedRevision<0||!r.command)throw Error('画布命令记录损坏');
 return p;
}
export function worldPosition(doc:CanvasDocument,node:Node):Point {
 let {x,y}=node.position, parent=node.parentId;const seen=new Set([node.id]);
 while(parent!==null){if(seen.has(parent)||!doc.nodes[parent])throw Error('Invalid frame parent');seen.add(parent);const n=doc.nodes[parent];x+=n.position.x;y+=n.position.y;parent=n.parentId;}
 return {x,y};
}
export function visibleNodes(doc:CanvasDocument,view:View,width:number,height:number):Node[] {
 // ponytail: a linear scan is bounded at 10,000 nodes; add a spatial index only if measured pan latency requires it.
 const positions=new Map<string,Point>(),hidden=new Map<string,boolean>();
 function resolve(node:Node):Point { const cached=positions.get(node.id);if(cached)return cached;const stack:Node[]=[];let n:Node|undefined=node;while(n&&!positions.has(n.id)){stack.push(n);n=n.parentId?doc.nodes[n.parentId]:undefined;}while(stack.length){const current=stack.pop()!,p=current.parentId?doc.nodes[current.parentId]:undefined,pos=p?positions.get(p.id)!:{x:0,y:0};positions.set(current.id,{x:pos.x+current.position.x,y:pos.y+current.position.y});hidden.set(current.id,current.hidden||!!p&&(hidden.get(p.id)===true||p.kind==='frame'&&p.collapsed));}return positions.get(node.id)!; }
 return Object.values(doc.nodes).filter(n=>{const p=resolve(n);return !hidden.get(n.id)&&(p.x+n.size.width)*view.scale+view.x>=0&&(p.y+n.size.height)*view.scale+view.y>=0&&p.x*view.scale+view.x<=width&&p.y*view.scale+view.y<=height;}).sort((a,b)=>a.zIndex-b.zIndex);
}
export function patch(before:Node|null,after:Node|null):Command {return {type:'canvas.patch',nodes:[{id:(after??before)!.id,before,after}],edges:[]};}
