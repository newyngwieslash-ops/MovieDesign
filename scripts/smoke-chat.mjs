// Actual release Tauri/WebView + pinned owned OpenCode, synthetic loopback supplier.
// This is protocol/UI evidence, not real LLM inference.
import assert from 'node:assert/strict';
import {createServer} from 'node:http';
import {readFile,writeFile} from 'node:fs/promises';
import {join} from 'node:path';
import {connect} from './webview-cdp.mjs';
const [port,dir,origin,phase='initial']=process.argv.slice(2);
const {call,evaluate,wait,requests,exceptions,close}=await connect(port,origin);
const ed=`document.querySelector('.chat-composer .tiptap').editor`;
const click=async(text,scope='document')=>{await wait(`(()=>{const b=[...${scope}.querySelectorAll('button')].find(x=>x.textContent.trim()===${JSON.stringify(text)});return b&&!b.disabled})()`);await evaluate(`[...${scope}.querySelectorAll('button')].find(x=>x.textContent.trim()===${JSON.stringify(text)}).click()`);};
const input=(selector,value)=>evaluate(`(()=>{const e=document.querySelector(${JSON.stringify(selector)});Object.getOwnPropertyDescriptor(${selector.startsWith('textarea')?'HTMLTextAreaElement':'HTMLInputElement'}.prototype,'value').set.call(e,${JSON.stringify(value)});e.dispatchEvent(new Event('input',{bubbles:true}));})()`);
const key=async(key,modifiers=0)=>{const code=key===' '?'Space':key,vk=({Enter:13,Backspace:8,Delete:46,ArrowDown:40,ArrowUp:38,Tab:9,' ':32})[key];await call('Input.dispatchKeyEvent',{type:'keyDown',key,code,modifiers,windowsVirtualKeyCode:vk,nativeVirtualKeyCode:vk,...(key==='Enter'?{text:'\r',unmodifiedText:'\r'}:key===' '?{text:' ',unmodifiedText:' '}:{})});await call('Input.dispatchKeyEvent',{type:'keyUp',key,code,modifiers,windowsVirtualKeyCode:vk,nativeVirtualKeyCode:vk});};
const get=(p,path)=>evaluate(`fetch('http://127.0.0.1:8001${path}${path.includes('?')?'&':'?'}project_id=${p}').then(r=>r.json())`);
const writes=()=>requests.filter(r=>r.method==='POST'&&r.url.startsWith('http://127.0.0.1:8001/api/')&&new URL(r.url).pathname!=='/api/canvas/content');
let fixture,modelRequests=[],releaseHold,expected;
async function supplier(listenPort=0){
 fixture=createServer(async(req,res)=>{
  let raw='';for await(const chunk of req)raw+=chunk;const body=JSON.parse(raw||'{}');
  if(req.method==='GET'){res.writeHead(200,{'content-type':'application/json'});res.end('{"data":[{"id":"chat-fixture"}]}');return;}
  assert.equal(req.url,'/v1/chat/completions');assert.equal(req.headers.authorization,'Bearer synthetic-chat-key');
  modelRequests.push({messages:body.messages,model:body.model});
  const lastUser=body.messages.filter(m=>m.role==='user').at(-1),prompt=typeof lastUser?.content==='string'?lastUser.content:JSON.stringify(lastUser?.content);
  const last=body.messages.at(-1),toolResult=last?.role==='tool';
  if(prompt.includes('hold')&&!toolResult)await new Promise(resolve=>{releaseHold=resolve;req.on('close',resolve);});
  if(res.destroyed)return;
  res.writeHead(200,{'content-type':'text/event-stream'});
  const emit=(delta,finish=null)=>res.write(`data: ${JSON.stringify({id:'chat-fixture',object:'chat.completion.chunk',created:1,model:'chat-fixture',choices:[{index:0,delta,finish_reason:finish}]})}\n\n`);
  if(prompt.includes('question')&&!toolResult){emit({role:'assistant',tool_calls:[{index:0,id:`question_${modelRequests.length}`,type:'function',function:{name:'question',arguments:JSON.stringify({questions:[{header:'原生人工选择',question:'请选择继续或拒绝',options:[{label:'Continue',description:'Local fixture only'}]}]})}}]});emit({},'tool_calls');}
  else{emit({role:'assistant',content:'哈哈 <img src=x onerror="window.chatUnsafe=true">'});emit({content:' 合成供应商完成。'});emit({},'stop');}
  res.end('data: [DONE]\n\n');
 });await new Promise(resolve=>fixture.listen(listenPort,'127.0.0.1',resolve));return fixture.address().port;
}
async function send(text){await evaluate(`${ed}.commands.setContent({type:'doc',content:[{type:'paragraph',content:[{type:'text',text:${JSON.stringify(text)}}]}]})`);await click('发送消息');await wait(`![...document.querySelectorAll('.chat-panel button')].some(b=>b.textContent==='发送中')`);}
async function waitFixture(count){for(let i=0;i<100&&modelRequests.length<count;i++)await new Promise(resolve=>setTimeout(resolve,200));assert.equal(modelRequests.length,count);}
try{
 await wait(`document.body?.innerText.includes('本地服务已连接')`);
 if(phase==='initial'){
  const supplierPort=await supplier();
  await click('供应商设置');
  for(const [label,value] of [['LLM Base URL',`http://127.0.0.1:${supplierPort}/v1`],['LLM Model ID','chat-fixture'],['LLM API 密钥','synthetic-chat-key']])await evaluate(`(()=>{const e=[...document.querySelectorAll('label')].find(x=>x.textContent.startsWith(${JSON.stringify(label)})).querySelector('input');Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(e,${JSON.stringify(value)});e.dispatchEvent(new Event('input',{bubbles:true}));})()`);
  await click('保存 LLM 连接');await wait(`document.body.innerText.includes('重启后生效')`);await click('创作画布');
  await input('#project-name','聊天原生验收');await evaluate(`document.querySelector('.project-picker form').requestSubmit()`);await wait(`!!document.querySelector('.chat-panel')`);const p=await evaluate(`document.querySelector('#project').value`);
  const png=await evaluate(`(()=>{const c=document.createElement('canvas');c.width=600;c.height=400;const x=c.getContext('2d');x.fillStyle='#c4d4b7';x.fillRect(0,0,600,400);return c.toDataURL()})()`);const path=join(dir,'chat-source.png');await writeFile(path,Buffer.from(png.split(',')[1],'base64'));
  const {root}=await call('DOM.getDocument'),{nodeId}=await call('DOM.querySelector',{nodeId:root.nodeId,selector:'input[aria-label="导入素材"]'});await call('DOM.setFileInputFiles',{nodeId,files:[path]});await wait(`document.body.innerText.includes('导入完成')`);
  await click('新建对话');await wait(`document.querySelector('[aria-label="当前对话"]').value!==''`);const s=await evaluate(`document.querySelector('[aria-label="当前对话"]').value`),asset=(await get(p,'/api/assets')).items[0],version=(await get(p,'/api/versions')).items[0];
  expected={p,s,asset,version,supplierPort};await writeFile(join(dir,'chat-expected.json'),JSON.stringify(expected));
 }else{
  expected=JSON.parse(await readFile(join(dir,'chat-expected.json'),'utf8'));const {p,s,asset,version}=expected;
  await wait(`document.querySelector('[aria-label="当前对话"]')?.value===${JSON.stringify(s)}`);
  assert.equal(writes().length,0,'physical restart no automatic controls');await supplier(expected.supplierPort);
  if(phase==='fix'){
   await send('hold first queue-fix');await wait(`document.querySelector('.chat-history').innerText.includes('已受理')`);
   await send('hold queued queue-fix');await wait(`document.querySelector('.chat-history').innerText.includes('已排队')`);
   const beforeDraft=writes().length,cancelKey=`moviedesign.chat.cancel.v1.${p}`;
   await evaluate(`${ed}.commands.setContent('<p>保留我的草稿</p>')`);
   assert.equal(await evaluate(`[...document.querySelectorAll('.chat-panel button')].find(b=>b.textContent==='撤回并编辑').disabled`),true);
   await evaluate(`[...document.querySelectorAll('.chat-panel button')].find(b=>b.textContent==='撤回并编辑').click()`);
   assert.equal(await evaluate(`${ed}.getText()`),'保留我的草稿');assert.equal(writes().length,beforeDraft);assert.equal(await evaluate(`localStorage.getItem('${cancelKey}')`),null);await click('清空草稿');
   // Transfer real image-editor geometry, then leave only its immutable annotation draft.
   await evaluate(`document.querySelector('.asset-card').click()`);await click('编辑图像');await wait(`!!document.querySelector('.editor-stage canvas')`);await evaluate(`document.querySelector('.image-editor').scrollIntoView()`);await click('区域标注');
   const rect=await evaluate(`(()=>{const r=document.querySelector('.editor-stage canvas').getBoundingClientRect();return {x:r.x,y:r.y}})()`);
   for(const [type,x,y] of [['mousePressed',30,40],['mouseMoved',140,120],['mouseReleased',140,120]])await call('Input.dispatchMouseEvent',{type,x:rect.x+x,y:rect.y+y,button:'left',buttons:type==='mouseMoved'?1:undefined,clickCount:1});
   await wait(`!!document.querySelector('textarea[aria-label="区域说明"]')`);await call('Input.insertText',{text:'保留标注草稿'});await key('Enter');await wait(`document.querySelectorAll('[aria-label="区域标注列表"] li').length===1`);
   await click(await evaluate(`[...document.querySelectorAll('.chat-panel button')].find(b=>b.textContent.startsWith('附加当前标注')).textContent`));await evaluate(`${ed}.commands.clearContent()`);
   await wait(`[...document.querySelectorAll('.chat-panel button')].find(b=>b.textContent==='撤回并编辑').disabled`);
   assert.equal(await evaluate(`${ed}.isEmpty`),true);assert.equal(await evaluate(`[...document.querySelectorAll('.chat-panel button')].find(b=>b.textContent==='移除草稿标注').disabled`),false);
   await evaluate(`[...document.querySelectorAll('.chat-panel button')].find(b=>b.textContent==='撤回并编辑').click()`);assert.equal(writes().length,beforeDraft);assert.equal(await evaluate(`localStorage.getItem('${cancelKey}')`),null);assert(await evaluate(`document.querySelector('.chat-composer').innerText.includes('保留标注草稿')`));await click('清空草稿');
   const queued=(await get(p,`/api/chat/sessions/${s}/messages`)).items.find(m=>m.state==='queued');assert(queued);
   // Hold only the browser's already explicit cancel until the real server dequeues.
   await evaluate(`window.nativeFetch=window.fetch;window.fetch=async(...a)=>{if(String(a[0]).includes('/cancel')&&a[1]?.method==='POST'){await new Promise(resolve=>window.releaseWithdrawal=resolve);const r=await window.nativeFetch(...a);window.withdrawalResult={status:r.status,body:await r.clone().json()};return r;}return window.nativeFetch(...a);}`);
   await click('撤回并编辑');await wait(`typeof window.releaseWithdrawal==='function'`);const pending=await evaluate(`localStorage.getItem('${cancelKey}')`);assert.equal(JSON.parse(JSON.parse(pending).body).message_id,queued.id);
   await waitFixture(1);releaseHold();await wait(`fetch('http://127.0.0.1:8001/api/chat/sessions/${s}/messages?project_id=${p}').then(r=>r.json()).then(r=>r.items.some(m=>m.id==='${queued.id}'&&m.state==='accepted'))`);
   await evaluate(`window.releaseWithdrawal()`);await wait(`!!window.withdrawalResult`);expected.withdrawal=await evaluate(`window.withdrawalResult`);assert.equal(expected.withdrawal.status,409);assert.equal(expected.withdrawal.body.error.code,'invalid_input');assert.equal(expected.withdrawal.body.error.message,'Only queued messages can be withdrawn');
   await wait(`!localStorage.getItem('${cancelKey}')`);await wait(`document.querySelector('[data-message-id="${queued.id}"]').innerText.includes('已受理')`);
   assert.equal(writes().filter(r=>r.url.includes('/messages')).length,2,'rejection never submits replacement');assert.equal(await evaluate(`[...document.querySelectorAll('.chat-panel button')].find(b=>b.textContent==='取消本次对话及队列').disabled`),false);
   await evaluate(`window.fetch=window.nativeFetch`);await waitFixture(2);releaseHold();await wait(`document.querySelector('[data-message-id="${queued.id}"]').innerText.includes('已完成')`);
   await send('explicit fresh send after race');await wait(`document.querySelectorAll('.assistant-message').length===3`);const sent=writes().filter(r=>r.url.includes('/messages')).map(r=>JSON.parse(r.postData));assert.notEqual(sent[2].requestId,sent[1].requestId);assert.equal(sent[2].text,'explicit fresh send after race');
   expected.fix={textDraftPreserved:true,annotationOnlyPreserved:true,rejectedBody:JSON.parse(pending).body,explicitNewRequestId:sent[2].requestId};await writeFile(join(dir,'chat-expected.json'),JSON.stringify(expected));
  }else if(phase==='fix-restart'){
   assert.equal(await evaluate(`localStorage.getItem('moviedesign.chat.cancel.v1.${p}')`),null);assert.equal(writes().length,0);assert.equal(modelRequests.length,0);
   await evaluate(`${ed}.commands.setContent('<p>重启后仍可编辑</p>')`);await wait(`![...document.querySelectorAll('.chat-panel button')].find(b=>b.textContent==='发送消息').disabled`);assert.equal(writes().length,0);assert.equal(await evaluate(`[...document.querySelectorAll('.chat-panel button')].find(b=>b.textContent==='清空草稿').disabled`),false);
  }else if(phase==='recovery'){
   assert.equal((await evaluate(`fetch('http://127.0.0.1:8001/api/agent/status').then(r=>r.json())`)).active.model_id,'chat-fixture');
   await call('Page.bringToFront');await evaluate(`document.querySelector('.chat-panel').scrollIntoView();${ed}.commands.focus()`);await wait(`document.activeElement===document.querySelector('.chat-composer .tiptap')`);
   await call('Input.insertText',{text:'@'});await wait(`!!document.querySelector('.asset-suggestions [role="option"]')`);await key('ArrowDown');await key('Enter');await wait(`!!document.querySelector('.tiptap [data-type="mention"]')`);
   assert.equal(await evaluate(`document.querySelector('.tiptap [data-type="mention"]').dataset.path`),`asset:${asset.id}`);assert(await evaluate(`document.querySelector('.tiptap [data-type="mention"] img').complete`));
   // Real keyboard deletion removes the adjacent mention atom and no editor shape.
   await key('Backspace');await key('Backspace');await wait(`!document.querySelector('.tiptap [data-type="mention"]')`);
   await call('Input.imeSetComposition',{text:'中文',selectionStart:2,selectionEnd:2});const beforeIME=writes().length;
   await evaluate(`document.querySelector('.tiptap').dispatchEvent(new KeyboardEvent('keydown',{key:'Enter',isComposing:true,bubbles:true}))`);assert.equal(writes().length,beforeIME);await call('Input.insertText',{text:'中文'});await click('清空草稿');
   // Exercise the actual ImageEditor annotation callback, then freeze it in chat.
   await evaluate(`document.querySelector('.asset-card').click()`);await click('编辑图像');await wait(`!!document.querySelector('.editor-stage canvas')`);await evaluate(`document.querySelector('.image-editor').scrollIntoView()`);await click('区域标注');
   const rect=await evaluate(`(()=>{const r=document.querySelector('.editor-stage canvas').getBoundingClientRect();return {x:r.x,y:r.y}})()`);
   for(const [type,x,y] of [['mousePressed',30,40],['mouseMoved',140,120],['mouseReleased',140,120]])await call('Input.dispatchMouseEvent',{type,x:rect.x+x,y:rect.y+y,button:'left',buttons:type==='mouseMoved'?1:undefined,clickCount:1});
   await wait(`!!document.querySelector('textarea[aria-label="区域说明"]')`);await call('Input.insertText',{text:'保持这个区域'});await key('Enter');await wait(`document.querySelectorAll('[aria-label="区域标注列表"] li').length===1`);
   const attach=await evaluate(`[...document.querySelectorAll('.chat-panel button')].find(b=>b.textContent.startsWith('附加当前标注')).textContent`);await click(attach);
   await evaluate(`${ed}.commands.insertContent(' hold referenced');${ed}.chain().toggleBulletList().run();${ed}.commands.setContent({type:'doc',content:[{type:'blockquote',content:${ed}.getJSON().content}]})`);const doc=await evaluate(`${ed}.getJSON()`);assert(JSON.stringify(doc).includes('bulletList'));assert(JSON.stringify(doc).includes('blockquote'));
   await click('发送消息');await wait(`document.querySelector('.chat-history').innerText.includes('已受理')`);const sent=writes().filter(r=>r.url.includes('/messages')).at(-1),body=JSON.parse(sent.postData);assert.deepEqual(body.references,[asset.id]);assert.equal(body.annotations[0].assetId,asset.id);assert.equal(body.annotations[0].sourceVersionID,version.id);assert.equal(body.annotations[0].comment,'保持这个区域');
   await send('queued original');await wait(`document.querySelector('.chat-history').innerText.includes('已排队')`);await click('撤回并编辑');await wait(`![...document.querySelectorAll('.chat-panel button')].find(b=>b.textContent==='载入已撤回草稿')?.disabled`);await click('载入已撤回草稿');await evaluate(`${ed}.commands.insertContent(' revised')`);await click('发送消息');await wait(`document.querySelector('.chat-history').innerText.includes('已排队')`);
   const queuePosts=writes().filter(r=>r.url.includes('/messages')).map(r=>JSON.parse(r.postData));assert.notEqual(queuePosts[1].requestId,queuePosts[2].requestId);await click('撤回消息');await wait(`!document.querySelector('.chat-history').innerText.includes('已排队')`);
   releaseHold?.();await wait(`document.querySelector('.chat-history').innerText.includes('合成供应商完成')`);assert.equal(modelRequests.length,1);assert.equal(await evaluate(`window.chatUnsafe`),undefined);assert.equal(await evaluate(`document.querySelectorAll('.assistant-message img').length`),0);
   await send('question answer');await wait(`!!document.querySelector('.confirmation-card input[type="radio"]')`);
   const card=await evaluate(`document.querySelector('.confirmation-card:has(input[type="radio"])').dataset.questionId`);
   const scope=`document.querySelector('[data-question-id="${card}"]')`;
   await call('Page.bringToFront');await evaluate(`${scope}.scrollIntoView();${scope}.querySelector('input[type="radio"]').focus()`);await key(' ');assert.equal(await evaluate(`${scope}.querySelector('input[type="radio"]').checked`),true,'native Space selects option');await key('Tab');assert.equal(await evaluate(`document.activeElement.textContent`),'提交选择');await key('Enter');await wait(`${scope}.innerText.includes('已送达')`);await wait(`document.querySelectorAll('.assistant-message').length>=2`);
   await send('question reject');await wait(`[...document.querySelectorAll('.confirmation-card')].some(c=>c.querySelector('fieldset')&&!c.querySelector('fieldset').disabled)`);const pendingCard=await evaluate(`[...document.querySelectorAll('.confirmation-card')].find(c=>!c.querySelector('fieldset').disabled).dataset.questionId`);await click('拒绝',`document.querySelector('[data-question-id="${pendingCard}"]')`);await wait(`document.querySelector('[data-question-id="${pendingCard}"]').innerText.includes('已拒绝')`);
   await send('hold cancellation');await wait(`document.querySelector('.chat-history').innerText.includes('已受理')`);await click('取消本次对话及队列');releaseHold?.();await wait(`document.querySelector('.chat-history').innerText.includes('已取消')`);
   // Fresh session: lose a successful response and retain the exact original body.
   await click('新建对话');await wait(`document.querySelector('[aria-label="当前对话"]').value!==${JSON.stringify(s)}`);expected.s=await evaluate(`document.querySelector('[aria-label="当前对话"]').value`);
   await evaluate(`window.nativeFetch=window.fetch;window.dropSend=true;window.fetch=async(...a)=>{const r=await window.nativeFetch(...a);if(window.dropSend&&String(a[0]).includes('/messages')&&a[1]?.method==='POST'&&r.ok){window.dropSend=false;throw Error('controlled lost response')}return r;}`);
   await send('lost response original');await wait(`!!localStorage.getItem('moviedesign.chat.send.v1.${p}')`);expected.pending=await evaluate(`localStorage.getItem('moviedesign.chat.send.v1.${p}')`);expected.original=JSON.parse(expected.pending).body;
   const beforeReload=writes().length;
   await evaluate(`(()=>{const e=document.querySelector('#project');e.value='';e.dispatchEvent(new Event('change',{bubbles:true}));})()`);await wait(`!document.querySelector('.chat-panel')`);
   await evaluate(`(()=>{const e=document.querySelector('#project');e.value=${JSON.stringify(p)};e.dispatchEvent(new Event('change',{bubbles:true}));})()`);await wait(`document.body.innerText.includes('恢复原发送请求')`);assert.equal(writes().length,beforeReload);assert.equal(await evaluate(`localStorage.getItem('moviedesign.chat.send.v1.${p}')`),expected.pending);
   await call('Page.reload');await wait(`document.body.innerText.includes('恢复原发送请求')`);assert.equal(writes().length,beforeReload);
   await call('Network.emulateNetworkConditions',{offline:true,latency:0,downloadThroughput:-1,uploadThroughput:-1});await new Promise(r=>setTimeout(r,650));await call('Network.emulateNetworkConditions',{offline:false,latency:0,downloadThroughput:-1,uploadThroughput:-1});await new Promise(r=>setTimeout(r,1500));assert.equal(writes().length,beforeReload);assert(requests.filter(r=>r.url.includes('/api/events')).some(r=>r.headers['Last-Event-ID']||r.headers['last-event-id']));
   await click('恢复原发送请求');await wait(`!localStorage.getItem('moviedesign.chat.send.v1.${p}')`);assert.equal(writes().filter(r=>r.url.includes('/messages')).at(-1).postData,expected.original);
   // Persist a second true lost response for physical process restart proof.
   await evaluate(`window.nativeFetch=window.fetch;window.fetch=async(...a)=>{const r=await window.nativeFetch(...a);if(String(a[0]).includes('/messages')&&a[1]?.method==='POST'&&r.ok)throw Error('restart lost response');return r;}`);await send('restart original');await wait(`!!localStorage.getItem('moviedesign.chat.send.v1.${p}')`);expected.pending=await evaluate(`localStorage.getItem('moviedesign.chat.send.v1.${p}')`);expected.original=JSON.parse(expected.pending).body;await writeFile(join(dir,'chat-expected.json'),JSON.stringify(expected));
  }else{
   assert.equal(await evaluate(`localStorage.getItem('moviedesign.chat.send.v1.${p}')`),expected.pending);await click('恢复原发送请求');await wait(`!localStorage.getItem('moviedesign.chat.send.v1.${p}')`);assert.equal(writes()[0].postData,expected.original);assert.equal(modelRequests.length,0,'durable replay never repeats inference');
  }
 }
 assert.deepEqual(exceptions,[]);assert.equal(requests.filter(r=>r.url.startsWith('http://127.0.0.1:4096')).length,0);
 await evaluate(`document.querySelector('.chat-panel')?.scrollIntoView({block:'start'})`);
 await writeFile(join(dir,`chat-${phase}.png`),Buffer.from((await call('Page.captureScreenshot',{format:'png'})).data,'base64'));
 await writeFile(join(dir,`chat-${phase}.json`),JSON.stringify({phase,realLLM:'UNEXECUTED',fixtureRequests:modelRequests.length,requests:requests.map(({method,url,postData})=>({method,url,postData:method==='POST'&&!url.includes('/agent/settings')?postData:undefined})),exceptions},null,2));console.log(`M5.4 native chat ${phase} PASS: ${dir}`);
}catch(error){await writeFile(join(dir,`chat-failure-${phase}.json`),JSON.stringify({error:String(error),body:await evaluate('document.body.innerText').catch(()=>''),requests:requests.map(({method,url})=>({method,url})),modelRequests,exceptions},null,2));throw error;}
finally{releaseHold?.();close();if(fixture){fixture.closeAllConnections();await new Promise(resolve=>fixture.close(resolve));}}
