import assert from 'node:assert/strict';
import { writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { connect } from './webview-cdp.mjs';
const [port, dir, origin] = process.argv.slice(2);
const { call, evaluate, wait, requests, exceptions, close } = await connect(port, origin);
try {
  await wait(`document.body?.innerText.includes('本地服务已连接')`);
  const get = path => evaluate(`fetch('http://127.0.0.1:8001${path}').then(async r=>({status:r.status,body:await r.json()}))`);
  for (const path of ['/api/health', '/api/projects?offset=0&limit=50', '/api/providers']) assert.equal((await get(path)).status, 200, path);
  for (let i=0;i<3;i++) { const result=await get('/api/agent/status');assert.equal(result.status,200);assert.equal(result.body.host_ready,false);assert.equal(result.body.active,null);assert.match(result.body.message,/准备失败/); }
  await evaluate(`[...document.querySelectorAll('button')].find(x=>x.textContent==='供应商设置').click()`);
  await wait(`document.body.innerText.includes('agent 配置目录准备失败')`);
  await evaluate(`document.querySelector('[aria-label="LLM 连接设置"]').scrollIntoView()`);
  assert.equal(requests.filter(x=>x.url.startsWith('http://127.0.0.1:4096')).length,0);
  assert.deepEqual(exceptions,[]);
  await writeFile(join(dir,'agent-prepare.png'),Buffer.from((await call('Page.captureScreenshot',{format:'png'})).data,'base64'));
  await writeFile(join(dir,'agent-prepare.json'),JSON.stringify({status:await get('/api/agent/status'),requests:requests.map(({method,url})=>({method,url})),exceptions},null,2));
} catch(error) { await writeFile(join(dir,'agent-prepare-failure.txt'),String(error)+'\n'+await evaluate('document.body?.innerText'));throw error; }
finally { close(); }
