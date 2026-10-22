import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {createRequire} from 'node:module';
import vm from 'node:vm';
import {setImmediate as tick} from 'node:timers/promises';
const require=createRequire(import.meta.url),ts=require('../desktop/node_modules/typescript');
function find(n,p){if(Array.isArray(n)){for(const c of n){const x=find(c,p);if(x)return x}}else if(n&&typeof n==='object'){if(p(n))return n;return find(n.props?.children,p)}}
function harness(denied=false,map=new Map(),snapshot) {
 let cursor=0,tree;const hooks=[],calls=[],effects=[];
 const react={useState(init){let i=cursor++;if(!(i in hooks))hooks[i]=typeof init==='function'?init():init;return[hooks[i],v=>hooks[i]=typeof v==='function'?v(hooks[i]):v]},useRef(init){return hooks[cursor++]??={current:init}},useEffect(fn,deps){const i=cursor++,old=hooks[i];if(!old||deps.some((v,j)=>v!==old.deps[j])){hooks[i]={deps};effects.push(()=>{old?.cleanup?.();hooks[i].cleanup=fn()})}}};
 const jsx=(type,props)=>typeof type==='function'?type(props):({type,props});
 const query={useQuery:()=>({data:snapshot}),useQueryClient:()=>({invalidateQueries(){},setQueryData(){}}),useMutation:opts=>({isPending:false,mutate:()=>{Promise.resolve(opts.mutationFn()).then(opts.onSuccess,opts.onError)}})};
 const invoke=(name,args)=>new Promise((resolve,reject)=>calls.push({name,args,resolve,reject}));
 class Channel {onmessage=()=>{}}
 const storage={getItem:k=>{if(denied)throw Error('storage denied');return map.get(k)??null},setItem:(k,v)=>{if(denied)throw Error('storage denied');map.set(k,v)},removeItem:k=>map.delete(k)};
 const deps={'react':react,'react/jsx-runtime':{jsx,jsxs:jsx},'@tanstack/react-query':query,'@tauri-apps/api/core':{invoke,Channel},'../../gateway':{newRequestID:()=>crypto.randomUUID()},'../questions/ConfirmationCard':null};
 const frame={exports:{}};vm.runInNewContext(ts.transpileModule(readFileSync(new URL('../desktop/src/features/questions/ConfirmationCard.tsx',import.meta.url),'utf8'),{compilerOptions:{module:ts.ModuleKind.CommonJS,jsx:ts.JsxEmit.ReactJSX}}).outputText,{module:frame,exports:frame.exports,require:n=>deps[n]??{}});deps['../questions/ConfirmationCard']=frame.exports;
 const module={exports:{}};vm.runInNewContext(ts.transpileModule(readFileSync(new URL('../desktop/src/features/settings/UpdateSettings.tsx',import.meta.url),'utf8'),{compilerOptions:{module:ts.ModuleKind.CommonJS,jsx:ts.JsxEmit.ReactJSX}}).outputText,{module,exports:module.exports,require:n=>deps[n]??{},console,crypto,JSON,Error,localStorage:storage});
 const render=()=>{cursor=0;tree=module.exports.UpdateSettings({ready:true,projectID:''});while(effects.length)effects.shift()();return tree};
 const settle=async()=>{for(let i=0;i<5;i++){await tick();render()}};
 const button=id=>find(render(),n=>n.type==='button'&&n.props['data-testid']===id);
 const check=async(id='candidate-1')=>{button('update-check-btn').props.onClick();await settle();const c=calls.findLast(c=>c.name==='updater_check_and_gate');assert(c,'native check invoked');c.resolve({candidateId:id,manifestVersion:'2.0.0',currentVersion:'0.1.0',autoInstallAllowed:true,action:'candidate_ready'});await settle()};
 render();return {calls,map,render,settle,button,check,unmount:()=>hooks.forEach(h=>h?.cleanup?.())};
}
test('actual update component: check only checks, explicit card gates one install and progress',async()=>{
 const h=harness();await h.check();assert.equal(h.calls.filter(c=>c.name==='updater_install').length,0);
 const approve=h.button('update-approve-btn');assert(approve,'candidate must render structured decision card');approve.props.onClick();approve.props.onClick();await h.settle();
 const decisions=h.calls.filter(c=>c.name==='updater_decide');assert.equal(decisions.length,1);assert(h.map.size);decisions[0].resolve({status:'approve',requestId:decisions[0].args.requestId});await h.settle();
 const installs=h.calls.filter(c=>c.name==='updater_install');assert.equal(installs.length,1);installs[0].args.onProgress.onmessage({stage:'downloading',downloaded:12,total:20});await h.settle();assert(JSON.stringify(h.render()).includes('12'));
 installs[0].reject(Error('controlled install error'));await h.settle();assert(find(h.render(),n=>n.props?.role==='alert'));assert(h.map.size,'retain original unknown intent');
 const restarted=harness(false,h.map);await restarted.settle();assert.equal(restarted.calls.length,0,'restart never repeats install');
});
test('storage failure and rejected candidate never install',async()=>{
 const denied=harness(true);await denied.check();const approve=denied.button('update-approve-btn');if(approve&&!approve.props.disabled)approve.props.onClick();await denied.settle();assert.equal(denied.calls.filter(c=>c.name==='updater_decide'||c.name==='updater_install').length,0);
 const h=harness();await h.check();h.button('update-reject-btn').props.onClick();await h.settle();const c=h.calls.find(c=>c.name==='updater_decide');assert.equal(c.args.decision,'reject');c.resolve({status:'reject'});await h.settle();assert.equal(h.calls.filter(c=>c.name==='updater_install').length,0);
});
test('old rendered candidate cannot approve after new check',async()=>{const h=harness();await h.check('old');const old=h.button('update-approve-btn');await h.check('new');old.props.onClick();await h.settle();assert.equal(h.calls.filter(c=>c.name==='updater_decide').length,0)});

