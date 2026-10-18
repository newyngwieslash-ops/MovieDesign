// Actual CanvasWorkspace source with controlled React/storage/HTTP boundaries; no native UI claims.
import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {createRequire} from 'node:module';
import vm from 'node:vm';
import {setImmediate as tick} from 'node:timers/promises';
const require=createRequire(import.meta.url),ts=require('../desktop/node_modules/typescript');
const source=name=>readFileSync(new URL('../desktop/src/features/canvas/'+name,import.meta.url),'utf8');
function find(tree,name){if(Array.isArray(tree)){for(const t of tree){const hit=find(t,name);if(hit)return hit}}else if(tree&&typeof tree==='object'){if(tree.type==='button'&&tree.props.children===name)return tree;return find(tree.props?.children,name)}}
function locate(tree,predicate){if(Array.isArray(tree)){for(const child of tree){const hit=locate(child,predicate);if(hit)return hit}}else if(tree&&typeof tree==='object'){if(predicate(tree))return tree;return locate(tree.props?.children,predicate)}}
const copy=v=>JSON.parse(JSON.stringify(v));
function harness(){
 const storage=new Map(),requests=[],receipts=new Map(),generations=new Map();let eventListener=()=>{};let tasks=[],document={schemaVersion:1,revision:0,nodes:{},edges:{}},sequence=0,state=[],composerState=[],cursor=0,effects=[],generationPosts=0;
 const localStorage={getItem:k=>storage.get(k)??null,setItem:(k,v)=>storage.set(k,v),removeItem:k=>storage.delete(k)};
 const changed=(a,b)=>!a||b.some((v,i)=>v!==a[i]);
 const react={
  useState(initial){const i=cursor++,owner=state;if(!(i in owner))owner[i]=typeof initial==='function'?initial():initial;return[owner[i],value=>{owner[i]=typeof value==='function'?value(owner[i]):value}]},
  useRef(initial){return state[cursor++]??={current:initial}},
  useCallback(fn,deps){const i=cursor++;if(!state[i]||changed(state[i].deps,deps))state[i]={fn,deps};return state[i].fn},
  useEffect(fn,deps){const i=cursor++,owner=state;if(!owner[i]||changed(owner[i].deps,deps)){effects.push(()=>{owner[i]?.cleanup?.();owner[i]={deps,cleanup:fn()}})}},
 };
 const jsx=(type,props)=>({type,props});
 class APIError extends Error{}
 const gateway={APIError,newRequestID:()=>`id-${++sequence}`,allPages(){},api(project,path,init){
  if(path==='/api/canvas/nodes')return Promise.resolve(copy(document));
  if(path==='/api/generate/image'){generationPosts++;const input=JSON.parse(init.body),existing=generations.get(input.requestId);if(existing){assert.equal(existing.body,init.body);return Promise.resolve({task_id:existing.id})}const id=`task-${input.requestId}`;generations.set(input.requestId,{id,body:init.body});tasks.push({id,request_id:input.requestId,status:'waiting_user',outputs:[{output_key:'image1',state:'declared',resource_id:'resource',composition_id:'composition'}]});return Promise.reject(Error('controlled generation response lost'))}
  assert.equal(path,'/api/canvas/commands');return new Promise((resolve,reject)=>{
   const request={body:init.body,finish(lost=false){
    const input=JSON.parse(init.body);let receipt=receipts.get(input.requestId);
    if(receipt&&receipt.body!==init.body){reject(new APIError('CANVAS_V3_COMMAND_ID_REUSED'));return}
    if(!receipt){assert.equal(input.expectedRevision,document.revision);const command=input.command;const inverse={type:'canvas.patch',nodes:command.nodes.map(c=>({id:c.id,before:c.after,after:c.before})),edges:[]};for(const change of command.nodes){assert.deepEqual(change.before,document.nodes[change.id]??null);if(change.after)document.nodes[change.id]=copy(change.after);else delete document.nodes[change.id]}document.revision++;receipt={body:init.body,requestId:input.requestId,appliedRevision:document.revision,effects:{undo:inverse,compositionIds:[]}};receipts.set(input.requestId,receipt)}
    if(lost)reject(Error('controlled response lost'));else resolve({...copy(receipt),document:copy(document),replayed:true});
   }};requests.push(request);
  });
 }};
 const query={useQuery:options=>({data:options.queryKey[0]==='providers'?{items:[{config:{id:'fixture',name:'Fixture',kind:'image',enabled:true,model_id:'fixture',capability:{edit:false,max_count:1}}}]}:options.queryKey.includes('versions')?[{id:'user-selected',resource_id:'resource'}]:[],dataUpdatedAt:0}),useQueryClient:()=>({invalidateQueries(){}})};
 function load(name,extras={}){const module={exports:{}};vm.runInNewContext(ts.transpileModule(source(name),{compilerOptions:{module:ts.ModuleKind.CommonJS,jsx:ts.JsxEmit.ReactJSX}}).outputText,{module,exports:module.exports,localStorage,Error,Map,Set,console,require:name=>extras[name]??(name==='react'?react:name==='react/jsx-runtime'?{jsx,jsxs:jsx}:name==='@tanstack/react-query'?query:name==='../../gateway'?gateway:name==='../../events'?{subscribeEvents:(p,fn)=>{eventListener=fn;return()=>{eventListener=()=>{}}}}:name==='./CanvasNode'?{CanvasNode(){}}:require(name))});return module.exports}
 const helpers=load('../tasks/tasks.ts'),{GenerationComposer}=load('../tasks/GenerationComposer.tsx',{'./tasks':helpers});
 const canvas=load('canvas.ts'),{CanvasWorkspace}=load('CanvasWorkspace.tsx',{'./canvas':canvas,'../tasks/GenerationComposer':{GenerationComposer},'../tasks/TaskCenter':{useTasks:()=>({data:tasks,refetch:async()=>({data:tasks})})},'../tasks/tasks':helpers} );
 function render(){cursor=0;effects=[];const component=CanvasWorkspace({projectID:'p'});const tree=component.type(component.props);for(const effect of effects)effect();return tree}
 function composer(){const element=locate(render(),n=>n.type===GenerationComposer),parent=state;state=composerState;cursor=0;effects=[];const tree=GenerationComposer(element.props);for(const effect of effects)effect();state=parent;return tree}
 function unmount(){for(const hook of [...state,...composerState])hook?.cleanup?.();state=[];composerState=[]}
 const button=name=>{const b=find(render(),name);assert(b,'missing '+name);return b.props};
 async function click(name){const b=button(name);assert(!b.disabled,'disabled '+name);b.onClick();await tick()}
 const key=canvas.pendingKey('p');
 return {storage,requests,key,render,unmount,button,click,composer,generations,async externalDelivery(){document={...document,revision:document.revision+1,nodes:{...document.nodes,external:{id:"external",kind:"composition",compositionId:"c",parentId:null,position:{x:0,y:0},size:{width:360,height:280},zIndex:0,hidden:false,activeItem:{type:"version",resourceId:"r",versionId:"v"},draft:null}}};eventListener({type:"canvas_changed"});await tick();return render()},get generationPosts(){return generationPosts},get document(){return copy(document)},fill(label,value){locate(composer(),n=>n.props?.['aria-label']===label).props.onChange({target:{value}})},async generate(name){const b=find(composer(),name);if(b?.props.onClick)b.props.onClick();else locate(composer(),n=>n.type==='form').props.onSubmit({preventDefault(){}});await tick()},deliver(){tasks=[{...tasks[0],status:'completed',outputs:[{...tasks[0].outputs[0],state:'delivered',version_id:'delivered'}]}];const patch=helpers.completedPatch(document,tasks);for(const change of patch?.nodes??[])document.nodes[change.id]=copy(change.after);document.revision++;eventListener({type:'canvas_changed'});render()},async selectAndMove(){locate(render(),n=>n.props?.node?.kind==='composition').props.onSelect();locate(render(),n=>n.props?.['aria-label']==='节点 x').props.onBlur({target:{value:'777',valueAsNumber:777}});await this.complete();locate(render(),n=>n.props?.['aria-label']==='画布采用版本').props.onChange({target:{value:'user-selected'}});await this.complete()},async mount(){render();await tick();render()},async complete(lost=false){requests.at(-1).finish(lost);await tick();render()}};
}
for(const mode of ['normal','undo','redo'])for(const reload of [false,true])test(`${mode} unknown continuation preserves frozen mode, reload=${reload}`,async()=>{
 const h=harness();await h.mount();await h.click('添加画框');
 if(mode!=='normal'){await h.complete();await h.click('撤销画布');if(mode==='redo'){await h.complete();await h.click('重做画布')}}
 const original=h.requests.at(-1).body;const forward=JSON.parse(h.requests[0].body).command;
 await h.complete(true);const saved=h.storage.get(h.key);assert(saved);
 if(reload){h.unmount();await h.mount();assert.equal(h.storage.get(h.key),saved)}
 const count=h.requests.length;await h.click('继续确认画布');assert.equal(h.requests.length,count+1);assert.equal(h.requests.at(-1).body,original,'wire body/requestId changed');await h.complete();
 assert.equal(h.storage.has(h.key),false);
 assert.equal(h.button('撤销画布').disabled,mode==='undo');assert.equal(h.button('重做画布').disabled,mode!=='undo');
 await h.click(mode==='undo'?'重做画布':'撤销画布');const next=JSON.parse(h.requests.at(-1).body).command;
 assert.deepEqual(next,mode==='undo'?forward:{type:'canvas.patch',nodes:forward.nodes.map(c=>({id:c.id,before:c.after,after:c.before})),edges:[]});
 assert.equal(JSON.parse(saved).mode,mode,'UI mode was not frozen before POST');
});
test('legacy mode-less pending replays original body and conservatively clears both shortcuts',async()=>{
 const h=harness();await h.mount();await h.click('添加画框');await h.complete(true);const p=JSON.parse(h.storage.get(h.key));delete p.mode;h.storage.set(h.key,JSON.stringify(p));h.unmount();await h.mount();await h.click('继续确认画布');assert.equal(h.requests.at(-1).body,p.body);await h.complete();assert(h.button('撤销画布').disabled);assert(h.button('重做画布').disabled);
});
for(const mode of ['invalid','UNDO','',null,3])test(`invalid pending mode ${JSON.stringify(mode)} preserves record without POST`,async()=>{
 const h=harness();await h.mount();await h.click('添加画框');await h.complete(true);const p={...JSON.parse(h.storage.get(h.key)),mode},raw=JSON.stringify(p);h.storage.set(h.key,raw);const count=h.requests.length;h.unmount();await h.mount();assert(h.button('添加画框').disabled);const resume=find(h.render(),'继续确认画布');if(resume&&!resume.props.disabled)await h.click('继续确认画布');assert.equal(h.requests.length,count);assert.equal(h.storage.get(h.key),raw);
});
for(const restart of [false,true])for(const deleted of [false,true])test(`composed generation replay after completed Canvas preserves original receipt and selection, restart=${restart}, deleted=${deleted}`,async()=>{
 const h=harness();await h.mount();h.fill('图像连接','fixture');h.fill('生成提示词','original prompt');await h.generate('请求费用确认');await h.complete();
 assert.equal(h.generations.size,1);const original=[...h.generations.values()][0].body;const journal=h.storage.get('moviedesign.generation.v1.p');assert(journal);
 h.deliver();await h.complete();await h.selectAndMove();if(deleted){await h.click('移除节点及子节点');await h.complete()}if(restart){h.unmount();await h.mount()}
 const before=h.document,count=h.requests.length;await h.generate('确认原生成请求');
 // A broken replay attempts to recreate already converted nodes; finish that
 // conflicting request so the component reports its actual failure.
 if(h.requests.length>count)await h.complete();
 assert.equal(h.storage.has('moviedesign.generation.v1.p'),false,'original generation journal must clear');assert.equal(h.generations.size,1);assert.equal(h.generationPosts,2);assert.equal([...h.generations.values()][0].body,original);assert.equal(h.requests.length,count,'replay must not write another Canvas command');assert.deepEqual(h.document,before);
});

test("agent delivery appears without composer pending node",async()=>{const h=harness();await h.mount();const tree=await h.externalDelivery();assert(locate(tree,n=>n.props?.node?.id==="external"),"external Canvas node was not refreshed");assert.equal(h.requests.length,0)});
