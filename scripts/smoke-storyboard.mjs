// Production WebView + pinned owned native OpenCode, synthetic local supplier.
// This verifies actual tool transport/UI/persistence, not model quality.
import assert from 'node:assert/strict';
import {createServer} from 'node:http';
import {readFile,writeFile} from 'node:fs/promises';
import {join} from 'node:path';
import {connect} from './webview-cdp.mjs';
const [port,dir,origin,phase='initial']=process.argv.slice(2);
const {call,evaluate,wait,requests,exceptions,close}=await connect(port,origin);
const click=async text=>{await wait(`(()=>{const b=[...document.querySelectorAll('button')].find(x=>x.textContent.trim()===${JSON.stringify(text)});return b&&!b.disabled})()`);await evaluate(`[...document.querySelectorAll('button')].find(x=>x.textContent.trim()===${JSON.stringify(text)}).click()`)};
const get=path=>evaluate(`fetch('http://127.0.0.1:8001'+${JSON.stringify(path)}).then(async r=>{if(!r.ok)throw Error(await r.text());return r.json()})`);
const post=(path,body)=>evaluate(`fetch('http://127.0.0.1:8001'+${JSON.stringify(path)},{method:'POST',headers:{'Content-Type':'application/json'},body:${JSON.stringify(JSON.stringify(body))}}).then(async r=>{if(!r.ok)throw Error(await r.text());return r.json()})`);
const fill=(label,value)=>evaluate(`(()=>{const e=[...document.querySelectorAll('label')].find(x=>x.textContent.startsWith(${JSON.stringify(label)})).querySelector('input');Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(e,${JSON.stringify(value)});e.dispatchEvent(new Event('input',{bubbles:true}))})()`);
const storyboard={panels:[{id:'p1',timeMs:0,framing:'近景',action:'讲述者看向镜头',transition:'同一空间连续动作'},{id:'p2',timeMs:4200,framing:'中景',action:'展示产品',transition:'随手势拉远'}],shots:[{id:'s1',startMs:0,endMs:7000,panelIds:['p1','p2'],narrativeRole:'引出主题并展示',startState:'人物静止',endState:'人物完成展示'},{id:'s2',startMs:4000,endMs:15000,panelIds:['p2'],narrativeRole:'保持主题至结尾',startState:'展示中',endState:'画面自然停留'}],timeline:{durationMs:15000,shotIds:['s1','s2'],audioIntent:'中文口播与轻快背景音乐'}};
let expected,fixture,models=[];
async function supplier(listenPort=0){fixture=createServer(async(req,res)=>{let raw='';for await(const c of req)raw+=c;if(req.method==='GET'){res.writeHead(200,{'content-type':'application/json'});res.end('{"data":[{"id":"storyboard-fixture"}]}');return}
 assert.equal(req.url,'/v1/chat/completions');const body=JSON.parse(raw),names=(body.tools??[]).map(t=>t.function.name).sort();assert.equal(names.length,19);assert(names.includes('question'));assert(names.every(n=>n==='question'||n.startsWith('design_design_')));models.push(body);
 res.writeHead(200,{'content-type':'text/event-stream'});const emit=(delta,finish=null)=>res.write(`data: ${JSON.stringify({id:'fixture',object:'chat.completion.chunk',created:1,model:'storyboard-fixture',choices:[{index:0,delta,finish_reason:finish}]})}\n\n`);
 if(models.length===1){emit({role:'assistant',tool_calls:[{index:0,id:'storyboard-call',type:'function',function:{name:'design_design_document_write',arguments:JSON.stringify({requestId:'native-storyboard',mode:'create',format:'md',typedDocument:{kind:'storyboard',pipelineId:expected.pipeline,expectedRevision:0,storyboard},output:{resources:[{key:'review',mode:'create',name:'storyboard_review.md'}],composition:{name:'口播分镜',type:{label:'视频项目'},placements:[{key:'review',role:'storyboard'}]}}})}}]});emit({},'tool_calls')}else{emit({role:'assistant',content:'分镜候选已交付画布，等待人类审阅。'});emit({},'stop')}
 res.end('data: [DONE]\n\n');});await new Promise(r=>fixture.listen(listenPort,'127.0.0.1',r));return fixture.address().port}