test('late approval after unmount or storage ownership change never installs',async()=>{
 for(const mode of ['unmount','storage']){const h=harness();await h.check();h.button('update-approve-btn').props.onClick();await h.settle();const d=h.calls.find(c=>c.name==='updater_decide');if(mode==='unmount')h.unmount();else h.map.set('moviedesign.update.pending.v1','newer-owner');d.resolve({status:'approve',requestId:d.args.requestId});await tick();await tick();assert.equal(h.calls.filter(c=>c.name==='updater_install').length,0);assert(h.map.size);}
});
test('corrupt saved intent blocks decisions, native error is visible, recovery only queries frozen identity',async()=>{
 const bad=harness(false,new Map([['moviedesign.update.pending.v1','{']]));assert(find(bad.render(),n=>n.props?.role==='alert'));assert.equal(bad.calls.length,0);
 const h=harness();h.button('update-check-btn').props.onClick();await h.settle();h.calls[0].reject(Error('manifest unavailable'));await h.settle();assert(JSON.stringify(h.render()).includes('manifest unavailable'));
 const pending={candidateId:'original-c',version:'2.0.0',requestId:'original-decision',installRequestId:'original-install',decision:'approve'};
 const recovered=harness(false,new Map([['moviedesign.update.pending.v1',JSON.stringify(pending)]]));assert.equal(recovered.calls.length,0);
 find(recovered.render(),n=>n.type==='button'&&n.props.children==='查询原更新请求').props.onClick();await recovered.settle();assert.equal(recovered.calls[0].name,'updater_receipt');assert.equal(recovered.calls[0].args.receiptId,'original-install');assert.equal(recovered.calls[0].args.decisionId,'original-decision');recovered.calls[0].resolve({status:'install_started'});await recovered.settle();assert.equal(recovered.calls.length,1);assert(JSON.stringify(recovered.render()).includes('install_started'));
});

test('gateway app ledger remains visible with lost local storage and never resumes install',()=>{
 const h=harness(false,new Map(),{status:'ready',currentVersion:'0.1.0',platform:'win32',policyCacheAgeMs:0,resolved:{minSupportedVersion:'0.1.0',manualOnly:false},autoUpdateAllowed:true,updateHistory:[{status:'install_started',requestId:'durable-install',candidateId:'original-candidate',version:'2.0.0'}]});
 const rendered=JSON.stringify(h.render());assert(rendered.includes('durable-install'));assert(rendered.includes('结果待核对'));assert.equal(h.calls.length,0);assert.equal(h.map.size,0);
});
