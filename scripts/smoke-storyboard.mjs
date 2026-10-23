// Production WebView + pinned owned native OpenCode, synthetic local supplier.
// This verifies actual tool transport/UI/persistence, not model quality.
import assert from 'node:assert/strict';
import {createServer} from 'node:http';
import {readFile,writeFile} from 'node:fs/promises';
import {join} from 'node:path';
import {connect} from './webview-cdp.mjs';
const [port,dir,origin,phase='initial']=process.argv.slice(2);
const {call,evaluate,wait,requests,exceptions,close}=await connect(port,origin);
const click=async text=>{await wait(`(()=>{const b=[...document.querySelectorAll('button')].find(x=>!x.disabled&&x.textContent.trim()===${JSON.stringify(text)});return b&&!b.disabled})()`);await evaluate(`[...document.querySelectorAll('button')].find(x=>!x.disabled&&x.textContent.trim()===${JSON.stringify(text)}).click()`)};
const get=path=>evaluate(`fetch('http://127.0.0.1:8001'+${JSON.stringify(path)}).then(async r=>{if(!r.ok)throw Error(await r.text());return r.json()})`);
const post=(path,body)=>evaluate(`fetch('http://127.0.0.1:8001'+${JSON.stringify(path)},{method:'POST',headers:{'Content-Type':'application/json'},body:${JSON.stringify(JSON.stringify(body))}}).then(async r=>{if(!r.ok)throw Error(await r.text());return r.json()})`);
const fill=(label,value)=>evaluate(`(()=>{const e=[...document.querySelectorAll('label')].find(x=>x.textContent.startsWith(${JSON.stringify(label)})).querySelector('input');Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(e,${JSON.stringify(value)});e.dispatchEvent(new Event('input',{bubbles:true}))})()`);
const storyboard={panels:[{id:'p1',timeMs:0,framing:'近景',action:'讲述者看向镜头',transition:'同一空间连续动作'},{id:'p2',timeMs:4200,framing:'中景',action:'展示产品',transition:'随手势拉远'}],shots:[{id:'s1',startMs:0,endMs:7000,panelIds:['p1','p2'],narrativeRole:'引出主题并展示',startState:'人物静止',endState:'人物完成展示'},{id:'s2',startMs:4000,endMs:15000,panelIds:['p2'],narrativeRole:'保持主题至结尾',startState:'展示中',endState:'画面自然停留'}],timeline:{durationMs:15000,shotIds:['s1','s2'],audioIntent:'中文口播与轻快背景音乐'}};
let expected,fixture,models=[];
async function supplier(listenPort=0){fixture=createServer(async(req,res)=>{let raw='';for await(const c of req)raw+=c;if(req.method==='GET'){res.writeHead(200,{'content-type':'application/json'});res.end('{"data":[{"id":"storyboard-fixture"}]}');return}
 assert.equal(req.url,'/v1/chat/completions');const body=JSON.parse(raw),names=(body.tools??[]).map(t=>t.function.name).sort();assert.equal(names.length,19);assert(names.includes('question'));assert(names.every(n=>n==='question'||n.startsWith('design_design_')));models.push(body);
 res.writeHead(200,{'content-type':'text/event-stream'});const emit=(delta,finish=null)=>res.write(`data: ${JSON.stringify({id:'fixture',object:'chat.completion.chunk',created:1,model:'storyboard-fixture',choices:[{index:0,delta,finish_reason:finish}]})}\n\n`);
 if(models.length%2===1){const gate=models.length===1?'intent':models.length===3?'script':'storyboard';const typedDocument=gate==='storyboard'?{kind:gate,pipelineId:expected.pipeline,expectedRevision:0,storyboard}:{kind:gate,pipelineId:expected.pipeline,expectedRevision:0,content:gate==='intent'?'创作意图：15秒中文口播，人物展示产品，配轻快音乐。':'完整脚本：人物开场向镜头介绍产品，随手势拉远展示产品，最后自然停留。'};
 emit({role:'assistant',tool_calls:[{index:0,id:`${gate}-call`,type:'function',function:{name:'design_design_document_write',arguments:JSON.stringify({requestId:`native-${gate}`,mode:'create',format:'md',typedDocument,output:{resources:[{key:gate,mode:'create',name:gate==='storyboard'?'storyboard_review.md':gate+'.md'}],composition:{name:'口播分镜',type:{label:'视频项目'},placements:[{key:gate,role:gate}]}}})}}]});emit({},'tool_calls')}else{const label=models.length===2?'意图':models.length===4?'脚本':'分镜';emit({role:'assistant',content:label+'候选已交付画布，等待人类审阅。'});emit({},'stop')}
 res.end('data: [DONE]\n\n');});await new Promise(r=>fixture.listen(listenPort,'127.0.0.1',r));return fixture.address().port}
