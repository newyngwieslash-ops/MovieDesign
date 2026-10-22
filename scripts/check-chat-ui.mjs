// Real TSX rendering; only browser editor/HTTP/hooks are controlled. No test framework.
import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {createRequire} from 'node:module';
import vm from 'node:vm';
import {setImmediate as tick} from 'node:timers/promises';
const require=createRequire(import.meta.url),ts=require('../desktop/node_modules/typescript'),React=require('../desktop/node_modules/react'),{renderToStaticMarkup}=require('../desktop/node_modules/react-dom/server');
const {QueryClient,QueryObserver}=require('../desktop/node_modules/@tanstack/react-query');
const delay=ms=>new Promise(resolve=>setTimeout(resolve,ms));
function load(file,dependencies={},globals={}) {const module={exports:{}};vm.runInNewContext(ts.transpileModule(readFileSync(new URL('../desktop/src/'+file,import.meta.url),'utf8'),{compilerOptions:{module:ts.ModuleKind.CommonJS,jsx:ts.JsxEmit.ReactJSX}}).outputText,{module,exports:module.exports,console,crypto,TextEncoder,JSON,Error,setTimeout,clearTimeout,AbortController,structuredClone,...globals,require:n=>dependencies[n]??(n==='react'?React:n==='react/jsx-runtime'?require('../desktop/node_modules/react/jsx-runtime'): {})});return module.exports;}
test('actual assistant and tool output render hostile strings literally',()=>{
 const {AssistantMessage}=load('features/chat/AssistantMessage.tsx');
 const html=renderToStaticMarkup(React.createElement(AssistantMessage,{parts:[{step:1,part_id:'a',part_type:'text',content:'<img src=x onerror=alert(1)>'},{step:1,part_id:'b',part_type:'tool',tool:'<script>bad()</script>',tool_output:'<iframe src=evil>',tool_input:{html:'<svg onload=bad()>'},state:'completed'}]}));
 assert(html.includes('&lt;img'));assert(html.includes('&lt;iframe'));assert(!html.includes('<img'));assert(!html.includes('<iframe'));assert(!html.includes('<script'));
});
export {load};
function find(tree,predicate){if(Array.isArray(tree)){for(const c of tree){const hit=find(c,predicate);if(hit)return hit;}}else if(tree&&typeof tree==='object'){if(predicate(tree))return tree;return find(tree.props?.children,predicate);}}
function harness(initial={},storageFailure=false,annotationSource,skillFetch){
 const map=new Map(Object.entries(initial)),writes=[],reads=[],hooks=[],effects=[];let cursor=0,tree,doc={type:'doc',content:[{type:'paragraph',content:[{type:'text',text:'original'}]}]},text='original',editorOptions,listener,history=[];
 const storage={getItem:k=>{if(storageFailure)throw Error('denied');return map.get(k)??null;},setItem:(k,v)=>map.set(k,v),removeItem:k=>map.delete(k)};
 const react={useState(init){const i=cursor++;if(!(i in hooks))hooks[i]=typeof init==='function'?init():init;return [hooks[i],v=>{hooks[i]=typeof v==='function'?v(hooks[i]):v;}];},useRef(init){return hooks[cursor++]??={current:init};},useEffect(fn,deps){const i=cursor++,old=hooks[i];if(!old||deps.some((v,j)=>v!==old.deps[j])){hooks[i]={deps,cleanup:old?.cleanup};effects.push(()=>{hooks[i].cleanup?.();hooks[i].cleanup=fn();});}}};
 class APIError extends Error {constructor(status,code='invalid_input',message='definite rejection'){super(message);this.status=status;this.code=code;}}
 const gateway={APIError,newRequestID:()=>crypto.randomUUID(),api:(p,path,init={})=>{if(init.method==='POST')return new Promise((resolve,reject)=>writes.push({p,path,body:init.body,resolve,reject}));reads.push(path);return Promise.resolve({items:path.includes('/messages')?history:[{id:'s',project_id:'p',title:'Saved',state:'active',created_at:1}],coverage:{total:1,next_offset:1,exhausted:true}});}};
 let skillQuery,skillData=[];
 if(skillFetch){const actual=load('gateway.ts',{}, {fetch:skillFetch,URL,Headers});const original=gateway.api;gateway.api=(p,path,init)=>path.startsWith('/api/skills')?actual.api(p,path,init):original(p,path,init);gateway.allPages=actual.allPages;}
 const editor={get isEmpty(){return text.length===0;},getText:()=>text,getJSON:()=>doc,setEditable(){},chain(){return {focus(){return this;},insertContent(){return this;},run(){}};},commands:{clearContent(){text='';doc={type:'doc',content:[{type:'paragraph'}]};},setContent(v){doc=v;text=v.content?.[0]?.content?.[0]?.text??'';}}};
 const jsx=(type,props)=>({type,props}),helpers=load('features/chat/messages.ts');
 const {ChatPanel}=load('features/chat/ChatPanel.tsx',{'react':react,'react/jsx-runtime':{jsx,jsxs:jsx},'@tanstack/react-query':{useQuery:o=>{if(o.queryKey.length===1&&o.queryKey[0]==='skills'){skillQuery=o;return {data:skillData};}return {data:[]};}},'@tiptap/react':{useEditor:o=>{editorOptions=o;return editor;},EditorContent:()=>null},'@tiptap/starter-kit':{default:{configure:()=>({})}},'../../gateway':gateway,'../../events':{subscribeEvents:(_p,event)=>{listener=event;return()=>{};}},'./AssetMention':{AssetMention:()=>({})},'./messages':helpers},{localStorage:storage});
 const render=()=>{cursor=0;tree=ChatPanel({projectID:'p',annotationSource});while(effects.length)effects.shift()();return tree;};
 const settle=async()=>{for(let i=0;i<5;i++){render();await tick();}render();};
 render();
 return {map,writes,reads,APIError,render,settle,loadSkills:async()=>{skillData=await skillQuery.queryFn({signal:new AbortController().signal});return skillData;},get text(){return text;},select:label=>find(render(),n=>n.type==='select'&&n.props['aria-label']===label),button:label=>find(render(),n=>n.type==='button'&&[n.props.children].flat().join('')===label),type:value=>{text=value;doc={type:'doc',content:[{type:'paragraph',content:[{type:'text',text:value}]}]};editorOptions.onUpdate({editor});},history:value=>{history=value;},event:e=>listener(e),unmount:()=>{for(const h of hooks)h?.cleanup?.();}};
}
test('actual skill query consumes every coverage page and rejects a nonadvancing cursor',async()=>{
 const calls=[],names=['口播视频','图片标注修复'];
 const h=harness({},false,undefined,async url=>{calls.push(url);const offset=Number(url.searchParams.get('offset'));assert.equal(url.searchParams.has('project_id'),false);return {ok:true,json:async()=>({items:[{name:names[offset],version:'1.0.0'}],coverage:{total:2,next_offset:offset+1,exhausted:offset===1}})};});
 const skills=await h.loadSkills();assert.deepEqual(Array.from(skills,s=>s.name),names);assert.deepEqual(calls.map(u=>u.searchParams.get('offset')),['0','1']);
 const selector=h.select('创作方法');for(const name of names)assert(find(selector,n=>n.type==='option'&&n.props.value===name));h.unmount();
 const stalled=harness({},false,undefined,async()=>({ok:true,json:async()=>({items:[],coverage:{total:2,next_offset:0,exhausted:false}})}));
 await assert.rejects(()=>stalled.loadSkills(),/分页进度无效/);stalled.unmount();
});
test('actual skill selector freezes explicit choice and restart replay retains it',async()=>{
 const h=harness();await h.settle();h.select('创作方法').props.onChange({target:{value:'口播视频'}});h.type('original');h.button('发送消息').props.onClick();const wire=h.writes[0].body;assert.equal(JSON.parse(wire).skill_id,'口播视频');h.writes[0].reject(Error('lost'));await h.settle();assert(h.select('创作方法').props.disabled);
 const restart=harness(Object.fromEntries(h.map));await restart.settle();assert.equal(restart.writes.length,0);restart.button('恢复原发送请求').props.onClick();assert.equal(restart.writes[0].body,wire);
 const none=harness();await none.settle();none.select('创作方法').props.onChange({target:{value:''}});none.type('口播');none.button('发送消息').props.onClick();assert.equal(JSON.parse(none.writes[0].body).skill_id,'');
});
test('actual composer freezes unknown body, locks duplicates, and restart never POSTs',async()=>{
 const h=harness();await h.settle();h.type('original');const send=h.button('发送消息');send.props.onClick();send.props.onClick();assert.equal(h.writes.length,1);const wire=h.writes[0].body;h.writes[0].reject(Error('lost'));await h.settle();h.type('changed behind disabled editor');h.button('恢复原发送请求').props.onClick();assert.equal(h.writes[1].body,wire);assert.equal(JSON.parse(wire).text,'original');h.unmount();
 const restart=harness(Object.fromEntries(h.map));await restart.settle();assert.equal(restart.writes.length,0);assert(restart.button('发送消息').props.disabled);assert(restart.reads.every(p=>p.includes('limit=100')));
});
test('actual composer storage denial/corruption blocks writes visibly',async()=>{
 for(const h of [harness({'moviedesign.chat.send.v1.p':'{'}),harness({},true)]){await h.settle();assert(h.button('新建对话').props.disabled);h.type('test');h.button('发送消息').props.onClick();assert.equal(h.writes.length,0);assert(find(h.render(),n=>n.props?.role==='alert'));}
});
test('late successful send from unmounted project cannot clear newer pending input',async()=>{
 const h=harness();await h.settle();h.type('old');h.button('发送消息').props.onClick();h.unmount();h.map.set('moviedesign.chat.send.v1.p','newer');h.writes[0].resolve({id:'m'});await tick();assert.equal(h.map.get('moviedesign.chat.send.v1.p'),'newer');
});
test('queue edit requires cancelled receipt before revised send gets a new identity',async()=>{
 const h=harness();h.history([{id:'m',session_id:'s',requestId:'old-key',role:'user',content:{requestId:'old-key',document:{type:'doc',content:[{type:'paragraph',content:[{type:'text',text:'queued'}]}]},text:'queued',references:[],annotations:[]},state:'queued',created_at:1}]);await h.settle();h.type('');h.button('撤回并编辑').props.onClick();assert.equal(h.writes.length,1);assert.equal(JSON.parse(h.writes[0].body).message_id,'m');assert(h.button('载入已撤回草稿').props.disabled);
 h.writes[0].reject(Error('lost cancellation'));await h.settle();assert(h.button('发送消息').props.disabled);h.button('恢复原取消请求').props.onClick();assert.equal(h.writes[1].body,h.writes[0].body);
 h.history([{id:'m',session_id:'s',requestId:'old-key',role:'user',content:{text:'queued'},state:'cancelled',created_at:1}]);h.writes[1].resolve({status:'cancelling'});await h.settle();h.button('载入已撤回草稿').props.onClick();h.type('revised');h.button('发送消息').props.onClick();assert.notEqual(JSON.parse(h.writes[2].body).requestId,'old-key');assert.equal(JSON.parse(h.writes[2].body).text,'revised');
});
test('definite public validation rejection permits correcting draft without replaying rejected input',async()=>{
 const h=harness();await h.settle();h.type('bad reference');h.button('发送消息').props.onClick();h.writes[0].reject(new h.APIError(400));await h.settle();assert.equal(h.map.has('moviedesign.chat.send.v1.p'),false);assert.equal(h.button('发送消息').props.disabled,false);
});
test('invalid draft is rejected before POST and can be corrected without a stale recovery record',async()=>{
 const h=harness();await h.settle();h.type('x'.repeat(65537));h.button('发送消息').props.onClick();await h.settle();assert.equal(h.writes.length,0);assert.equal(h.map.size,0);h.type('valid');h.button('发送消息').props.onClick();assert.equal(h.writes.length,1);
});
test('existing confirmation cards reread native delivery state after chat completion',async()=>{
 const hooks=[],effects=[];let cursor=0,listener,reads=0;
 const react={useState(v){const i=cursor++;hooks[i]??=typeof v==='function'?v():v;return [hooks[i],v=>hooks[i]=v];},useRef(v){return hooks[cursor++]??={current:v};},useEffect(fn){const i=cursor++;if(!(i in hooks)){hooks[i]=true;effects.push(fn);}}};
 const jsx=(type,props)=>({type,props});
 const {ConfirmationCards,ConfirmationCard}=load('features/questions/ConfirmationCard.tsx',{'react':react,'react/jsx-runtime':{jsx,jsxs:jsx},'@tanstack/react-query':{useQueryClient:()=>({cancelQueries(){},invalidateQueries(){}})},'../../gateway':{api:async()=>{reads++;return {items:[{id:'q',project_id:'p',session_id:'s',kind:'choice',state:'answered',revision:1,questions:[],native_send_state:reads===1?'pending':'sent'}]};}},'../../events':{subscribeEvents:(_p,cb)=>{listener=cb;return()=>{};}}},{setInterval:()=>1,clearInterval(){}});
 const render=()=>{cursor=0;return ConfirmationCards({projectID:'p'});};render();for(const f of effects)f();await tick();assert.equal(find(render(),n=>n.type===ConfirmationCard).props.question.native_send_state,'pending');listener({type:'chat_done',project_id:'p',session_id:'s',id:2,payload:{}});await tick();assert.equal(find(render(),n=>n.type===ConfirmationCard).props.question.native_send_state,'sent');
});
const queued={id:'m',session_id:'s',requestId:'queued-key',role:'user',content:{requestId:'queued-key',document:{type:'doc',content:[{type:'paragraph',content:[{type:'text',text:'queued'}]}]},text:'queued',references:[],annotations:[]},state:'queued',created_at:1};
test('withdraw-edit preflight preserves existing text and annotation-only drafts without writes',async()=>{
 for(const mode of ['text','annotation']){
  const h=harness({},false,{projectID:'p',assetId:'a',sourceVersionID:'v',name:'image',annotations:[{assetId:'a',comment:'keep annotation',bounds:{x:1}}]});h.history([queued]);await h.settle();
  h.type(mode==='text'?'keep this unsent draft':'');if(mode==='annotation'){h.button('附加当前标注 · image · 版本 v').props.onClick();h.type('');}
  const button=h.button('撤回并编辑');assert.equal(button.props.disabled,true);button.props.onClick();await h.settle();assert.equal(h.writes.length,0);assert.equal(h.map.size,0);assert.equal(h.button('清空草稿').props.disabled,false);
  if(mode==='text')assert.equal(h.text,'keep this unsent draft');else assert(h.button('移除草稿标注'));
 }
});
test('definite dequeue race releases only owned withdrawal journal, refreshes, and restart is usable',async()=>{
 const h=harness();h.history([queued]);await h.settle();h.type('');h.button('撤回并编辑').props.onClick();const before=h.reads.length;
 h.history([{...queued,state:'accepted'}]);h.writes[0].reject(new h.APIError(409,'invalid_input','Only queued messages can be withdrawn'));await h.settle();assert.equal(h.writes.length,1);assert.equal(h.map.size,0);assert(h.reads.length>before);assert.equal(h.button('清空草稿').props.disabled,false);assert.equal(h.button('取消本次对话及队列').props.disabled,false);
 const restart=harness(Object.fromEntries(h.map));await restart.settle();restart.type('new draft');assert.equal(restart.writes.length,0);assert.equal(restart.button('发送消息').props.disabled,false);
});
test('unknown, idempotency and unrelated 409s retain exact withdrawal identity across restart',async()=>{
 for(const failure of [Error('lost'),{code:'conflict',message:'Only queued messages can be withdrawn'},{code:'invalid_input',message:'Different conflict'}]){
  const h=harness();h.history([queued]);await h.settle();h.type('');h.button('撤回并编辑').props.onClick();const original=h.writes[0].body;h.writes[0].reject(failure instanceof Error?failure:new h.APIError(409,failure.code,failure.message));await h.settle();assert.equal(h.map.size,1);assert.equal(h.button('清空草稿').props.disabled,true);
  const restart=harness(Object.fromEntries(h.map));await restart.settle();assert.equal(restart.writes.length,0);restart.button('恢复原取消请求').props.onClick();assert.equal(restart.writes[0].body,original);
 }
});
test('known withdrawal rejection cannot clear another pending journal and does not classify send conflicts',async()=>{
 const h=harness();h.history([queued]);await h.settle();h.type('');h.button('撤回消息').props.onClick();h.map.set('moviedesign.chat.cancel.v1.p','newer');h.writes[0].reject(new h.APIError(409,'invalid_input','Only queued messages can be withdrawn'));await h.settle();assert.equal(h.map.get('moviedesign.chat.cancel.v1.p'),'newer');
 const send=harness();await send.settle();send.type('original');send.button('发送消息').props.onClick();send.writes[0].reject(new send.APIError(409,'invalid_input','Only queued messages can be withdrawn'));await send.settle();assert.equal(send.map.size,1);
});

