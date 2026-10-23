// Production Tauri + owned pinned native + synthetic loopback suppliers only.
import assert from 'node:assert/strict';
import {createServer} from 'node:http';
import {readFile,writeFile} from 'node:fs/promises';
import {join} from 'node:path';
import {connect} from './webview-cdp.mjs';
const [port,dir,origin,phase='initial']=process.argv.slice(2);
const {call,evaluate,wait,requests,exceptions,close}=await connect(port,origin);
const click=async(text,scope='document')=>{await wait(`(()=>{const b=[...${scope}.querySelectorAll('button')].find(x=>!x.disabled&&x.textContent.trim()===${JSON.stringify(text)});return b&&!b.disabled})()`);await evaluate(`[...${scope}.querySelectorAll('button')].find(x=>!x.disabled&&x.textContent.trim()===${JSON.stringify(text)}).click()`)};
const post=(p,path,body)=>evaluate(`fetch('http://127.0.0.1:8001${path}${p?'?project_id='+p:''}',{method:'POST',headers:{'Content-Type':'application/json'},body:${JSON.stringify(JSON.stringify(body))}}).then(async r=>({status:r.status,body:await r.json()}))`);
const get=(p,path)=>evaluate(`fetch('http://127.0.0.1:8001${path}${p?'?project_id='+p:''}').then(r=>r.json())`);
const fill=(label,value)=>evaluate(`(()=>{const e=[...document.querySelectorAll('label')].find(x=>x.textContent.startsWith(${JSON.stringify(label)})).querySelector('input');Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(e,${JSON.stringify(value)});e.dispatchEvent(new Event('input',{bubbles:true}))})()`);
const output=name=>({resources:[{key:'main',mode:'create',name}],composition:{name,type:{label:'工具验收'},placements:[{key:'main',role:'成果'}]}});
let fixture,expected,images=0,models=[];
async function supplier(listenPort=0){
 fixture=createServer(async(req,res)=>{
  let raw='';for await(const chunk of req)raw+=chunk;
  if(req.url==='/v1/images/generations') {images++;res.writeHead(200,{'content-type':'application/json'});res.end(JSON.stringify({data:[{b64_json:expected.png}]}));return;}
  assert.equal(req.url,'/v1/chat/completions');assert.equal(req.headers.authorization,'Bearer synthetic-tools-key');
  const body=JSON.parse(raw),names=(body.tools??[]).map(t=>t.function.name).sort();models.push({names,max_tokens:body.max_tokens,model:body.model});
  const n=models.length;res.writeHead(200,{'content-type':'text/event-stream'});
  const emit=(delta,finish=null)=>res.write(`data: ${JSON.stringify({id:'fixture',object:'chat.completion.chunk',created:1,model:'tools-fixture',choices:[{index:0,delta,finish_reason:finish}]})}\n\n`);
  if(names.length===0){assert([2,6].includes(n));assert(body.max_tokens>0&&body.max_tokens<=4096);emit({role:'assistant',content:'# 受管文本成果\nSynthetic local fixture.'});emit({},'stop');}
  else if([1,3,4].includes(n)){
   let name='design_design_execute',args={requestId:'native-text',capabilityId:'text:configured-llm',prompt:'生成测试文档',params:{},references:[],output:output('文本成果')};
   if(n===3){name='design_design_project_rules';args={action:'save',requestId:'native-rules',content:'长期保持人物名字一致。',expectedRevision:null};}
   if(n===4)args={requestId:'native-image',capabilityId:'image:tools-image',prompt:'Synthetic owned image fixture',params:{},references:[],output:output('图像成果')};
   emit({role:'assistant',tool_calls:[{index:0,id:'native_'+n,type:'function',function:{name,arguments:JSON.stringify(args)}}]});emit({},'tool_calls');
  }else{assert.equal(n,5);emit({role:'assistant',content:'测试成果已交付，请查看画布。'});emit({},'stop');}
  res.end('data: [DONE]\n\n');
 });await new Promise(r=>fixture.listen(listenPort,'127.0.0.1',r));return fixture.address().port;
}
// Only explicit sends to this script's owned synthetic supplier may approve a chat card.
async function sendCurrent(approve=true){
 const before=models.length, previous=requests.filter(r=>r.method==='POST'&&/\/api\/chat\/sessions\/[^/]+\/messages$/.test(new URL(r.url).pathname)).length;
 assert(fixture?.listening,'owned synthetic supplier must be listening');
 const active=await evaluate(`fetch('http://127.0.0.1:8001/api/agent/status').then(r=>r.json()).then(s=>s.active)`);
 assert.equal(active?.base_url,`http://127.0.0.1:${fixture.address().port}/v1`);assert.equal(active?.model_id,'tools-fixture');
 await click('发送消息');await wait(`![...document.querySelectorAll('.chat-panel button')].some(b=>b.textContent==='发送中')`);
 const sends=requests.filter(r=>r.method==='POST'&&/\/api\/chat\/sessions\/[^/]+\/messages$/.test(new URL(r.url).pathname));assert.equal(sends.length,previous+1,'one explicit Send');
 const sent=sends.at(-1),url=new URL(sent.url),session=decodeURIComponent(url.pathname.split('/')[4]),requestID=JSON.parse(sent.postData).requestId;
 assert.equal(url.searchParams.get('project_id'),expected.p);assert.equal(session,expected.s);
 const message=await evaluate(`(async()=>{let offset=0;for(;;){const r=await fetch(${JSON.stringify(sent.url)}+'&offset='+offset+'&limit=100');if(!r.ok)throw Error(await r.text());const page=await r.json(),m=page.items.find(m=>m.role==='user'&&m.requestId===${JSON.stringify(requestID)});if(m)return m;if(page.coverage.exhausted)return null;if(page.coverage.next_offset<=offset)throw Error('Invalid chat pagination');offset=page.coverage.next_offset;}})()`);
 assert(message?.billing_question_id,'fresh message must bind a billing card');assert.equal(message.state,'queued');assert.equal(models.length,before,'no synthetic supplier POST before this send approval');
 const question=await evaluate(`fetch('http://127.0.0.1:8001/api/question?project_id='+${JSON.stringify(expected.p)}+'&session_id='+${JSON.stringify(session)}).then(r=>r.json()).then(r=>r.items.find(q=>q.id===${JSON.stringify(message.billing_question_id)}))`);
 assert.equal(question?.project_id,expected.p);assert.equal(question?.session_id,session);assert.equal(question?.chat_message_id,message.id);assert.equal(question?.kind,'billing');assert.equal(question?.state,'pending');assert(!question.task_id);assert.equal(question.model,'tools-fixture');assert.equal(question.questions[0].options[0].label,'确认本轮聊天费用');
 if(approve){const scope=`document.querySelector('[data-question-id="${question.id}"]')`;await wait(`${scope}?.dataset.state==='pending'&&[...${scope}.querySelectorAll('button')].some(b=>b.textContent==='确认本轮聊天费用'&&!b.disabled)`);assert.equal(models.length,before,'card render must not submit inference');await evaluate(`[...${scope}.querySelectorAll('button')].find(b=>b.textContent==='确认本轮聊天费用').click()`);await wait(`${scope}?.dataset.state==='approved'`);const replies=requests.filter(r=>r.method==='POST'&&new URL(r.url).pathname===`/api/question/${question.id}/reply`);assert.equal(replies.length,1,'one explicit fee confirmation');assert.equal(new URL(replies[0].url).searchParams.get('session_id'),session);const reply=JSON.parse(replies[0].postData);assert.deepEqual(reply.answers,[['确认本轮聊天费用']]);assert.equal(reply.expected_revision,question.revision);assert(reply.requestId);}
 return message;
}
try{
 await wait(`document.body?.innerText.includes('本地服务已连接')`);
 if(phase==='initial'){
  expected={supplierPort:await supplier()};
  expected.png=await evaluate(`(()=>{const c=document.createElement('canvas');c.width=400;c.height=300;const x=c.getContext('2d');x.fillStyle='#476e96';x.fillRect(0,0,400,300);x.fillStyle='#edc380';x.fillRect(100,60,180,180);return c.toDataURL().split(',')[1]})()`);
  await click('供应商设置');await fill('LLM Base URL',`http://127.0.0.1:${expected.supplierPort}/v1`);await fill('LLM Model ID','tools-fixture');await fill('LLM API 密钥','synthetic-tools-key');
  await evaluate(`[...document.querySelectorAll('label')].find(x=>x.textContent.includes('声明文本生成价格')).querySelector('input').click()`);
  await fill('上下文 token 上限','200000');await fill('单 token 最高价','1');await fill('报价来源','synthetic-fixture');await fill('报价版本','h20-v1');await click('保存 LLM 连接');await wait(`document.body.innerText.includes('重启后生效')`);
  const config={id:'tools-image',name:'工具图像验收',kind:'image',protocol:'openai-images',base_url:`http://127.0.0.1:${expected.supplierPort}/v1`,model_id:'synthetic-image',enabled:true,headers:{},capability:{max_prompt_bytes:65536,max_count:1,max_references:0,sizes:['1024x1024'],edit:false,duration_seconds:[],audio:false},pricing:{currency:'USD',unit:'image',unit_amount:1,minor_unit_scale:2,credit_numerator:1,credit_denominator:1,source:'synthetic-fixture',version:'1',effective_at:0}};
  assert.equal((await post('','/api/providers',{requestId:crypto.randomUUID(),action:'create',config})).status,200);
  await click('创作画布');await evaluate(`(()=>{const e=document.querySelector('#project-name');Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(e,'M5.5 工具验收');e.dispatchEvent(new Event('input',{bubbles:true}))})()`);await evaluate(`document.querySelector('.project-picker form').requestSubmit()`);await wait(`!!document.querySelector('.chat-panel')`);
  expected.p=await evaluate(`document.querySelector('#project').value`);await click('新建对话');await wait(`!!document.querySelector('[aria-label="当前对话"]').value`);expected.s=await evaluate(`document.querySelector('[aria-label="当前对话"]').value`);
  await writeFile(join(dir,'tools-expected.json'),JSON.stringify(expected));
 }else{
  expected=JSON.parse(await readFile(join(dir,'tools-expected.json'),'utf8'));await supplier(expected.supplierPort);const {p,s}=expected;
  await wait(`document.querySelector('[aria-label="当前对话"]')?.value===${JSON.stringify(s)}`);
  await evaluate(`document.querySelector('.chat-composer .tiptap').editor.commands.setContent('<p>生成一份文档和一张图，并长期保存人物名字规则。</p>')`);await sendCurrent();
  await wait(`[...document.querySelectorAll('.confirmation-card')].some(c=>c.innerText.includes('保守估算')&&[...c.querySelectorAll('button')].some(b=>b.textContent==='确认生成'&&!b.disabled))`);assert.equal(models.length,1);
  await click('确认生成');await wait(`document.body.innerText.includes('长期项目规则')`);
  await evaluate(`[...document.querySelectorAll('.confirmation-card')].find(x=>x.innerText.includes('长期项目规则')).querySelector('input').click()`);await click('提交选择',`[...document.querySelectorAll('.confirmation-card')].find(x=>x.innerText.includes('长期项目规则'))`);
  await wait(`document.querySelectorAll('.confirmation-card button').length>0 && [...document.querySelectorAll('.confirmation-card button')].some(b=>b.textContent==='确认生成'&&!b.disabled)`);await click('确认生成');
  await wait(`document.querySelectorAll('.object-node.composition').length===2 && !!document.querySelector('.object-node img') && [...document.querySelectorAll('.object-node pre')].some(x=>x.innerText.includes('受管文本成果'))`);
  await wait(`document.querySelector('.chat-history').innerText.includes('测试成果已交付')`);
  const names=models[0].names;assert.equal(names.length,19);assert(names.includes('question'));assert(names.every(n=>n==='question'||n.startsWith('design_design_')));assert.equal(models.length,5);assert.equal(images,1);
  const body={requestId:'http-text',session_id:s,capability_id:'text:configured-llm',prompt:'HTTP text document',params:{},references:[],output:output('HTTP 文档')};
  const accepted=await post(p,'/api/generate/text',body);assert.equal(accepted.status,202);await wait(`document.querySelector('[data-question-id]')&&[...document.querySelectorAll('button')].some(b=>b.textContent==='确认生成'&&!b.disabled)`);await click('确认生成');
  await wait(`document.body.innerText.includes('/ 3 个节点')`);assert.equal(models.length,6);assert.equal((await post(p,'/api/generate/text',body)).body.task_id,accepted.body.task_id);assert.equal(models.length,6);
  const tasks=(await get(p,'/api/tasks')).items,versions=(await get(p,'/api/versions')).items,canvas=await get(p,'/api/canvas/nodes');
  assert.equal(tasks.filter(t=>t.status==='completed').length,3);assert.equal(versions.filter(v=>v.origin==='agent').length,2);assert.equal(versions.filter(v=>v.origin==='user').length,1);assert.equal(Object.keys(canvas.nodes).length,3);
  assert.equal(requests.filter(r=>r.method==='POST'&&r.url.includes('/api/canvas/commands')).length,0,'agent rendering must not create frontend commands');assert.equal(requests.filter(r=>r.url.startsWith('http://127.0.0.1:4096')).length,0);
  assert.deepEqual(exceptions,[]);await writeFile(join(dir,'tools-result.json'),JSON.stringify({real_inference:'UNEXECUTED',models,images,tasks,versions,canvas,exceptions},null,2));
  await evaluate(`document.querySelector('.object-surface').scrollIntoView({block:'center'})`);await writeFile(join(dir,'tools-canvas.png'),Buffer.from((await call('Page.captureScreenshot',{format:'png'})).data,'base64'));
 }
 console.log(`M5.5 production tools ${phase} passed: ${dir}`);
}catch(e){await writeFile(join(dir,`tools-${phase}-failure.txt`),String(e)+'\n'+JSON.stringify({models,images})+'\n'+await evaluate('document.body.innerText'));throw e}finally{close();if(fixture)await new Promise(r=>fixture.close(r))}