try{
 await wait(`document.body?.innerText.includes('本地服务已连接')`);
 if(phase==='initial'){
  expected={supplierPort:await supplier(),pipeline:crypto.randomUUID()};await click('供应商设置');await fill('LLM Base URL',`http://127.0.0.1:${expected.supplierPort}/v1`);await fill('LLM Model ID','storyboard-fixture');await fill('LLM API 密钥','synthetic-storyboard-key');await click('保存 LLM 连接');await wait(`document.body.innerText.includes('重启后生效')`);await click('创作画布');
  await evaluate(`(()=>{const e=document.querySelector('#project-name');Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(e,'M6.2 分镜验收');e.dispatchEvent(new Event('input',{bubbles:true}))})()`);await evaluate(`document.querySelector('.project-picker form').requestSubmit()`);await wait(`!!document.querySelector('.chat-panel')`);expected.p=await evaluate(`document.querySelector('#project').value`);await click('新建对话');await wait(`!!document.querySelector('[aria-label="当前对话"]').value`);expected.s=await evaluate(`document.querySelector('[aria-label="当前对话"]').value`);
 }else{
  expected=JSON.parse(await readFile(join(dir,'storyboard-expected.json'),'utf8'));await supplier(expected.supplierPort);const query=`?project_id=${expected.p}`;
  await wait(`document.querySelector('[aria-label="当前对话"]')?.value===${JSON.stringify(expected.s)}`);
  assert.equal(requests.filter(r=>r.method==='POST'&&r.url.includes('/api/')&&new URL(r.url).pathname!=='/api/canvas/content').length,0,'restart never writes');
  if(phase==='recovery'){
   const skill=await get('/api/skills?name='+encodeURIComponent('口播视频'));
   await evaluate(`document.querySelector('.chat-composer .tiptap').editor.commands.setContent('<p>用这张图做个15秒口播视频配轻快音乐，先创建语义分镜。</p>')`);await click('发送消息');
   await wait(`document.querySelector('.storyboard-review')?.innerText.includes('分镜 revision 1')`);await wait(`document.querySelector('.chat-history').innerText.includes('分镜候选已交付画布')`);assert.equal(models.length,2);
   const user=models[0].messages.filter(m=>m.role==='user').at(-1),first=user.content[0];const prefix=`当前唯一采用的创作方法（取代历史方法选择；仅方法，不授予工具权限或任何后续批准）：${skill.name} / ${skill.version} / ${skill.revision}\n${skill.body}`+Object.keys(skill.references).sort().map(name=>`\n\n受管引用 ${name}\n${skill.references[name]}`).join('');assert.equal(first.text,prefix);
   const canvas=await get('/api/canvas/nodes'+query),node=Object.values(canvas.nodes)[0];assert.equal(Object.keys(canvas.nodes).length,1);expected.resource=node.activeItem.resourceId;expected.first=node.activeItem.versionId;
   const after={...node,position:{x:30,y:25},size:{width:650,height:700}};
   await post('/api/canvas/commands'+query,{requestId:'review-layout',expectedRevision:canvas.revision,command:{type:'canvas.patch',nodes:[{id:node.id,before:node,after}],edges:[]}});
   await wait(`document.querySelector('.storyboard-review')?.innerText.includes('景别：近景')`);await click('编辑分镜 Markdown');
   await evaluate(`(()=>{const e=document.querySelector('[aria-label="分镜 Markdown 候选"]');Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype,'value').set.call(e,e.value.replace('讲述者看向镜头','真人编辑动作：讲述者微笑看向镜头'));e.dispatchEvent(new Event('input',{bubbles:true}))})()`);await click('校验并保存新 revision');
   await wait(`document.querySelector('.storyboard-review')?.innerText.includes('分镜 revision 2')`);await wait(`document.querySelector('.storyboard-review')?.innerText.includes('变更：panels')`);
   const updated=await get('/api/canvas/nodes'+query),newNode=updated.nodes[node.id];assert.equal(Object.keys(updated.nodes).length,1);assert.deepEqual(newNode.position,after.position);assert.deepEqual(newNode.size,after.size);assert.equal(newNode.activeItem.resourceId,expected.resource);assert.notEqual(newNode.activeItem.versionId,expected.first);expected.version=newNode.activeItem.versionId;expected.node=newNode;
   const revision=await get('/api/pipeline/storyboard'+query+'&versionId='+expected.version);assert.equal(revision.revision,2);assert.equal(revision.approvalStatus,'unapproved');assert.equal(revision.promptStatus,'pending');assert(revision.storyboard.panels[0].action.includes('真人编辑动作'));
   const versions=(await get('/api/versions'+query)).items;assert.deepEqual(versions.map(v=>v.origin).sort(),['agent','user']);assert(versions.every(v=>v.resource_id===expected.resource));
   await writeFile(join(dir,'storyboard-models.json'),JSON.stringify(models,null,2));await writeFile(join(dir,'storyboard-recovery-result.json'),JSON.stringify({qualification:'synthetic supplier; real native MCP and WebView UI, not real model acceptance',revision,versions,canvas:updated,exceptions},null,2));
  }else{
   await wait(`document.querySelector('.storyboard-review')?.innerText.includes('分镜 revision 2')`);assert.equal(models.length,0);const canvas=await get('/api/canvas/nodes'+query);assert.deepEqual(canvas.nodes[expected.node.id],expected.node);const old=await get('/api/pipeline/storyboard'+query+'&versionId='+expected.first);assert.equal(old.revision,1);assert.equal(old.currentRevision,2);assert.equal(old.storyboard.panels[0].action,'讲述者看向镜头');assert.equal(requests.filter(r=>r.method==='POST'&&r.url.includes('/api/')&&new URL(r.url).pathname!=='/api/canvas/content').length,0);
   await writeFile(join(dir,'storyboard-restart-result.json'),JSON.stringify({old,canvas,models:models.length,requests,exceptions},null,2));
  }
  assert.deepEqual(exceptions,[]);assert.equal(requests.filter(r=>r.url.startsWith('http://127.0.0.1:4096')).length,0);
  await evaluate(`document.querySelector('.object-surface').scrollIntoView({block:'start'})`);await writeFile(join(dir,`storyboard-${phase}.png`),Buffer.from((await call('Page.captureScreenshot',{format:'png'})).data,'base64'));
 }
 await writeFile(join(dir,'storyboard-expected.json'),JSON.stringify(expected));console.log(`M6.2 storyboard ${phase} passed: ${dir}`);
}catch(e){await writeFile(join(dir,`storyboard-${phase}-failure.txt`),String(e)+'\n'+JSON.stringify(requests)+'\n'+await evaluate('document.body.innerText'));throw e}finally{close();if(fixture)await new Promise(r=>fixture.close(r))}