const approvedBilling={id:'q',project_id:'p',session_id:'s',task_id:'t',task_revision:1,kind:'billing',state:'approved',revision:2,provider:'saved provider',model:'saved model',questions:[{header:'生成费用确认',question:'确认此报价并提交一次生成？',options:[{label:'确认生成',description:'一次生成'}]}],quote:{quote_id:'original-quote',currency:'CNY',estimated_amount:123,minor_unit_scale:2,estimated_credits:1,expires_at:1,version:'saved-v1',source:'saved quote'}};
const billingIntent=JSON.stringify({path:'/api/question/q/reply?session_id=s',body:'{"requestId":"original-human-reply","answers":[["确认生成"]],"expected_revision":1}'});
function confirmationHarness({question=approvedBilling,map=new Map(),client=new QueryClient({defaultOptions:{queries:{retry:false,gcTime:0}}}),saved=billingIntent,denied=false}={}){
 const key=`moviedesign.question.pending.v1.${question.project_id}.${question.id}`;
 if(saved!==undefined&&!map.has(key))map.set(key,saved);
 const hooks=[],effects=[],requests=[],observers=[];let cursor=0,refreshes=0;
 const storage={getItem:k=>{if(denied)throw Error('storage denied');return map.get(k)??null},setItem:(k,v)=>map.set(k,v),removeItem:k=>map.delete(k)};
 const react={useState(init){const i=cursor++;if(!(i in hooks))hooks[i]=typeof init==='function'?init():init;return[hooks[i],v=>hooks[i]=typeof v==='function'?v(hooks[i]):v]},useRef(init){return hooks[cursor++]??={current:init}},useEffect(fn,deps){const i=cursor++,old=hooks[i];if(!old||deps.some((v,j)=>v!==old.deps[j])){hooks[i]={deps};effects.push(()=>{old?.cleanup?.();hooks[i].cleanup=fn()})}}};
 const query={useQueryClient:()=>client,useQuery(options){const i=cursor++;if(!hooks[i]){const observer=new QueryObserver(client,options);hooks[i]=observer;observers.push(observer.subscribe(()=>{}))}else hooks[i].setOptions(options);return hooks[i].getCurrentResult()}};
 const gateway=load('gateway.ts',{}, {URL,Headers,fetch:(url,init)=>new Promise((resolve,reject)=>requests.push({url,method:init.method??'GET',body:init.body,signal:init.signal,resolve:data=>resolve({ok:true,json:async()=>data}),reject}))});
 const pending=load('features/questions/pending.ts'),tasks=load('features/tasks/tasks.ts');
 const jsx=(type,props)=>({type,props});
 const {ConfirmationCard}=load('features/questions/ConfirmationCard.tsx',{'react':react,'react/jsx-runtime':{jsx,jsxs:jsx},'@tanstack/react-query':query,'../../gateway':gateway,'./pending':pending,'../tasks/tasks':tasks},{localStorage:storage,Date,setInterval:()=>0,clearInterval(){}});
 const render=()=>{cursor=0;const tree=ConfirmationCard({question,refresh:()=>refreshes++});while(effects.length)effects.shift()();return tree};
 const settle=async()=>{for(let i=0;i<4;i++){render();await tick()}return render()};
 const button=label=>find(render(),n=>n.type==='button'&&n.props.children===label);
 render();
 return{key,map,client,question,requests,render,settle,button,get refreshes(){return refreshes},unmount(){for(const unsubscribe of observers)unsubscribe();for(const h of hooks)h?.cleanup?.()}};
}

