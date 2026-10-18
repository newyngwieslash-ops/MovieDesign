import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { writeFile, readFile } from 'node:fs/promises';
import { join } from 'node:path';
import { connect } from './webview-cdp.mjs';
const [port, dir, origin, phase = 'initial'] = process.argv.slice(2);
const cdp = await connect(port, origin), {call, evaluate, wait, requests, exceptions, storageEvents, close} = cdp;
const click = async text => { await wait(`(()=>{const e=[...document.querySelectorAll('button')].find(e=>e.textContent.trim()===${JSON.stringify(text)});return e&&!e.disabled})()`);return evaluate(`(()=>{const e=[...document.querySelectorAll('button')].find(e=>e.textContent.trim()===${JSON.stringify(text)});if(!e||e.disabled)throw Error('button unavailable');e.click()})()`); };
const input = (label,value) => evaluate(`(()=>{const e=document.querySelector('input[aria-label=${JSON.stringify(label)}]');Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(e,${JSON.stringify(value)});e.dispatchEvent(new Event('input',{bubbles:true}))})()`);
const get = () => evaluate(`fetch('http://127.0.0.1:8001/api/providers').then(r=>r.json())`);
const settled = () => wait(`!document.querySelector('.provider-settings fieldset').disabled`);
let fixture, fixtureCalls=0;
try {
 await wait(`document.body.innerText.includes('本地服务已连接')`);await click('供应商设置');
 if(phase==='initial'){
  fixture=createServer((req,res)=>{fixtureCalls++;assert.equal(req.method,'GET');assert.equal(req.url,'/prefix/v1/models');assert.equal(req.headers.authorization,'Bearer native-synthetic-secret');res.writeHead(401,{'content-type':'application/json'});res.end(JSON.stringify({error:{message:'Do not echo native-synthetic-secret'}}))});await new Promise(r=>fixture.listen(0,'127.0.0.1',r));
  await input('供应商名称','本地测试');await input('供应商 Base URL',`http://127.0.0.1:${fixture.address().port}/prefix/v1`);await input('供应商模型','native-image');await input('供应商密钥','native-synthetic-secret');await click('保存供应商');await settled();
  assert.equal(await evaluate(`document.querySelector('input[aria-label="供应商密钥"]').value`),'');let list=await get();assert.equal(list.items.length,1);assert(list.items[0].has_key);assert(!JSON.stringify(list).includes('native-synthetic-secret'));
  await click('设为默认 本地测试');await settled();await click('免费测试 本地测试');await settled();assert.equal(fixtureCalls,1);await wait(`document.body.innerText.includes('远端拒绝凭据或访问权限')`);assert.equal(await evaluate(`getComputedStyle(document.querySelector('.provider-message')).color`),'rgb(32, 40, 23)');assert(!await evaluate(`document.body.innerText.includes('native-synthetic-secret')`));
  await click('新增供应商');await input('供应商名称','云端配置');await input('供应商 Base URL','https://api.example.com/v1');await input('供应商模型','configured-image');
  await evaluate(`window.realFetch=window.fetch;window.providerDrop=true;window.fetch=async(...args)=>{if(window.providerDrop&&String(args[0]).includes('/api/providers')&&args[1]?.method==='POST'){window.providerDrop=false;const r=await window.realFetch(...args);if(r.ok)throw Error('controlled response loss');return r}return window.realFetch(...args)}`);
  await click('保存供应商');await wait(`document.body.innerText.includes('结果待确认')`);const original=requests.filter(r=>r.method==='POST'&&r.url.includes('/api/providers')).at(-1).postData;
  await click('创作画布');await wait(`document.querySelector('.provider-settings').parentElement.hidden`);await click('供应商设置');await wait(`document.body.innerText.includes('确认原请求')`);await click('确认原请求');await settled();assert.equal(requests.filter(r=>r.method==='POST'&&r.url.includes('/api/providers')).at(-1).postData,original);
  await click('设为默认 云端配置');await settled();list=await get();assert.equal(list.items.filter(v=>v.default_for==='image')[0].config.name,'云端配置');await writeFile(join(dir,'providers-expected.json'),JSON.stringify(list));
  // Commit a key-changing update, lose the response, and exit with only nonsecret
  // original identity persisted. has_key alone cannot prove this rotation landed.
  await click('编辑 本地测试');await input('供应商密钥','native-synthetic-rotated');await evaluate(`window.providerDrop=true`);await click('保存供应商');await wait(`document.body.innerText.includes('结果待确认')`);
  const pending=await evaluate(`localStorage.getItem('moviedesign.providers.pending.v1')`);assert(pending);assert(!pending.includes('native-synthetic-rotated'));assert(!pending.includes('native-synthetic-secret'));
 }else{
  const expected=JSON.parse(await readFile(join(dir,'providers-expected.json'),'utf8'));assert.deepEqual(await get(),expected);assert.equal(requests.filter(r=>r.method==='POST'&&r.url.includes('/api/providers')).length,0,'restart never automatically resubmits');
  await wait(`document.body.innerText.includes('查询原回执')`);await click('查询原回执');await settled();assert.equal(await evaluate(`localStorage.getItem('moviedesign.providers.pending.v1')`),null);assert.equal(requests.filter(r=>r.method==='POST'&&r.url.includes('/api/providers')).length,0,'committed rotation reconciles without resubmission');
  await click('编辑 本地测试');assert.equal(await evaluate(`document.querySelector('input[aria-label="供应商密钥"]').value`),'');await input('供应商模型','updated-after-restart');await click('保存供应商');await settled();assert((await get()).items.find(v=>v.config.name==='本地测试').has_key,'empty key preserves stored secret');
  await click('设为默认 本地测试');await settled();assert.equal((await get()).items.find(v=>v.default_for==='image').config.name,'本地测试');
  await click('停用 本地测试');await settled();assert.equal((await get()).items.filter(v=>v.default_for).length,0);await click('移除 云端配置');await settled();assert.equal((await get()).items.length,1);
  // API accepted null/missing optional arrays must be editable without a React crash.
  for(const kind of ['image','video']){
   const seed=(await get()).items[0].config;const config={...seed,id:crypto.randomUUID(),name:`空数组-${kind}`,kind,protocol:kind==='image'?'openai-images':'ark-video',enabled:true,headers:{},pricing:null,capability:{max_prompt_bytes:100,max_count:1,max_references:0,sizes:null,edit:false,audio:false}};
   const result=await evaluate(`fetch('http://127.0.0.1:8001/api/providers',{method:'POST',headers:{'Content-Type':'application/json'},body:${JSON.stringify(JSON.stringify({requestId:crypto.randomUUID(),action:'create',config}))}}).then(async r=>({status:r.status,data:await r.json()}))`);assert.equal(result.status,200);assert.deepEqual(result.data.provider.config.capability.sizes,[]);assert.deepEqual(result.data.provider.config.capability.duration_seconds,[]);
   await click('刷新列表');await wait(`document.body.innerText.includes('编辑 空数组-${kind}')`);await click(`编辑 空数组-${kind}`);await settled();assert.equal(await evaluate(`document.querySelector('input[aria-label="供应商名称"]').value`),`空数组-${kind}`);
  }
  // Not-yet-sent key update: reload and replay only after original key reentry.
  await click('编辑 空数组-image');await input('供应商密钥','native-synthetic-reentry');
  await evaluate(`window.realFetch=window.fetch;window.fetch=async(...args)=>{if(String(args[0]).includes('/api/providers')&&args[1]?.method==='POST')throw Error('controlled before send');return window.realFetch(...args)}`);
  await click('保存供应商');await wait(`document.body.innerText.includes('结果待确认')`);const remembered=await evaluate(`JSON.parse(localStorage.getItem('moviedesign.providers.pending.v1'))`);
  await call('Page.reload');await wait(`document.body.innerText.includes('本地服务已连接')`);await click('供应商设置');await click('查询原回执');await wait(`document.body.innerText.includes('尚无原回执')`);
  await input('恢复原密钥','wrong-key');const before=requests.filter(r=>r.method==='POST'&&r.url.includes('/api/providers')).length;await click('确认原请求');await wait(`document.body.innerText.includes('相同密钥')`);assert.equal(requests.filter(r=>r.method==='POST'&&r.url.includes('/api/providers')).length,before);
  await input('恢复原密钥','native-synthetic-reentry');await click('确认原请求');await settled();const resent=JSON.parse(requests.filter(r=>r.method==='POST'&&r.url.includes('/api/providers')).at(-1).postData);const withoutKey={...resent};delete withoutKey.key;assert.deepEqual(withoutKey,JSON.parse(remembered.body));assert.equal(resent.key,'native-synthetic-reentry');assert.equal(await evaluate(`localStorage.getItem('moviedesign.providers.pending.v1')`),null);
 }
 if(phase==='recovery')assert.equal(await evaluate(`getComputedStyle(document.querySelector('.provider-message')).color`),'rgb(32, 40, 23)');
 assert(!JSON.stringify(await evaluate(`({local:{...localStorage},session:{...sessionStorage}})`)).includes('native-synthetic-secret'));for(const secret of ['native-synthetic-secret','native-synthetic-rotated','native-synthetic-reentry']){assert(!JSON.stringify(storageEvents).includes(secret));assert(!JSON.stringify(await evaluate(`({local:{...localStorage},session:{...sessionStorage}})`)).includes(secret));}assert.deepEqual(exceptions,[]);
 await writeFile(join(dir,`providers-${phase}.png`),Buffer.from((await call('Page.captureScreenshot',{format:'png'})).data,'base64'));
 // Never persist captured POST bodies; the synthetic key exercises the same boundary as a real one.
 await writeFile(join(dir,`providers-${phase}.json`),JSON.stringify({origin,phase,fixtureCalls,config:await get(),exceptions,requests:requests.map(r=>({method:r.method,url:r.url}))},null,2));
 console.log(`M4.1 native providers ${phase} passed: ${dir}`);
}catch(error){await writeFile(join(dir,`providers-failure-${phase}.txt`),String(error)+'\n'+await evaluate('document.body.innerText'));throw error}finally{close();if(fixture)await new Promise(r=>fixture.close(r))}
