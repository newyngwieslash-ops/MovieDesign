import assert from 'node:assert/strict';
import { writeFile, readFile } from 'node:fs/promises';
import { join } from 'node:path';
import { createServer } from 'node:http';
import { connect } from './webview-cdp.mjs';
const [port, dir, origin, phase = 'initial'] = process.argv.slice(2);
const { call, evaluate, wait, requests, exceptions, storageEvents, close } = await connect(port, origin);
const get = path => evaluate(`fetch('http://127.0.0.1:8001${path}').then(r=>r.json())`);
const click = async text => { await wait(`(()=>{const b=[...document.querySelectorAll('button')].find(x=>x.textContent===${JSON.stringify(text)});return b&&!b.disabled})()`); await evaluate(`[...document.querySelectorAll('button')].find(x=>x.textContent===${JSON.stringify(text)}).click()`); };
const input = (text, value) => evaluate(`(()=>{const label=[...document.querySelectorAll('label')].find(x=>x.textContent.startsWith(${JSON.stringify(text)}));const e=label.querySelector('input');Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(e,${JSON.stringify(value)});e.dispatchEvent(new Event('input',{bubbles:true}))})()`);
let fixture;
try {
 await wait(`document.body?.innerText.includes('本地服务已连接')`);await click('供应商设置');
 await wait(`document.body.innerText.includes('聊天推理尚未开放')`);
 const initial=await get('/api/agent/status');assert(initial.host_ready);assert.equal(initial.tools_ready,true);
 for(const path of ['/global/health','/config','/agent','/session','/doc']) { const res=await fetch('http://127.0.0.1:4096'+path,{headers:{Origin:origin}});assert.equal(res.status,401,path);assert.equal(res.headers.get('access-control-allow-origin'),origin);await res.arrayBuffer(); }
 if(phase==='initial'){
  let probes=0;fixture=createServer((req,res)=>{probes++;assert.equal(req.method,'GET');assert.equal(req.url,'/v1/models');assert.equal(req.headers.authorization,'Bearer synthetic-native-llm-key');res.writeHead(200,{'content-type':'application/json'});res.end(JSON.stringify({object:'list',data:[{id:'native-custom-model'}]}));});await new Promise(r=>fixture.listen(0,'127.0.0.1',r));
  await input('LLM Base URL',`http://127.0.0.1:${fixture.address().port}/v1`);await input('LLM Model ID','native-custom-model');await input('LLM API 密钥','synthetic-native-llm-key');await click('保存 LLM 连接');await wait(`document.body.innerText.includes('重启后生效')`);
  assert.equal((await get('/api/agent/status')).active,null);await click('免费测试已保存 LLM 连接');await wait(`document.body.innerText.includes('免费模型列表读取成功且列出所选模型')`);assert.equal(probes,1);
  await writeFile(join(dir,'llm-expected.json'),JSON.stringify(await get('/api/agent/settings')));
  await input('LLM Model ID','native-next-model');await input('LLM API 密钥','synthetic-native-llm-rotated');
  await evaluate(`window.originalFetch=window.fetch;window.fetch=async(...args)=>{const r=await window.originalFetch(...args);if(String(args[0]).includes('/api/agent/settings')&&args[1]?.method==='POST'&&r.ok)throw Error('controlled response loss');return r;}`);
  await click('保存 LLM 连接');await wait(`document.body.innerText.includes('查询 LLM 原回执')`);
 }else{
  const expected=JSON.parse(await readFile(join(dir,'llm-expected.json'),'utf8'));assert(expected.has_key);
  assert.equal(initial.active.model_id,'native-next-model');assert.equal(initial.pending_changes,false);assert.equal(requests.filter(x=>x.method==='POST'&&x.url.startsWith('http://127.0.0.1:8001/api/')).length,0);
  await click('查询 LLM 原回执');await wait(`localStorage.getItem('moviedesign.llm.pending.v1')===null`);
  assert.equal(requests.filter(x=>x.method==='POST'&&x.url.startsWith('http://127.0.0.1:8001/api/')).length,0,'recovery only reads receipt');
 }
 for(const secret of ['synthetic-native-llm-key','synthetic-native-llm-rotated']){assert(!JSON.stringify(await get('/api/agent/settings')).includes(secret));assert(!JSON.stringify(await evaluate(`({...localStorage})`)).includes(secret));assert(!JSON.stringify(storageEvents).includes(secret));}
 assert.equal(requests.filter(x=>x.url.startsWith('http://127.0.0.1:4096')).length,0,'renderer only talks to gateway');assert.deepEqual(exceptions,[]);
 await writeFile(join(dir,`agent-${phase}.png`),Buffer.from((await call('Page.captureScreenshot',{format:'png'})).data,'base64'));
 await writeFile(join(dir,`agent-${phase}.json`),JSON.stringify({phase,status:await get('/api/agent/status'),settings:await get('/api/agent/settings'),requests:requests.map(({method,url})=>({method,url})),exceptions},null,2));
 console.log(`M5.2 native agent ${phase} passed: ${dir}`);
}catch(error){await writeFile(join(dir,`agent-failure-${phase}.txt`),String(error)+'\n'+JSON.stringify(requests.map(({method,url})=>({method,url})))+'\n'+await evaluate('document.body.innerText'));throw error;}finally{close();if(fixture)await new Promise(r=>fixture.close(r));}