test('H15a approved confirmation recovery follows authoritative task state without changing human history',async t=>{
 await t.test('terminal tasks clear only the local pending marker and never POST the old approval',async()=>{
  for(const status of ['completed','failed','cancelled']){
   const question=structuredClone(approvedBilling),before=JSON.stringify(question),h=confirmationHarness({question});
   assert.equal(h.button('恢复已批准的原任务'),undefined,'no replay while task status is unconfirmed');
   assert.equal(h.requests.length,1);assert.equal(h.requests[0].method,'GET');assert.equal(h.requests[0].url.pathname,'/api/tasks/t');assert.equal(h.requests[0].url.searchParams.get('project_id'),'p');
   h.requests[0].resolve({id:'t',status,revision:4,attempt_state:status,billing:{charge_status:'completed'}});await h.settle();
   assert.equal(h.button('恢复已批准的原任务'),undefined);assert.equal(h.map.has(h.key),false);assert.equal(h.requests.filter(r=>r.method==='POST').length,0);
   assert.equal(JSON.stringify(question),before);assert(JSON.stringify(h.render()).includes('saved quote'));assert(JSON.stringify(h.render()).includes('saved provider'));h.unmount();
  }
 });
 await t.test('query failure and active or unknown submissions retain the exact original request with query only',async()=>{
  for(const state of ['error','running','unknown','accepted-submitted','later-attempt']){
   const h=confirmationHarness();assert.equal(h.requests.length,1,'approved card must query the current task');
   if(state==='error')h.requests[0].reject(Error('query transport lost'));
   else h.requests[0].resolve({id:'t',status:state==='accepted-submitted'||state==='later-attempt'?'accepted':state,revision:state==='later-attempt'?8:2,attempt_state:state==='later-attempt'?'approved':'submitted',upstream_id:state==='later-attempt'?'':'upstream-1',billing:{charge_status:state==='later-attempt'?'not_submitted':'submitted'}});
   await h.settle();assert.equal(h.map.get(h.key),billingIntent);assert.equal(h.button('恢复已批准的原任务'),undefined);assert(h.button('查询确认状态'));assert(!JSON.stringify(h.render()).includes('只会启动尚未发送'));
   h.button('查询确认状态').props.onClick();await h.settle();assert.equal(h.requests.filter(r=>r.method==='POST').length,0);assert(h.requests.filter(r=>r.method==='GET').length>=2);h.unmount();
  }
 });
 await t.test('only the same approved unsent task permits a manual replay of the frozen request',async()=>{
  const h=confirmationHarness();assert.equal(h.requests.length,1,'approved card must query the current task');h.requests[0].resolve({id:'t',status:'accepted',revision:2,attempt_state:'approved',billing:{charge_status:'not_submitted'}});await h.settle();
  h.button('恢复已批准的原任务').props.onClick();h.button('恢复已批准的原任务').props.onClick();assert.equal(h.requests.filter(r=>r.method==='POST').length,1);assert.equal(h.requests.at(-1).body,JSON.parse(billingIntent).body);h.requests.at(-1).reject(Error('reply outcome unknown'));await h.settle();assert.equal(h.map.get(h.key),billingIntent);h.unmount();
  for(const saved of [JSON.stringify({path:'/api/question/q/reject?session_id=s',body:'{"requestId":"rejected-reply","answers":[],"expected_revision":1}'}),JSON.stringify({path:'/api/question/q/reply?session_id=s',body:'{"requestId":"old-reply","answers":[["确认生成"]],"expected_revision":4}'})]){
   const other=confirmationHarness({saved});other.requests[0].resolve({id:'t',status:'accepted',revision:2,attempt_state:'approved',billing:{charge_status:'not_submitted'}});await other.settle();assert.equal(other.button('恢复已批准的原任务'),undefined);assert.equal(other.map.get(other.key),saved);assert.equal(other.requests.filter(r=>r.method==='POST').length,0);other.unmount();
  }
 });
 await t.test('terminal observation and late replies cannot clear a newer or another project intent',async()=>{
  const h=confirmationHarness();assert.equal(h.requests.length,1,'approved card must query the current task');h.requests[0].resolve({id:'t',status:'accepted',revision:2,attempt_state:'approved',billing:{charge_status:'not_submitted'}});await h.settle();h.button('恢复已批准的原任务').props.onClick();const oldReply=h.requests.at(-1);h.unmount();
  h.map.set(h.key,'newer request');const other=confirmationHarness({map:h.map,client:h.client,question:{...approvedBilling,project_id:'other'}});
  oldReply.resolve({state:'approved'});await other.settle();assert.equal(h.map.get(h.key),'newer request');assert.equal(other.map.get(other.key),billingIntent);assert.equal(other.requests[0].url.searchParams.get('project_id'),'other');
  other.map.set(other.key,'other newer request');other.requests[0].resolve({id:'t',status:'completed',revision:4});await other.settle();assert.equal(other.map.get(other.key),'other newer request');assert.equal(h.map.get(h.key),'newer request');other.unmount();
 });
 await t.test('late task GET after unmount and denied storage preserve the pending evidence',async()=>{
  const h=confirmationHarness();assert.equal(h.requests.length,1,'approved card must query the current task');h.unmount();h.map.set(h.key,'newer request');h.requests[0].resolve({id:'t',status:'completed',revision:4});await tick();assert.equal(h.map.get(h.key),'newer request');
  const denied=confirmationHarness({denied:true});assert(denied.requests[0]);denied.requests[0].reject(Error('query lost'));await denied.settle();assert.equal(denied.map.get(denied.key),billingIntent);assert.equal(denied.requests.filter(r=>r.method==='POST').length,0);assert(find(denied.render(),n=>n.props?.role==='alert'));assert.equal(denied.button('查询确认状态').props.disabled,false);denied.button('查询确认状态').props.onClick();assert.equal(denied.requests.length,2);denied.unmount();
 });
 await t.test('task_changed rereads current task only in its project and retires the completed recovery prompt',async()=>{
  const h=confirmationHarness(),other=confirmationHarness({client:h.client,question:{...approvedBilling,project_id:'other'}});
  h.requests[0].resolve({id:'t',status:'accepted',revision:2,attempt_state:'approved',billing:{charge_status:'not_submitted'}});other.requests[0].resolve({id:'t',status:'running',revision:3});await h.settle();await other.settle();assert(h.button('恢复已批准的原任务'));
  let cursor=0,listener;const hooks=[],effects=[],react={useState(init){const i=cursor++;hooks[i]??=init;return[hooks[i],v=>hooks[i]=v]},useRef(init){return hooks[cursor++]??={current:init}},useEffect(fn){const i=cursor++;if(!(i in hooks)){hooks[i]=true;effects.push(fn)}}},jsx=(type,props)=>({type,props});
  const {ConfirmationCards}=load('features/questions/ConfirmationCard.tsx',{'react':react,'react/jsx-runtime':{jsx,jsxs:jsx},'@tanstack/react-query':{useQueryClient:()=>h.client},'../../gateway':{api:async()=>({items:[approvedBilling]})},'../../events':{subscribeEvents:(_p,cb)=>{listener=cb;return()=>{}}}},{setInterval:()=>0,clearInterval(){}});
  ConfirmationCards({projectID:'p'});const cleanups=effects.map(fn=>fn());await h.settle();assert.equal(h.requests.length,2);h.requests[1].resolve({id:'t',status:'running',revision:3});await h.settle();
  listener({type:'task_changed',project_id:'p',task_id:'t'});await h.settle();assert.equal(h.requests.length,3);h.requests[2].resolve({id:'t',status:'completed',revision:4});await h.settle();await other.settle();
  assert.equal(h.button('恢复已批准的原任务'),undefined);assert.equal(h.map.has(h.key),false);assert.equal(other.requests.length,1);assert.equal(other.map.get(other.key),billingIntent);assert.equal(h.requests.filter(r=>r.method==='POST').length,0);cleanups.forEach(fn=>fn?.());h.unmount();other.unmount();
 });
});

