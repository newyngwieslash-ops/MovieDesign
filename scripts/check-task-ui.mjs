// Actual TSX functions with controlled storage/HTTP and StrictMode effect replay.
import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {createRequire} from 'node:module';
import vm from 'node:vm';
import {setImmediate as tick} from 'node:timers/promises';
const require=createRequire(import.meta.url),ts=require('../desktop/node_modules/typescript');
function find(tree,predicate){if(Array.isArray(tree)){for(const child of tree){const hit=find(child,predicate);if(hit)return hit}}else if(tree&&typeof tree==='object'){if(predicate(tree))return tree;return find(tree.props?.children,predicate)}}
function harness(initial,taskOverride={}){
 const storage=new Map(),requests=[],hooks=[],effects=[];let cursor=0,refreshes=0;
 const key='moviedesign.task.resolve.v1.p.t';if(initial!==undefined)storage.set(key,initial);
 const localStorage={getItem:k=>storage.get(k)??null,setItem:(k,v)=>storage.set(k,v),removeItem:k=>storage.delete(k)};
 const react={useState(initial){const i=cursor++;if(!(i in hooks))hooks[i]=typeof initial==='function'?initial():initial;return [hooks[i],value=>{hooks[i]=typeof value==='function'?value(hooks[i]):value}]},useRef(initial){return hooks[cursor++]??={current:initial}},useEffect(fn){const i=cursor++;if(!(i in hooks)){hooks[i]=fn;effects.push(fn)}}};
 const jsx=(type,props)=>({type,props});class APIError extends Error{status=409}
 const gateway={APIError,newRequestID:()=>crypto.randomUUID(),api:()=>new Promise((resolve,reject)=>requests.push({resolve,reject}))};
 function load(file,extra={}){const module={exports:{}};let source=readFileSync(new URL('../desktop/src/features/'+file,import.meta.url),'utf8');if(file==='tasks/TaskCenter.tsx')source+='\nexport {TaskRow};';vm.runInNewContext(ts.transpileModule(source,{compilerOptions:{module:ts.ModuleKind.CommonJS,jsx:ts.JsxEmit.ReactJSX}}).outputText,{module,exports:module.exports,localStorage,Error,crypto,Date,setInterval:()=>0,clearInterval(){},require:name=>extra[name]??(name==='react'?react:name==='react/jsx-runtime'?{jsx,jsxs:jsx}:name.endsWith('/gateway')?gateway:{})});return module.exports}
 const helpers=load('tasks/tasks.ts'),canvasHelpers=load('canvas/canvas.ts'),{TaskRow}=load('tasks/TaskCenter.tsx',{'./tasks':helpers});
 const render=()=>{cursor=0;return TaskRow({projectID:'p',task:{id:'t',revision:4,prompt:'original',status:'failed',actions:['retry'],progress:{phase:'paused',percent:null},problem:{source:'delivery',message:'retry collection'},...taskOverride},refresh:()=>refreshes++})};
 render();for(const effect of effects){effect()?.();effect()}
 const button=(label='继续收集原结果')=>find(render(),n=>n.type==='button'&&n.props.children===label);
 return {storage,key,requests,render,button,helpers,canvasHelpers,APIError,get refreshes(){return refreshes}};
}
test('actual TaskRow resolves after StrictMode setup-cleanup-setup',async()=>{const h=harness();h.button().props.onClick();assert.equal(h.requests.length,1);h.requests[0].resolve({});await tick();assert.equal(h.storage.has(h.key),false);assert.equal(h.button().props.disabled,false);assert.equal(h.refreshes,1)});
for(const initial of ['', '{}',JSON.stringify({path:'/api/tasks/other/resolve',body:'{"requestId":"x","action":"retry","expected_revision":1}'})])test(`actual TaskRow blocks corrupt or foreign intent ${JSON.stringify(initial)}`,()=>{const h=harness(initial);assert.equal(h.button().props.disabled,true);assert.equal(h.requests.length,0);assert.equal(h.storage.get(h.key),initial)});
test('actual late 4xx preserves another pending body',async()=>{const h=harness();h.button().props.onClick();h.storage.set(h.key,'newer owned request');h.requests[0].reject(new h.APIError('revision changed'));await tick();assert.equal(h.storage.get(h.key),'newer owned request')});
test('M6.5 TaskRow renders server-driven recovery classification, action differences, and dispatches modify_params/modify_storyboard/retry/query',async()=>{
 const h=harness(undefined,{actions:['retry','modify_params','modify_storyboard'],problem:{source:'provider',code:'PROVIDER_RATE_LIMITED',message:'429'},recovery:{stage:'upstream',recommendedAction:'retry',selectedAction:'modify_params',targetStage:'parameter',receiptId:'rec-1',fromTaskId:'t',fromContractId:'c-1'}});
 const text=JSON.stringify(h.render());
 assert(text.includes('修改参数'));assert(text.includes('修改分镜'));assert(text.includes('原样重试'));assert(text.includes('继续查询'));assert(text.includes('聊天回复不构成执行授权'));assert(text.includes('rec-1'));
 h.button('修改参数').props.onClick();
 assert.equal(h.requests.length,1);
 const saved=JSON.parse(h.storage.get(h.key));
 assert.equal(saved.path,'/api/tasks/t/resolve');
 assert.equal(JSON.parse(saved.body).action,'modify_params');
 h.requests[0].resolve({});await tick();
 const u=harness(undefined,{status:'unknown',actions:['query','cancel'],problem:{source:'provider',code:'SUBMISSION_OUTCOME_UNKNOWN',message:'unknown'},recovery:{stage:'unknown',recommendedAction:'query',fromTaskId:'t'}});
 assert(u.button('继续查询'));assert(u.button('取消任务'));assert.equal(u.button('原样重试'),undefined);
});
test('M7.3 TaskRow renders billing breakdown, remote cancel stage boundary, upstream_id, and non-overlapping Canvas positions',()=>{
 const h=harness(undefined,{
  status:'cancelled',
  attempt:2,
  attempt_state:'running',
  upstream_id:'cgt-ark-live-1',
  provider:{id:'ark-1',name:'火山方舟 Seedance',kind:'video',protocol:'ark-video',model_id:'doubao-seedance-2-0',remote_cancel_stages:['queued'],remote_cancel_summary:'仅远端排队阶段（queued）支持 DELETE 取消；running 阶段远端不可中断'},
  billing:{currency:'CNY',estimated_amount:360,minor_unit_scale:2,estimated_credits:36,charge_status:'local_cancelled_remote_may_charge',billing_note:'已向供应商发送请求；本地已停止自动投递'},
  problem:{source:'provider',code:'RUNNING_UNCANCELLABLE',message:'Video task is already running upstream'}
 });
 const text=JSON.stringify(h.render());
 assert(text.includes('cgt-ark-live-1'));
 assert(text.includes('CNY 3.60'));
 assert(text.includes('仅远端排队阶段（queued）支持 DELETE 取消'));
 assert(text.includes('尝试 #2（running）'));
 const doc=h.canvasHelpers.emptyDocument();
 const [first]=h.canvasHelpers.nextGenerationPositions(doc,1);
 assert.equal(first.x,40);assert.equal(first.y,50);
 doc.nodes.n1={id:'n1',kind:'placeholder',expectedKind:'image',draft:null,parentId:null,position:first,size:{width:350,height:350},zIndex:1,hidden:false};
 const [second]=h.canvasHelpers.nextGenerationPositions(doc,1);
 assert(second.x!==first.x||second.y!==first.y,'second generation must not overlap at identical {x:40,y:50}');
});
