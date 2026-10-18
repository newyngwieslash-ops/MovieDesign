// Actual TSX functions with controlled storage/HTTP and StrictMode effect replay.
import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {createRequire} from 'node:module';
import vm from 'node:vm';
import {setImmediate as tick} from 'node:timers/promises';
const require=createRequire(import.meta.url),ts=require('../desktop/node_modules/typescript');
function find(tree,predicate){if(Array.isArray(tree)){for(const child of tree){const hit=find(child,predicate);if(hit)return hit}}else if(tree&&typeof tree==='object'){if(predicate(tree))return tree;return find(tree.props?.children,predicate)}}
function harness(initial){
 const storage=new Map(),requests=[],hooks=[],effects=[];let cursor=0,refreshes=0;
 const key='moviedesign.task.resolve.v1.p.t';if(initial!==undefined)storage.set(key,initial);
 const localStorage={getItem:k=>storage.get(k)??null,setItem:(k,v)=>storage.set(k,v),removeItem:k=>storage.delete(k)};
 const react={useState(initial){const i=cursor++;if(!(i in hooks))hooks[i]=typeof initial==='function'?initial():initial;return [hooks[i],value=>{hooks[i]=typeof value==='function'?value(hooks[i]):value}]},useRef(initial){return hooks[cursor++]??={current:initial}},useEffect(fn){const i=cursor++;if(!(i in hooks)){hooks[i]=fn;effects.push(fn)}}};
 const jsx=(type,props)=>({type,props});class APIError extends Error{status=409}
 const gateway={APIError,newRequestID:()=>crypto.randomUUID(),api:()=>new Promise((resolve,reject)=>requests.push({resolve,reject}))};
 function load(file,extra={}){const module={exports:{}};let source=readFileSync(new URL('../desktop/src/features/'+file,import.meta.url),'utf8');if(file==='tasks/TaskCenter.tsx')source+='\nexport {TaskRow};';vm.runInNewContext(ts.transpileModule(source,{compilerOptions:{module:ts.ModuleKind.CommonJS,jsx:ts.JsxEmit.ReactJSX}}).outputText,{module,exports:module.exports,localStorage,Error,crypto,Date,setInterval:()=>0,clearInterval(){},require:name=>extra[name]??(name==='react'?react:name==='react/jsx-runtime'?{jsx,jsxs:jsx}:name.endsWith('/gateway')?gateway:{})});return module.exports}
 const helpers=load('tasks/tasks.ts'),{TaskRow}=load('tasks/TaskCenter.tsx',{'./tasks':helpers});
 const render=()=>{cursor=0;return TaskRow({projectID:'p',task:{id:'t',revision:4,prompt:'original',status:'failed',actions:['retry'],progress:{phase:'paused',percent:null},problem:{source:'delivery',message:'retry collection'}},refresh:()=>refreshes++})};
 render();for(const effect of effects){effect()?.();effect()}
 const button=()=>find(render(),n=>n.type==='button'&&n.props.children==='继续收集原结果');
 return {storage,key,requests,render,button,APIError,get refreshes(){return refreshes}};
}
test('actual TaskRow resolves after StrictMode setup-cleanup-setup',async()=>{const h=harness();h.button().props.onClick();assert.equal(h.requests.length,1);h.requests[0].resolve({});await tick();assert.equal(h.storage.has(h.key),false);assert.equal(h.button().props.disabled,false);assert.equal(h.refreshes,1)});
for(const initial of ['', '{}',JSON.stringify({path:'/api/tasks/other/resolve',body:'{"requestId":"x","action":"retry","expected_revision":1}'})])test(`actual TaskRow blocks corrupt or foreign intent ${JSON.stringify(initial)}`,()=>{const h=harness(initial);assert.equal(h.button().props.disabled,true);assert.equal(h.requests.length,0);assert.equal(h.storage.get(h.key),initial)});
test('actual late 4xx preserves another pending body',async()=>{const h=harness();h.button().props.onClick();h.storage.set(h.key,'newer owned request');h.requests[0].reject(new h.APIError('revision changed'));await tick();assert.equal(h.storage.get(h.key),'newer owned request')});