test('H15a I1 task_changed during first GET discards the old snapshot and requires a newer GET before recovery',async()=>{
 for(const status of ['running','completed']){
  const h=confirmationHarness(),other=confirmationHarness({client:h.client,question:{...approvedBilling,project_id:'other'}});
  let cursor=0,listener;const hooks=[],effects=[],react={useState(init){const i=cursor++;hooks[i]??=init;return[hooks[i],v=>hooks[i]=v]},useRef(init){return hooks[cursor++]??={current:init}},useEffect(fn){const i=cursor++;if(!(i in hooks)){hooks[i]=true;effects.push(fn)}}},jsx=(type,props)=>({type,props});
  const {ConfirmationCards}=load('features/questions/ConfirmationCard.tsx',{'react':react,'react/jsx-runtime':{jsx,jsxs:jsx},'@tanstack/react-query':{useQueryClient:()=>h.client},'../../gateway':{api:async()=>({items:[approvedBilling]})},'../../events':{subscribeEvents:(_p,cb)=>{listener=cb;return()=>{}}}},{setInterval:()=>0,clearInterval(){}});
  ConfirmationCards({projectID:'p'});const cleanups=effects.map(fn=>fn());
  listener({type:'task_changed',project_id:'p',task_id:'t'});await h.settle();
  assert(h.requests.length>=2,'an event during the first GET must start a newer GET');
  const stale=h.requests.slice(0,-1),fresh=h.requests.at(-1);
  for(const request of stale)request.resolve({id:'t',status:'accepted',revision:2,attempt_state:'approved',billing:{charge_status:'not_submitted'}});
  await h.settle();assert.equal(h.button('恢复已批准的原任务'),undefined,'late unsent snapshots cannot satisfy a newer event');assert.equal(h.map.get(h.key),billingIntent);
  listener({type:'task_changed',project_id:'p',task_id:'t'});await h.settle();const latest=h.requests.at(-1);assert.notEqual(latest,fresh,'another in-flight event must also retain its newer observation');
  fresh.resolve({id:'t',status:'accepted',revision:2,attempt_state:'approved',billing:{charge_status:'not_submitted'}});await h.settle();assert.equal(h.button('恢复已批准的原任务'),undefined);
  latest.resolve({id:'t',status,revision:4,attempt_state:status,billing:{charge_status:status==='completed'?'completed':'submitted'}});await h.settle();
  assert.equal(h.button('恢复已批准的原任务'),undefined);assert.equal(h.map.get(h.key),status==='completed'?undefined:billingIntent);assert.equal(h.requests.filter(r=>r.method==='POST').length,0);assert.equal(other.requests.length,1);assert.equal(other.map.get(other.key),billingIntent);assert(JSON.stringify(h.render()).includes('saved quote'));
  cleanups.forEach(fn=>fn?.());h.unmount();other.unmount();
 }
});

