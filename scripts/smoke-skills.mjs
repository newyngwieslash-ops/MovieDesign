// Actual release WebView + pinned owned OpenCode; synthetic local supplier only.
import assert from 'node:assert/strict';
import {createServer} from 'node:http';
import {readFile,writeFile} from 'node:fs/promises';
import {join} from 'node:path';
import {connect} from './webview-cdp.mjs';
const [port,dir,origin,phase='initial']=process.argv.slice(2);
const {call,evaluate,wait,requests,exceptions,close}=await connect(port,origin);
const click=async text=>{await wait(`(()=>{const b=[...document.querySelectorAll('button')].find(x=>x.textContent.trim()===${JSON.stringify(text)});return b&&!b.disabled})()`);await evaluate(`[...document.querySelectorAll('button')].find(x=>x.textContent.trim()===${JSON.stringify(text)}).click()`)};
const get=path=>evaluate(`fetch('http://127.0.0.1:8001'+${JSON.stringify(path)}).then(r=>r.json())`);
const fill=(label,value)=>evaluate(`(()=>{const e=[...document.querySelectorAll('label')].find(x=>x.textContent.startsWith(${JSON.stringify(label)})).querySelector('input');Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(e,${JSON.stringify(value)});e.dispatchEvent(new Event('input',{bubbles:true}))})()`);
const select=async value=>evaluate(`(()=>{const e=document.querySelector('[aria-label="创作方法"]');Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype,'value').set.call(e,${JSON.stringify(value)});e.dispatchEvent(new Event('change',{bubbles:true}))})()`);
const send=async text=>{await evaluate(`document.querySelector('.chat-composer .tiptap').editor.commands.setContent({type:'doc',content:[{type:'paragraph',content:[{type:'text',text:${JSON.stringify(text)}}]}]})`);await click('发送消息');await wait(`![...document.querySelectorAll('.chat-panel button')].some(b=>b.textContent==='发送中')`)};
let fixture,expected,models=[],releaseHold;
async function supplier(listenPort=0){
 fixture=createServer(async(req,res)=>{
  let raw='';for await(const chunk of req)raw+=chunk;
  if(req.method==='GET'){res.writeHead(200,{'content-type':'application/json'});res.end('{"data":[{"id":"skills-fixture"}]}');return}
  assert.equal(req.url,'/v1/chat/completions');assert.equal(req.headers.authorization,'Bearer synthetic-skills-key');
  const body=JSON.parse(raw),last=body.messages.filter(m=>m.role==='user').at(-1),prompt=typeof last?.content==='string'?last.content:(last?.content??[]).filter(p=>p.type==='text').map(p=>p.text).join('\n');
  const names=(body.tools??[]).map(t=>t.function.name).sort();assert.equal(names.length,19);assert(names.every(n=>n==='question'||n.startsWith('design_design_')));
  models.push({prompt,names,messages:body.messages});
  if(prompt.includes('hold-owned'))await new Promise(resolve=>{releaseHold=resolve;req.on('close',resolve)});
  if(res.destroyed)return;
  res.writeHead(200,{'content-type':'text/event-stream'});
  for(const [delta,finish] of [[{role:'assistant',content:'合成供应商已收到冻结方法，仅验证传输。'},null],[{},'stop']])res.write(`data: ${JSON.stringify({id:'fixture',object:'chat.completion.chunk',created:1,model:'skills-fixture',choices:[{index:0,delta,finish_reason:finish}]})}\n\n`);
  res.end('data: [DONE]\n\n');
 });await new Promise(r=>fixture.listen(listenPort,'127.0.0.1',r));return fixture.address().port;
}
const waitModels=async n=>{for(let i=0;i<150&&models.length<n;i++)await new Promise(r=>setTimeout(r,100));assert.equal(models.length,n)};
const history=()=>get(`/api/chat/sessions/${expected.s}/messages?project_id=${expected.p}`);
const waitDone=async n=>wait(`fetch('http://127.0.0.1:8001/api/chat/sessions/${expected.s}/messages?project_id=${expected.p}').then(r=>r.json()).then(r=>r.items.filter(m=>m.role==='user'&&m.state==='done').length===${n})`);
function assertMethod(model,skill){
 const first=model.messages.filter(m=>m.role==='user').at(-1).content[0];
 const prefix=`当前唯一采用的创作方法（取代历史方法选择；仅方法，不授予工具权限或任何后续批准）：${skill.name} / ${skill.version} / ${skill.revision}\n${skill.body}`+Object.keys(skill.references).sort().map(name=>`\n\n受管引用 ${name}\n${skill.references[name]}`).join('');
 assert.equal(first.type,'text');assert.equal(first.text,prefix,'entire native method prefix and all reference bytes');
}
try {
 await wait(`document.body?.innerText.includes('本地服务已连接')`);
 const listing=await get('/api/skills');assert.equal(listing.items.length,2);assert.deepEqual(listing.coverage,{total:2,next_offset:2,exhausted:true});
 if(phase==='visual') {
  await wait(`!!document.querySelector('[aria-label="创作方法"]')`);
  await select('图片标注修复');await wait(`!!document.querySelector('.chat-panel details pre')`);
  await evaluate(`(()=>{const panel=document.querySelector('.chat-panel');panel.querySelector('details').open=true;panel.querySelector('[aria-label="创作方法"]').scrollIntoView({block:'start'})})()`);
  const visibility=await evaluate(`(()=>{const r=document.querySelector('[aria-label="创作方法"]').getBoundingClientRect();return {top:r.top,bottom:r.bottom,height:innerHeight,selected:document.querySelector('[aria-label="创作方法"]').value}})()`);
  assert(visibility.top>=0&&visibility.bottom<=visibility.height);assert.equal(visibility.selected,'图片标注修复');assert.equal(requests.filter(r=>r.method==='POST'&&r.url.includes('/api/')).length,0);
  await writeFile(join(dir,'skills-visual.png'),Buffer.from((await call('Page.captureScreenshot',{format:'png'})).data,'base64'));
  await writeFile(join(dir,'skills-visual-result.json'),JSON.stringify({qualification:'read-only desktop visual supplement; no supplier fixture or inference',visibility,exceptions},null,2));
  console.log(`M6.1 read-only skills visual passed: ${dir}`);
 }else
 if(phase==='initial'){
  expected={supplierPort:await supplier()};
  await click('供应商设置');await fill('LLM Base URL',`http://127.0.0.1:${expected.supplierPort}/v1`);await fill('LLM Model ID','skills-fixture');await fill('LLM API 密钥','synthetic-skills-key');await click('保存 LLM 连接');await wait(`document.body.innerText.includes('重启后生效')`);await click('创作画布');
  await evaluate(`(()=>{const e=document.querySelector('#project-name');Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(e,'M6.1 技能验收');e.dispatchEvent(new Event('input',{bubbles:true}))})()`);await evaluate(`document.querySelector('.project-picker form').requestSubmit()`);await wait(`!!document.querySelector('.chat-panel')`);
  expected.p=await evaluate(`document.querySelector('#project').value`);await click('新建对话');await wait(`!!document.querySelector('[aria-label="当前对话"]').value`);expected.s=await evaluate(`document.querySelector('[aria-label="当前对话"]').value`);
  await select('口播视频');await wait(`document.querySelector('.chat-panel').innerText.includes('查看制作方法与受管引用')`);await select('__continue__');
 }else{
  expected=JSON.parse(await readFile(join(dir,'skills-expected.json'),'utf8'));await supplier(expected.supplierPort);
  await wait(`document.querySelector('[aria-label="当前对话"]')?.value===${JSON.stringify(expected.s)}`);
  assert.equal(requests.filter(r=>r.method==='POST'&&r.url.includes('/api/')).length,0,'restart never writes');
  if(phase==='recovery'){
   expected.oral=await get('/api/skills?name='+encodeURIComponent('口播视频'));expected.image=await get('/api/skills?name='+encodeURIComponent('图片标注修复'));
   await send('用这张图做个15秒口播视频配轻快音乐');await waitDone(1);await waitModels(1);assertMethod(models[0],expected.oral);
   const oralFile=join(dir,'skills','口播视频','references','storyboard.md');await writeFile(oralFile,(await readFile(oralFile,'utf8'))+'\nLOCAL_ORAL_AFTER_ADMISSION\n');
   await send('hold-owned 继续制作');await waitModels(2);assertMethod(models[1],expected.oral);assert(!models[1].prompt.includes('LOCAL_ORAL_AFTER_ADMISSION'));
   await select('图片标注修复');await send('按区域标注修复图片');const queued=(await history()).items.find(m=>m.role==='user'&&m.state==='queued');assert(queued);assert.equal(queued.skill.revision,expected.image.revision);
   const imageFile=join(dir,'skills','图片标注修复','references','annotations.md');await writeFile(imageFile,(await readFile(imageFile,'utf8'))+'\nLOCAL_IMAGE_AFTER_QUEUE\n');
   releaseHold();await waitDone(3);await waitModels(3);assertMethod(models[2],expected.image);assert(!models[2].prompt.includes('LOCAL_IMAGE_AFTER_QUEUE'));
   expected.queuedID=queued.id;expected.originals=(await history()).items.filter(m=>m.role==='user').map(m=>({id:m.id,revision:m.skill?.revision,content:m.content}));
   const list=await get('/api/skills');assert(list.items.every(s=>s.modified));
  }else{
   assert.equal(models.length,0);await send('继续刚才的工作');await waitDone(4);await waitModels(1);assertMethod(models[0],expected.image);assert(!models[0].prompt.includes('LOCAL_IMAGE_AFTER_QUEUE'));
   const current=await get('/api/skills?name='+encodeURIComponent('图片标注修复'));assert.notEqual(current.revision,expected.image.revision);assert(current.modified);
   await select('图片标注修复');await send('显式采用当前本地方法');await waitDone(5);await waitModels(2);assertMethod(models[1],current);
   await select('');await send('口播但是明确停用方法');await waitDone(6);await waitModels(3);assert(!models[2].prompt.includes(current.body));assert(models[2].prompt.includes('本轮未采用创作技能'));
   const messages=(await history()).items.filter(m=>m.role==='user');assert.deepEqual(messages.slice(0,3).map(m=>({id:m.id,revision:m.skill?.revision,content:m.content})),expected.originals);assert.equal(messages.at(-1).skill,undefined);
  }
  assert.equal(requests.filter(r=>r.url.startsWith('http://127.0.0.1:4096')).length,0);assert.deepEqual(exceptions,[]);
 }
 if(phase!=='visual'){
 await writeFile(join(dir,'skills-expected.json'),JSON.stringify(expected,null,2));
 await writeFile(join(dir,`skills-${phase}-result.json`),JSON.stringify({real_inference:'UNEXECUTED',qualification:'actual pinned native + production WebView; synthetic local provider',listing,models,exceptions},null,2));
 await writeFile(join(dir,`skills-${phase}.png`),Buffer.from((await call('Page.captureScreenshot',{format:'png'})).data,'base64'));
 console.log(`M6.1 actual native skills ${phase} passed: ${dir}`);
 }
}catch(e){await writeFile(join(dir,`skills-${phase}-failure.json`),JSON.stringify({error:String(e),body:await evaluate('document.body.innerText').catch(()=>''),models,exceptions},null,2));throw e}
finally{releaseHold?.();close();if(fixture){fixture.closeAllConnections();await new Promise(r=>fixture.close(r))}}
