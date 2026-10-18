import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {createRequire} from 'node:module';
import vm from 'node:vm';
import {setImmediate as tick} from 'node:timers/promises';
const require=createRequire(import.meta.url),ts=require('../desktop/node_modules/typescript');
function load(file,deps,globals={}){const module={exports:{}};vm.runInNewContext(ts.transpileModule(readFileSync(new URL('../desktop/src/'+file,import.meta.url),'utf8'),{compilerOptions:{module:ts.ModuleKind.CommonJS,jsx:ts.JsxEmit.ReactJSX}}).outputText,{module,exports:module.exports,console,JSON,Error,AbortController,crypto,...globals,require:n=>deps[n]??{}});return module.exports;}
function find(n,p){if(Array.isArray(n)){for(const c of n){const hit=find(c,p);if(hit)return hit}}else if(n&&typeof n==='object'){if(p(n))return n;return find(n.props?.children,p)}}
const key='moviedesign.storyboard.pending.v1.p.film';
const board={panels:[{id:'p',timeMs:0,framing:'close',action:'speak',transition:'continuous'}],shots:[{id:'s',startMs:0,endMs:2000,panelIds:['p'],narrativeRole:'intro',startState:'start',endState:'end'}],timeline:{durationMs:2000,shotIds:['s'],audioIntent:'none'}};
function harness(map=new Map(),denied=false){let cursor=0,tree;const hooks=[],effects=[],writes=[],reads=[];
 const storage={getItem:k=>{if(denied)throw Error('denied');return map.get(k)??null},setItem:(k,v)=>map.set(k,v),removeItem:k=>map.delete(k)};
 const react={useState(init){const i=cursor++;if(!(i in hooks))hooks[i]=init;return[hooks[i],v=>hooks[i]=v]},useRef(init){return hooks[cursor++]??={current:init}},useEffect(fn,deps){const i=cursor++,old=hooks[i];if(!old||deps.some((v,j)=>v!==old.deps[j])){hooks[i]={deps};effects.push(()=>{old?.cleanup?.();hooks[i].cleanup=fn()})}}};
 class APIError extends Error{constructor(status){super('rejected');this.status=status}}
 const gateway={APIError,newRequestID:()=>crypto.randomUUID(),api:(p,path,init={})=>{if(init.method==='POST')return new Promise((resolve,reject)=>writes.push({p,path,body:init.body,resolve,reject}));reads.push(path);return Promise.resolve(path.includes('/pipeline/')?{pipelineId:'film',revision:1,currentRevision:1,storyboard:board,promptStatus:'pending'}:path.includes('/versions')?{resource_id:'r'}:path.includes('/resources')?{composition_id:'c'}:{name:'Film',type_id:'t'})}};
 const jsx=(type,props)=>({type,props});const helpers=load('features/chat/messages.ts',{});
 const {StoryboardReview}=load('features/pipeline/StoryboardReview.tsx',{'react':react,'react/jsx-runtime':{jsx,jsxs:jsx},'../../gateway':gateway,'../chat/messages':helpers},{localStorage:storage});
 const render=()=>{cursor=0;tree=StoryboardReview({projectID:'p',versionID:'v',text:'# review\n```json\n'+JSON.stringify(board)+'\n```'});while(effects.length)effects.shift()();return tree};
 const settle=async()=>{for(let i=0;i<6;i++){render();await tick()}render()};
 const button=label=>find(render(),n=>n.type==='button'&&n.props.children===label);
 const edit=()=>{button('编辑分镜 Markdown').props.onClick();return button('校验并保存新 revision')};
 render();return{map,writes,reads,settle,button,edit,render,APIError,unmount:()=>hooks.forEach(h=>h?.cleanup?.())};
}
test('actual storyboard component locks double submit, preserves unknown, restart replays frozen body',async()=>{const h=harness();await h.settle();const send=h.edit();send.props.onClick();send.props.onClick();await h.settle();assert.equal(h.writes.length,1);const wire=h.writes[0].body;h.writes[0].reject(Error('lost'));await h.settle();assert(h.map.has(key));const restarted=harness(h.map);await restarted.settle();assert.equal(restarted.writes.length,0);restarted.button('重试原分镜请求').props.onClick();await restarted.settle();assert.equal(restarted.writes[0].body,wire)});
test('duplicate nodes cannot overwrite an uncertain intent',async()=>{const map=new Map(),a=harness(map),b=harness(map);await a.settle();await b.settle();a.edit().props.onClick();b.edit().props.onClick();await a.settle();await b.settle();assert.equal(a.writes.length+b.writes.length,1);assert(map.has(key))});
test('late completion cannot clear newer local intent',async()=>{const h=harness();await h.settle();h.edit().props.onClick();await h.settle();h.unmount();h.map.set(key,'newer');h.writes[0].resolve({delivery:{status:'completed'}});await tick();assert.equal(h.map.get(key),'newer')});
test('storage corruption and denial block effects, show errors, never post',async()=>{for(const h of [harness(new Map([[key,'{']])),harness(new Map(),true)]){await h.settle();assert(h.button('编辑分镜 Markdown').props.disabled);assert(find(h.render(),n=>n.props?.role==='alert'));assert.equal(h.writes.length,0)}});
test('definite rejection frees only own intent and keeps candidate editable',async()=>{const h=harness();await h.settle();h.edit().props.onClick();await h.settle();h.writes[0].reject(new h.APIError(400));await h.settle();assert(!h.map.has(key));assert(!h.button('校验并保存新 revision').props.disabled)});
test('fresh malformed candidate fails before persistence or POST and stays editable',async()=>{const h=harness();await h.settle();h.edit();const candidate=JSON.parse(JSON.stringify(board));delete candidate.panels[0].framing;find(h.render(),n=>n.type==='textarea').props.onChange({target:{value:'# review\n```json\n'+JSON.stringify(candidate)+'\n```'}});h.button('校验并保存新 revision').props.onClick();await h.settle();assert.equal(h.writes.length,0);assert(!h.map.has(key));assert(find(h.render(),n=>n.props?.role==='alert'));assert(!find(h.render(),n=>n.type==='textarea').props.disabled);assert(!h.button('校验并保存新 revision').props.disabled)});
test('malformed nested frozen bodies fail closed before replay and retain exact evidence',async t=>{
 const source=harness();await source.settle();source.edit().props.onClick();await source.settle();source.writes[0].reject(Error('unknown'));await source.settle();const original=source.map.get(key);
 for(const [name,change] of Object.entries({
  'missing composition':b=>delete b.output.composition,
  'null composition type':b=>b.output.composition.type=null,
  'missing resource name':b=>delete b.output.resources[0].name,
  'placement key mismatch':b=>b.output.composition.placements[0].key='other',
  'missing placement role':b=>delete b.output.composition.placements[0].role,
  'extra typed property':b=>b.typedDocument.approved=true,
  'missing panel framing':b=>delete b.typedDocument.storyboard.panels[0].framing,
  'null shot':b=>b.typedDocument.storyboard.shots[0]=null,
  'unknown panel reference':b=>b.typedDocument.storyboard.shots[0].panelIds=['other'],
  'missing timeline audio intent':b=>delete b.typedDocument.storyboard.timeline.audioIntent,
  'Markdown typed disagreement':b=>b.content=b.content.replace('speak','different'),
  'base version mismatch':b=>b.output.resources[0].baseVersionId='other',
  'revision mismatch':b=>b.typedDocument.expectedRevision=2,
 }))await t.test(name,async()=>{
  const p=JSON.parse(original),b=JSON.parse(p.body);change(b);p.body=JSON.stringify(b);const raw=JSON.stringify(p),h=harness(new Map([[key,raw]]));await h.settle();const retry=h.button('重试原分镜请求');if(retry&&!retry.props.disabled)retry.props.onClick();await h.settle();if(h.writes[0]){h.writes[0].reject(new h.APIError(400));await h.settle()}
  assert.equal(h.writes.length,0,'corrupt stored body must never POST');assert.equal(h.map.get(key),raw,'retain exact corrupted record');assert(find(h.render(),n=>n.props?.role==='alert'));h.unmount();
 });source.unmount();
});