test('typed text bursts leave task GETs and questions alone while business and unknown chat events still refresh',async t=>{
 const client=new QueryClient({defaultOptions:{queries:{retry:false,gcTime:0}}}),hooks=[],effects=[];let cursor=0,listener,reconnect,questionReads=0,reads=0,aborted=0,completed=0,otherReads=0;
 const react={useState(init){const i=cursor++;hooks[i]??=init;return[hooks[i],v=>hooks[i]=v]},useRef(init){return hooks[cursor++]??={current:init}},useEffect(fn){const i=cursor++;if(!(i in hooks)){hooks[i]=true;effects.push(fn)}}},jsx=(type,props)=>({type,props});
 const {ConfirmationCards}=load('features/questions/ConfirmationCard.tsx',{'react':react,'react/jsx-runtime':{jsx,jsxs:jsx},'@tanstack/react-query':{useQueryClient:()=>client},'../../gateway':{api:async()=>{questionReads++;return {items:[]}}},'../../events':{subscribeEvents:(_p,cb,refresh)=>{listener=cb;reconnect=refresh;return()=>{}}}},{setInterval:()=>0,clearInterval(){}});
 ConfirmationCards({projectID:'p'});const cleanups=effects.map(fn=>fn());await tick();
 const observer=new QueryObserver(client,{queryKey:['project','p','tasks'],queryFn:({signal})=>{reads++;return new Promise((resolve,reject)=>{const timer=setTimeout(()=>{completed++;resolve({items:[]})},100);signal.addEventListener('abort',()=>{aborted++;clearTimeout(timer);reject(Error('aborted'))},{once:true})})}}),unsubscribe=observer.subscribe(()=>{});
 const other=new QueryObserver(client,{queryKey:['project','other','tasks'],queryFn:async()=>{otherReads++;return {items:[]}}}),unother=other.subscribe(()=>{});
 try {
  for(let id=1;id<=12;id++){listener({id,type:'chat_message',project_id:'p',session_id:'s',payload:{session_id:'s',message_id:'m',part_id:'part',step:1,part_type:'text',type:'content_delta',content_delta:'prefix '}});await delay(20)}
  assert(completed>0,'an active task GET must complete during text streaming');assert.equal(reads,1,'text deltas must not restart task reads');assert.equal(aborted,0);assert.equal(questionReads,1,'text deltas must not restart question reads');
  t.diagnostic(`typed text burst: ${JSON.stringify({events:12,reads,aborted,completed,questionReads,otherReads})}`);
  for(const event of [{type:'task_changed'},{type:'question_changed'},{type:'chat_done'},{type:'chat_message',payload:{part_type:'tool',type:'tool_result',tool:'question'}},{type:'chat_message',payload:{part_type:'future'}},{type:'chat_message',payload:{part_type:'text',type:'content_delta',content_delta:'mixed',tool_input:{question:'read'}}}]){const previous=reads,questions=questionReads;listener({project_id:'p',session_id:'s',payload:{},...event});await tick();assert.equal(reads,previous+1);assert.equal(questionReads,questions+1)}
  const before=reads;reconnect();await tick();assert.equal(reads,before+1);assert.equal(otherReads,1,'project invalidation remains isolated');
 } finally {cleanups.forEach(fn=>fn?.());unsubscribe();unother();client.clear()}
});
