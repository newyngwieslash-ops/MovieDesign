import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { writeFile, readFile } from 'node:fs/promises';
import { join } from 'node:path';
import { connect } from './webview-cdp.mjs';
const [port, dir, origin, phase = 'initial'] = process.argv.slice(2);
const cdp = await connect(port, origin), {call, evaluate, wait, requests, exceptions, storageEvents, close} = cdp;
const click = text => evaluate(`(()=>{const e=[...document.querySelectorAll('button')].find(e=>e.textContent.trim()===${JSON.stringify(text)});if(!e||e.disabled)throw Error('button unavailable');e.click()})()`);
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
  await click('设为默认 本地测试');await settled();await click('免费测试 本地测试');await settled();assert.equal(fixtureCalls,1);await wait(`document.body.innerText.includes('远端拒绝凭据或访问权限')`);assert(!await evaluate(`document.body.innerText.includes('native-synthetic-secret')`));
  await click('新增供应商');await input('供应商名称','云端配置');await input('供应商 Base URL','https://api.example.com/v1');await input('供应商模型','configured-image');
  await evaluate(`window.realFetch=window.fetch;window.providerDrop=true;window.fetch=async(...args)=>{if(window.providerDrop&&String(args[0]).includes('/api/providers')&&args[1]?.method==='POST'){window.providerDrop=false;const r=await window.realFetch(...args);if(r.ok)throw Error('controlled response loss');return r}return window.realFetch(...args)}`);
  await click('保存供应商');await wait(`document.body.innerText.includes('结果待确认')`);const original=requests.filter(r=>r.method==='POST'&&r.url.includes('/api/providers')).at(-1).postData;
  await click('创作画布');await click('供应商设置');await wait(`document.body.innerText.includes('确认原请求')`);await click('确认原请求');await settled();assert.equal(requests.filter(r=>r.method==='POST'&&r.url.includes('/api/providers')).at(-1).postData,original);
  await click('设为默认 云端配置');await settled();list=await get();assert.equal(list.items.filter(v=>v.default_for==='image')[0].config.name,'云端配置');await writeFile(join(dir,'providers-expected.json'),JSON.stringify(list));
 }else{
  const expected=JSON.parse(await readFile(join(dir,'providers-expected.json'),'utf8'));assert.deepEqual(await get(),expected);assert.equal(requests.filter(r=>r.method==='POST'&&r.url.includes('/api/providers')).length,0,'restart never automatically resubmits');
  await click('编辑 本地测试');assert.equal(await evaluate(`document.querySelector('input[aria-label="供应商密钥"]').value`),'');await input('供应商模型','updated-after-restart');await click('保存供应商');await settled();assert((await get()).items.find(v=>v.config.name==='本地测试').has_key,'empty key preserves stored secret');
  await click('设为默认 本地测试');await settled();assert.equal((await get()).items.find(v=>v.default_for==='image').config.name,'本地测试');
  await click('停用 本地测试');await settled();assert.equal((await get()).items.filter(v=>v.default_for).length,0);await click('移除 云端配置');await settled();assert.equal((await get()).items.length,1);
 }
 assert(!JSON.stringify(await evaluate(`({local:{...localStorage},session:{...sessionStorage}})`)).includes('native-synthetic-secret'));assert(!JSON.stringify(storageEvents).includes('native-synthetic-secret'));assert.deepEqual(exceptions,[]);
 await writeFile(join(dir,`providers-${phase}.png`),Buffer.from((await call('Page.captureScreenshot',{format:'png'})).data,'base64'));
 // Never persist captured POST bodies; the synthetic key exercises the same boundary as a real one.
 await writeFile(join(dir,`providers-${phase}.json`),JSON.stringify({origin,phase,fixtureCalls,config:await get(),exceptions,requests:requests.map(r=>({method:r.method,url:r.url}))},null,2));
 console.log(`M4.1 native providers ${phase} passed: ${dir}`);
}catch(error){await writeFile(join(dir,`providers-failure-${phase}.txt`),String(error)+'\n'+await evaluate('document.body.innerText'));throw error}finally{close();if(fixture)await new Promise(r=>fixture.close(r))}