// Only explicit sends to this script's owned synthetic supplier may approve a chat card.
async function sendCurrent(approve=true){
 const before=models.length, previous=requests.filter(r=>r.method==='POST'&&/\/api\/chat\/sessions\/[^/]+\/messages$/.test(new URL(r.url).pathname)).length;
 assert(fixture?.listening,'owned synthetic supplier must be listening');
 const active=await evaluate(`fetch('http://127.0.0.1:8001/api/agent/status').then(r=>r.json()).then(s=>s.active)`);
 assert.equal(active?.base_url,`http://127.0.0.1:${fixture.address().port}/v1`);assert.equal(active?.model_id,'storyboard-fixture');
 await click('发送消息');await wait(`![...document.querySelectorAll('.chat-panel button')].some(b=>b.textContent==='发送中')`);
 const sends=requests.filter(r=>r.method==='POST'&&/\/api\/chat\/sessions\/[^/]+\/messages$/.test(new URL(r.url).pathname));assert.equal(sends.length,previous+1,'one explicit Send');
 const sent=sends.at(-1),url=new URL(sent.url),session=decodeURIComponent(url.pathname.split('/')[4]),requestID=JSON.parse(sent.postData).requestId;
 assert.equal(url.searchParams.get('project_id'),expected.p);assert.equal(session,expected.s);
 const message=await evaluate(`(async()=>{let offset=0;for(;;){const r=await fetch(${JSON.stringify(sent.url)}+'&offset='+offset+'&limit=100');if(!r.ok)throw Error(await r.text());const page=await r.json(),m=page.items.find(m=>m.role==='user'&&m.requestId===${JSON.stringify(requestID)});if(m)return m;if(page.coverage.exhausted)return null;if(page.coverage.next_offset<=offset)throw Error('Invalid chat pagination');offset=page.coverage.next_offset;}})()`);
 assert(message?.billing_question_id,'fresh message must bind a billing card');assert.equal(message.state,'queued');assert.equal(models.length,before,'no synthetic supplier POST before this send approval');
 const question=await evaluate(`fetch('http://127.0.0.1:8001/api/question?project_id='+${JSON.stringify(expected.p)}+'&session_id='+${JSON.stringify(session)}).then(r=>r.json()).then(r=>r.items.find(q=>q.id===${JSON.stringify(message.billing_question_id)}))`);
 assert.equal(question?.project_id,expected.p);assert.equal(question?.session_id,session);assert.equal(question?.chat_message_id,message.id);assert.equal(question?.kind,'billing');assert.equal(question?.state,'pending');assert(!question.task_id);assert.equal(question.model,'storyboard-fixture');assert.equal(question.questions[0].options[0].label,'确认本轮聊天费用');
 if(approve){const scope=`document.querySelector('[data-question-id="${question.id}"]')`;await wait(`${scope}?.dataset.state==='pending'&&[...${scope}.querySelectorAll('button')].some(b=>b.textContent==='确认本轮聊天费用'&&!b.disabled)`);assert.equal(models.length,before,'card render must not submit inference');await evaluate(`[...${scope}.querySelectorAll('button')].find(b=>b.textContent==='确认本轮聊天费用').click()`);await wait(`${scope}?.dataset.state==='approved'`);const replies=requests.filter(r=>r.method==='POST'&&new URL(r.url).pathname===`/api/question/${question.id}/reply`);assert.equal(replies.length,1,'one explicit fee confirmation');assert.equal(new URL(replies[0].url).searchParams.get('session_id'),session);const reply=JSON.parse(replies[0].postData);assert.deepEqual(reply.answers,[['确认本轮聊天费用']]);assert.equal(reply.expected_revision,question.revision);assert(reply.requestId);}
 return message;
}
try{
 await wait(`document.body?.innerText.includes('本地服务已连接')`);
 if(phase==='initial'){
  expected={supplierPort:await supplier(),pipeline:crypto.randomUUID()};await click('供应商设置');await fill('LLM Base URL',`http://127.0.0.1:${expected.supplierPort}/v1`);await fill('LLM Model ID','storyboard-fixture');await fill('LLM API 密钥','synthetic-storyboard-key');await evaluate(`[...document.querySelectorAll('label')].find(x=>x.textContent.includes('声明文本生成价格')).querySelector('input').click()`);await fill('上下文 token 上限','200000');await fill('单 token 最高价','0');await fill('报价来源','synthetic-fixture');await fill('报价版本','h20-v1');await click('保存 LLM 连接');await wait(`document.body.innerText.includes('重启后生效')`);await click('创作画布');
  await evaluate(`(()=>{const e=document.querySelector('#project-name');Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(e,'M6.4 契约与血缘验收');e.dispatchEvent(new Event('input',{bubbles:true}))})()`);await evaluate(`document.querySelector('.project-picker form').requestSubmit()`);await wait(`!!document.querySelector('.chat-panel')`);expected.p=await evaluate(`document.querySelector('#project').value`);await click('新建对话');await wait(`!!document.querySelector('[aria-label="当前对话"]').value`);expected.s=await evaluate(`document.querySelector('[aria-label="当前对话"]').value`);
 }else{
  expected=JSON.parse(await readFile(join(dir,'storyboard-expected.json'),'utf8'));await supplier(expected.supplierPort);const query=`?project_id=${expected.p}`;
  await wait(`document.querySelector('[aria-label="当前对话"]')?.value===${JSON.stringify(expected.s)}`);
  assert.equal(requests.filter(r=>r.method==='POST'&&r.url.includes('/api/')&&new URL(r.url).pathname!=='/api/canvas/content').length,0,'restart never writes');
  if(phase==='recovery'){
   const skill=await get('/api/skills?name='+encodeURIComponent('口播视频'));
   for(const [n,label] of ['意图','脚本','分镜'].entries()){
    await evaluate(`document.querySelector('.chat-composer .tiptap').editor.commands.setContent('<p>${n===0?'用这张图做个15秒口播视频配轻快音乐，先确认意图。':n===1?'意图已批准，请创建完整脚本。':'脚本已批准，请创建语义分镜。'}</p>')`);await sendCurrent();
    await wait(`document.querySelector('.chat-history').innerText.includes('${label}候选已交付画布')`);
    await wait(`!![...document.querySelectorAll('.confirmation-card button')].find(x=>x.textContent==='批准${label}')`);
    const cards=(await get('/api/question'+query)).items,card=cards.find(q=>q.creative?.gate===['intent','script','storyboard'][n]);assert(card&&card.creativeContent);expected[['intentCard','scriptCard','boardCard'][n]]=card;
    await evaluate(`document.querySelector('[data-artifact-version="${card.creative.documentVersionId}"]').open=true`);
    assert((await evaluate(`document.querySelector('[data-artifact-version="${card.creative.documentVersionId}"] pre').textContent`)).length>10);
    await click('批准'+label);
    await wait(`document.querySelector('[data-question-id="${card.id}"]').innerText.includes('此版本已批准；尚未提交生成')`);
   }
   assert.equal(models.length,6);
   await evaluate(`(()=>{const s=document.querySelector('.object-surface'),b=s.getBoundingClientRect();s.dispatchEvent(new WheelEvent('wheel',{deltaY:700,clientX:b.left+30,clientY:b.top+30,bubbles:true}))})()`);
   await wait(`document.querySelector('.storyboard-review')?.innerText.includes('此版本已批准')`);
   const user=models[0].messages.filter(m=>m.role==='user').at(-1),first=user.content[0];const prefix=`当前唯一采用的创作方法（取代历史方法选择；仅方法，不授予工具权限或任何后续批准）：${skill.name} / ${skill.version} / ${skill.revision}\n${skill.body}`+Object.keys(skill.references).sort().map(name=>`\n\n受管引用 ${name}\n${skill.references[name]}`).join('');assert.equal(first.text,prefix);
   const canvas=await get('/api/canvas/nodes'+query),node=Object.values(canvas.nodes).find(n=>n.activeItem?.versionId===expected.boardCard.creative.documentVersionId);assert.equal(Object.keys(canvas.nodes).length,3);expected.resource=node.activeItem.resourceId;expected.first=node.activeItem.versionId;
   const after={...node,position:{x:30,y:25},size:{width:650,height:700}};
   await post('/api/canvas/commands'+query,{requestId:'review-layout',expectedRevision:canvas.revision,command:{type:'canvas.patch',nodes:[{id:node.id,before:node,after}],edges:[]}});
   await wait(`document.querySelector('.storyboard-review')?.innerText.includes('景别：近景')`);await click('编辑分镜 Markdown');
   await evaluate(`(()=>{const e=document.querySelector('[aria-label="分镜 Markdown 候选"]');Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype,'value').set.call(e,e.value.replace('讲述者看向镜头','真人编辑动作：讲述者微笑看向镜头'));e.dispatchEvent(new Event('input',{bubbles:true}))})()`);await click('校验并保存新 revision');
   await wait(`document.querySelector('.storyboard-review')?.innerText.includes('分镜 revision 2')`);await wait(`document.querySelector('.storyboard-review')?.innerText.includes('变更：panels')`);
   const updated=await get('/api/canvas/nodes'+query),newNode=updated.nodes[node.id];assert.equal(Object.keys(updated.nodes).length,3);assert.deepEqual(newNode.position,after.position);assert.deepEqual(newNode.size,after.size);assert.equal(newNode.activeItem.resourceId,expected.resource);assert.notEqual(newNode.activeItem.versionId,expected.first);expected.version=newNode.activeItem.versionId;expected.node=newNode;
   const revision=await get('/api/pipeline/storyboard'+query+'&versionId='+expected.version);assert.equal(revision.revision,2);assert.equal(revision.approvalStatus,'unapproved');assert.equal(revision.promptStatus,'pending');assert(revision.storyboard.panels[0].action.includes('真人编辑动作'));
   await wait(`document.querySelector('[data-question-id="${expected.boardCard.id}"]').innerText.includes('旧卡片不可使用')`);
   const stale=await evaluate(`fetch('http://127.0.0.1:8001/api/question/${expected.boardCard.id}/reply?project_id=${expected.p}&session_id=${expected.s}',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({requestId:'late-old-card',expected_revision:1,answers:[['批准分镜']]})}).then(r=>r.status)`);assert.equal(stale,409);
   await click('提出此版本批准卡');await click('批准分镜');
   await wait(`document.querySelector('.storyboard-review').innerText.includes('此版本已批准')`);
   const approved=await get('/api/pipeline/storyboard'+query+'&versionId='+expected.version);assert.equal(approved.approvalStatus,'approved');
   const cardsAfter=(await get('/api/question'+query)).items;assert.equal(cardsAfter.filter(c=>c.kind==='creative'&&c.state==='approved').length,3);expected.cards=cardsAfter;
   const versions=(await get('/api/versions'+query)).items.filter(v=>v.resource_id===expected.resource);assert.deepEqual(versions.map(v=>v.origin).sort(),['agent','user']);assert(versions.every(v=>v.resource_id===expected.resource));
   await post('/api/providers',{requestId:'smoke-seedance',action:'create',config:{id:'seedance',name:'Seedance Video',kind:'video',protocol:'ark-video',base_url:'https://api.example.com/v1',model_id:'seedance-2.0',enabled:true,capability:{max_prompt_bytes:4000,max_count:1,max_references:2,sizes:['1280x720'],duration_seconds:[5,10,15],audio:true},pricing:{currency:'USD',unit:'second',unit_amount:10,minor_unit_scale:2,credit_numerator:1,credit_denominator:1,source:'declared',version:'v1'}}});
   const promptText='镜头一 [00:00-00:07] [近景]：讲述者微笑看向镜头介绍产品。\n镜头二 [00:07-00:15] [中景]：随手势拉远展示产品，轻快背景音乐收束。 ';
   const promptDoc=await post('/api/documents/write'+query,{requestId:'smoke-prompt-1',mode:'create',format:'md',typedDocument:{kind:'prompt',pipelineId:expected.pipeline,expectedRevision:0,content:promptText,contract:{capabilityId:'video:seedance',params:{duration:15,size:'1280x720',aspect_ratio:'16:9',generate_audio:true,audio_mode:'narration+music'}}},output:{resources:[{key:'prompt',mode:'create',name:'final_video_prompt.md'}],composition:{name:'口播分镜',type:{label:'视频项目'},placements:[{key:'prompt',role:'prompt'}]}}});
   expected.promptVersion=promptDoc.delivery.versions[0].versionId;
   const canvas2=await get('/api/canvas/nodes'+query),pNode=Object.values(canvas2.nodes).find(n=>n.activeItem?.versionId===expected.promptVersion),pAfter={...pNode,position:{x:700,y:25},size:{width:650,height:700}};
   await post('/api/canvas/commands'+query,{requestId:'prompt-layout',expectedRevision:canvas2.revision,command:{type:'canvas.patch',nodes:[{id:pNode.id,before:pNode,after:pAfter}],edges:[]}});
   await wait(`document.querySelector('.prompt-review')?.innerText.includes('Preflight 七项门禁：全部通过')`);
   await wait(`document.querySelector('.prompt-review')?.innerText.includes('音轨契约：narration+music（口播：是 · 配乐：是')`);
   await wait(`document.querySelector('.prompt-review')?.innerText.includes('SQL 血缘链路：意图 r1 → 脚本 r1 → 分镜 r2 → Prompt r1')`);
   const promptView=await get('/api/pipeline/prompt'+query+'&versionId='+expected.promptVersion),lineage=await get('/api/pipeline/lineage'+query+'&versionId='+expected.promptVersion);
   assert.equal(promptView.content,promptText);assert.equal(promptView.contract.status,'ready');assert.equal(promptView.contract.preflight.passed,true);assert.equal(promptView.contract.preflight.checks.length,7);assert.equal(lineage.storyboard.revision,2);assert.equal(lineage.prompt.revision,1);
   await writeFile(join(dir,'storyboard-models.json'),JSON.stringify(models,null,2));await writeFile(join(dir,'storyboard-recovery-result.json'),JSON.stringify({qualification:'synthetic supplier; real native MCP and WebView UI, not real model acceptance',revision,approved,stale,cardsAfter,versions,promptView,lineage,canvas:updated,exceptions},null,2));
  }else{
   await wait(`document.querySelector('.storyboard-review')?.innerText.includes('分镜 revision 2')`);await wait(`document.querySelector('.prompt-review')?.innerText.includes('Preflight 七项门禁：全部通过')`);assert.equal(models.length,0);const canvas=await get('/api/canvas/nodes'+query);assert.deepEqual(canvas.nodes[expected.node.id],expected.node);const old=await get('/api/pipeline/storyboard'+query+'&versionId='+expected.first);assert.equal(old.revision,1);assert.equal(old.currentRevision,2);assert.equal(old.approvalStatus,'unapproved');const current=await get('/api/pipeline/storyboard'+query+'&versionId='+expected.version);assert.equal(current.approvalStatus,'approved');const promptView=await get('/api/pipeline/prompt'+query+'&versionId='+expected.promptVersion),lineage=await get('/api/pipeline/lineage'+query+'&versionId='+expected.promptVersion);assert.equal(promptView.contract.status,'ready');assert.equal(lineage.storyboard.revision,2);const cards=(await get('/api/question'+query)).items;assert.deepEqual(cards,expected.cards);assert.equal(old.storyboard.panels[0].action,'讲述者看向镜头');assert.equal(requests.filter(r=>r.method==='POST'&&r.url.includes('/api/')&&new URL(r.url).pathname!=='/api/canvas/content').length,0);
   await writeFile(join(dir,'storyboard-restart-result.json'),JSON.stringify({old,current,promptView,lineage,cards,canvas,models:models.length,requests,exceptions},null,2));
  }
  assert.deepEqual(exceptions,[]);assert.equal(requests.filter(r=>r.url.startsWith('http://127.0.0.1:4096')).length,0);
  await evaluate(`document.querySelector('.object-surface').scrollIntoView({block:'start'})`);await writeFile(join(dir,`storyboard-${phase}.png`),Buffer.from((await call('Page.captureScreenshot',{format:'png'})).data,'base64'));
 }
 await writeFile(join(dir,'storyboard-expected.json'),JSON.stringify(expected));console.log(`M6.4 storyboard & prompt contract ${phase} passed: ${dir}`);
}catch(e){await writeFile(join(dir,`storyboard-${phase}-failure.txt`),String(e)+'\n'+JSON.stringify(requests)+'\n'+await evaluate('document.body.innerText'));throw e}finally{close();if(fixture)await new Promise(r=>fixture.close(r))}
