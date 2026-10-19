// Production WebView + pinned owned native OpenCode, synthetic local supplier & image fixture.
// Explicitly marked test fixture: M6 video adapter is offline/fixture-only; real ark-video is M7.1.
import assert from 'node:assert/strict';
import {createServer} from 'node:http';
import {readFile,writeFile} from 'node:fs/promises';
import {join} from 'node:path';
import {connect} from './webview-cdp.mjs';

const [port,dir,origin,phase='initial']=process.argv.slice(2);
const {call,evaluate,wait,requests,exceptions,close}=await connect(port,origin);
const click=async(text,scope='document')=>{await wait(`(()=>{const b=[...${scope}.querySelectorAll('button')].find(x=>!x.disabled&&x.textContent.trim()===${JSON.stringify(text)});return b&&!b.disabled})()`);await evaluate(`[...${scope}.querySelectorAll('button')].find(x=>!x.disabled&&x.textContent.trim()===${JSON.stringify(text)}).click()`)};
const get=path=>evaluate(`fetch('http://127.0.0.1:8001'+${JSON.stringify(path)}).then(async r=>{if(!r.ok)throw Error(await r.text());return r.json()})`);
const post=(path,body)=>evaluate(`fetch('http://127.0.0.1:8001'+${JSON.stringify(path)},{method:'POST',headers:{'Content-Type':'application/json'},body:${JSON.stringify(JSON.stringify(body))}}).then(async r=>({status:r.status,data:await r.json()}))`);
const fill=(selector,value)=>evaluate(`(()=>{const e=document.querySelector(${JSON.stringify(selector)});if(!e)throw Error('Missing '+${JSON.stringify(selector)});Object.getOwnPropertyDescriptor(Object.getPrototypeOf(e),'value').set.call(e,${JSON.stringify(value)});e.dispatchEvent(new Event(e.tagName==='SELECT'?'change':'input',{bubbles:true}))})()`);
const fillLabel=(label,value)=>evaluate(`(()=>{const e=[...document.querySelectorAll('label')].find(x=>x.textContent.startsWith(${JSON.stringify(label)})).querySelector('input');Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(e,${JSON.stringify(value)});e.dispatchEvent(new Event('input',{bubbles:true}))})()`);

const storyboard={
 panels:[
  {id:'p1',timeMs:0,framing:'近景',action:'讲述者看向镜头介绍产品',transition:'切'},
  {id:'p2',timeMs:6000,framing:'中景',action:'随手势拉远展示全景',transition:'慢推'},
 ],
 shots:[
  {id:'s1',startMs:0,endMs:6000,panelIds:['p1'],narrativeRole:'引出主题',startState:'人物静止',endState:'人物开口'},
  {id:'s2',startMs:6000,endMs:15000,panelIds:['p2'],narrativeRole:'展示收束',startState:'展示中',endState:'自然定格'},
 ],
 timeline:{durationMs:15000,shotIds:['s1','s2'],audioIntent:'15s人声口播+背景配乐'},
};

let expected,fixture,models=[],imageMode='ok',imagePosts=0,imageEdits=0,pngBase64='';

async function supplier(listenPort=0){
 fixture=createServer(async(req,res)=>{
  let raw='';for await(const c of req)raw+=c;
  if(req.method==='GET'&&req.url==='/result'){
   res.writeHead(200,{'content-type':'image/png'});
   res.end(Buffer.from(pngBase64,'base64'));
   return;
  }
  if(req.method==='GET'){
   res.writeHead(200,{'content-type':'application/json'});
   res.end('{"data":[{"id":"pipeline-fixture"}]}');
   return;
  }
  if(req.url==='/images/generations'||req.url==='/images/edits'){
   imagePosts++;
   if(req.url==='/images/edits')imageEdits++;
   if(imageMode==='param_error'){
    res.writeHead(400,{'content-type':'application/json'});
    res.end(JSON.stringify({error:{code:'invalid_parameter',message:'quality high rejected by fixture'}}));
    return;
   }
   if(imageMode==='semantic_error'){
    res.writeHead(400,{'content-type':'application/json'});
    res.end(JSON.stringify({error:{code:'content_policy_violation',message:'narrative conflict rejected by fixture'}}));
    return;
   }
   if(imageMode==='upstream_error'){
    res.writeHead(429,{'content-type':'application/json'});
    res.end(JSON.stringify({error:{code:'rate_limit_exceeded',message:'temporary upstream rate limit'}}));
    return;
   }
   res.writeHead(200,{'content-type':'application/json'});
   res.end(JSON.stringify({data:[{url:`http://127.0.0.1:${fixture.address().port}/result`}]}));
   return;
  }
  assert.equal(req.url,'/v1/chat/completions');
  const body=JSON.parse(raw),names=(body.tools??[]).map(t=>t.function.name).sort();
  assert.equal(names.length,19);assert(names.includes('question'));
  models.push(body);
  res.writeHead(200,{'content-type':'text/event-stream'});
  const emit=(delta,finish=null)=>res.write(`data: ${JSON.stringify({id:'fixture',object:'chat.completion.chunk',created:1,model:'pipeline-fixture',choices:[{index:0,delta,finish_reason:finish}]})}\n\n`);
  if(models.length%2===1){
   const gate=models.length===1?'intent':models.length===3?'script':'storyboard';
   const typedDocument=gate==='storyboard'
    ?{kind:gate,pipelineId:expected.pipeline,expectedRevision:0,storyboard}
    :{kind:gate,pipelineId:expected.pipeline,expectedRevision:0,content:gate==='intent'?'创作意图：15秒人声口播配轻快背景音乐。':'完整脚本：讲述者微笑向镜头介绍产品，随手势拉远展示全景。'};
   emit({role:'assistant',tool_calls:[{index:0,id:`${gate}-call`,type:'function',function:{name:'design_design_document_write',arguments:JSON.stringify({requestId:`native-${gate}`,mode:'create',format:'md',typedDocument,output:{resources:[{key:gate==='storyboard'?'review':gate,mode:'create',name:gate==='storyboard'?'storyboard_review.md':gate+'.md'}],composition:{name:'口播成片',type:{label:'视频项目'},placements:[{key:gate==='storyboard'?'review':gate,role:gate}]}}})}}]});
   emit({},'tool_calls');
  }else{
   const label=models.length===2?'意图':models.length===4?'脚本':'分镜';
   emit({role:'assistant',content:label+'候选已交付画布，等待人类审阅。'});
   emit({},'stop');
  }
  res.end('data: [DONE]\n\n');
 });
 await new Promise(r=>fixture.listen(listenPort,'127.0.0.1',r));
 return fixture.address().port;
}

try{
 await wait(`document.body?.innerText.includes('本地服务已连接')`);
 pngBase64=await evaluate(`(()=>{const c=document.createElement('canvas');c.width=400;c.height=300;const x=c.getContext('2d');x.fillStyle='#1f4e68';x.fillRect(0,0,400,300);x.fillStyle='#f4d35e';x.fillRect(60,50,120,110);return c.toDataURL().split(',')[1]})()`);
 if(phase==='initial'){
  expected={supplierPort:await supplier(),pipeline:crypto.randomUUID()};
  await click('供应商设置');
  await fillLabel('LLM Base URL',`http://127.0.0.1:${expected.supplierPort}/v1`);
  await fillLabel('LLM Model ID','pipeline-fixture');
  await fillLabel('LLM API 密钥','synthetic-pipeline-key');
  await click('保存 LLM 连接');
  await wait(`document.body.innerText.includes('重启后生效')`);
  await click('创作画布');
  await fill('#project-name','M6.5 一句话工作流与失败回流验收');
  await evaluate(`document.querySelector('.project-picker form').requestSubmit()`);
  await wait(`!!document.querySelector('.chat-panel')`);
  expected.p=await evaluate(`document.querySelector('#project').value`);
  await click('新建对话');
  await wait(`!!document.querySelector('[aria-label="当前对话"]').value`);
  expected.s=await evaluate(`document.querySelector('[aria-label="当前对话"]').value`);
 }else{
  expected=JSON.parse(await readFile(join(dir,'pipeline-expected.json'),'utf8'));
  await supplier(expected.supplierPort);
  const query=`?project_id=${expected.p}`;
  await wait(`document.querySelector('[aria-label="当前对话"]')?.value===${JSON.stringify(expected.s)}`);
  assert.equal(requests.filter(r=>r.method==='POST'&&r.url.includes('/api/')&&new URL(r.url).pathname!=='/api/canvas/content').length,0,'restart performs no control writes');

  if(phase==='recovery'){
   // 1. One-sentence workflow -> Intent -> Script -> Storyboard rev 1 -> Human approvals
   for(const [n,label] of ['意图','脚本','分镜'].entries()){
    await evaluate(`document.querySelector('.chat-composer .tiptap').editor.commands.setContent('<p>${n===0?'用这张图做个15秒口播视频配轻快音乐，先确认意图。':n===1?'意图已批准，请生成完整脚本。':'脚本已批准，请生成语义分镜。'}</p>')`);
    await click('发送消息');
    await wait(`document.querySelector('.chat-history').innerText.includes('${label}候选已交付画布')`);
    await wait(`!![...document.querySelectorAll('.confirmation-card button')].find(x=>x.textContent==='批准${label}')`);
    const cards=(await get('/api/question'+query)).items,card=cards.find(q=>q.creative?.gate===['intent','script','storyboard'][n]);
    assert(card&&card.creativeContent);
    expected[['intentCard','scriptCard','boardCard'][n]]=card;
    await click('批准'+label);
    await wait(`document.querySelector('[data-question-id="${card.id}"]').innerText.includes('此版本已批准；尚未提交生成')`);
   }
   assert.equal(models.length,6);
   expected.sbRev1Version=expected.boardCard.creative.documentVersionId;

   // Configure test-only video fixture provider + loopback image & edit providers
   const baseImgCfg={
    id:'seedream',name:'Seedream Keyframe Fixture',kind:'image',protocol:'openai-images',
    base_url:`http://127.0.0.1:${expected.supplierPort}`,model_id:'seedream-3.0-fixture',enabled:true,headers:{},
    capability:{max_prompt_bytes:4000,max_count:1,max_references:2,sizes:['1024x1024'],edit:true,duration_seconds:[],audio:false},
    pricing:{currency:'USD',unit:'image',unit_amount:5,minor_unit_scale:2,credit_numerator:1,credit_denominator:1,source:'fixture',version:'v1',effective_at:0}
   };
   assert.equal((await post('/api/providers',{requestId:'cfg-seedream',action:'create',config:baseImgCfg})).status,200);
   const editImgCfg={...baseImgCfg,id:'seedream-edit',name:'Seedream Edit Fixture',capability:{...baseImgCfg.capability,reference_roles:['image','mask']}};
   assert.equal((await post('/api/providers',{requestId:'cfg-seedream-edit',action:'create',config:editImgCfg})).status,200);
   const vidCfg={
    id:'seedance',name:'Seedance Video Test Fixture (Not Real Video Acceptance)',kind:'video',protocol:'ark-video',
    base_url:`http://127.0.0.1:${expected.supplierPort}`,model_id:'seedance-2.0-fixture',enabled:true,headers:{},
    capability:{max_prompt_bytes:4000,max_count:1,max_references:2,sizes:['1280x720'],edit:false,duration_seconds:[5,10,15],audio:true},
    pricing:{currency:'USD',unit:'second',unit_amount:10,minor_unit_scale:2,credit_numerator:1,credit_denominator:1,source:'fixture',version:'v1',effective_at:0}
   };
   assert.equal((await post('/api/providers',{requestId:'cfg-seedance',action:'create',config:vidCfg})).status,200);

   // 2. Write exact final_video_prompt.md with 15s oral narration + music contract, then compile keyframe contract1
   const promptText='镜头一 [00:00-00:06] [近景]：讲述者微笑向镜头介绍产品。\n镜头二 [00:06-00:15] [中景]：随手势拉远展示全景，配乐收束。 ';
   const promptDoc=(await post('/api/documents/write'+query,{
    requestId:'wf-prompt-v1',mode:'create',format:'md',
    typedDocument:{kind:'prompt',pipelineId:expected.pipeline,expectedRevision:0,content:promptText,contract:{capabilityId:'video:seedance',params:{duration:15,size:'1280x720',aspect_ratio:'16:9',generate_audio:true,audio_mode:'narration+music'}}},
    output:{resources:[{key:'prompt',mode:'create',name:'final_video_prompt.md'}],composition:{name:'口播成片',type:{label:'视频项目'},placements:[{key:'prompt',role:'prompt'}]}}
   })).data;
   expected.promptV1=promptDoc.delivery.versions[0].versionId;

   // Layout storyboard & prompt review nodes on Canvas
   await evaluate(`(()=>{const s=document.querySelector('.object-surface'),b=s.getBoundingClientRect();s.dispatchEvent(new WheelEvent('wheel',{deltaY:700,clientX:b.left+30,clientY:b.top+30,bubbles:true}))})()`);
   const canvas1=await get('/api/canvas/nodes'+query);
   const sbNode=Object.values(canvas1.nodes).find(n=>n.activeItem?.versionId===expected.sbRev1Version);
   const prNode=Object.values(canvas1.nodes).find(n=>n.activeItem?.versionId===expected.promptV1);
   await post('/api/canvas/commands'+query,{requestId:'layout-sb-pr',expectedRevision:canvas1.revision,command:{type:'canvas.patch',nodes:[
    {id:sbNode.id,before:sbNode,after:{...sbNode,position:{x:25,y:25},size:{width:620,height:680}}},
    {id:prNode.id,before:prNode,after:{...prNode,position:{x:670,y:25},size:{width:620,height:680}}}
   ],edges:[]}});
   await wait(`document.querySelector('.prompt-review')?.innerText.includes('M6 视频适配器状态')`);
   await wait(`document.querySelector('.prompt-review')?.innerText.includes('Preflight 七项门禁：全部通过')`);

   // Compile image:seedream contract1 on promptV1
   const compiled1=(await post('/api/pipeline/compile'+query,{
    requestId:'compile-img-1',pipelineId:expected.pipeline,promptVersionId:expected.promptV1,expectedRevision:1,
    prompt:promptText,capabilityId:'image:seedream',params:{size:'1024x1024',quality:'high'}
   })).data.contract;

   const submitContractTask=async(reqId,contractId,params,name)=>{
    const res=await post('/api/generate/image'+query,{
     requestId:reqId,capability_id:'image:seedream',contract_id:contractId,prompt:promptText,params,references:[],count:1,
     output:{resources:[{key:'shot',mode:'create',name}],composition:{name:'口播成片',type:{label:'视频项目'},placements:[{key:'shot',role:'keyframe'}]}}
    });
    assert.equal(res.status,202,JSON.stringify(res.data));
    const qItems=(await get('/api/question'+query)).items;
    const bill=qItems.find(q=>q.kind==='billing'&&q.task_id===res.data.task_id&&q.state==='pending');
    assert(bill);
    await click('确认生成',`document.querySelector('[data-question-id="${bill.id}"]')`);
    return res.data.task_id;
   };

   // 3. Failure Return Path 1: Parameter failure -> UI "修改参数" -> PromptReview "编译新参数契约"
   imageMode='param_error';
   const paramTaskId=await submitContractTask('gen-param-1',compiled1.id,{size:'1024x1024',quality:'high'},'参数失败镜头');
   await wait(`document.querySelector('[data-task-id="${paramTaskId}"]')?.innerText.includes('参数错误 (parameter)')`);
   await click('修改参数',`document.querySelector('[data-task-id="${paramTaskId}"]')`);
   await wait(`document.querySelector('[data-task-id="${paramTaskId}"]')?.innerText.includes('已记录回流：修改参数')`);
   await wait(`!!document.querySelector('.prompt-review [data-recovery-stage="parameter"]')`);
   await fill('.prompt-review textarea[aria-label="回流参数 JSON"]','{"size":"1024x1024","quality":"medium"}');
   await click('编译新参数契约',`document.querySelector('.prompt-review')`);
   await wait(`!!document.querySelector('.prompt-review [data-contract-recovery="${paramTaskId}"]')`);
   const promptAfterParam=await get('/api/pipeline/prompt'+query+'&versionId='+expected.promptV1);
   const compiled2=promptAfterParam.contract;
   assert.notEqual(compiled2.id,compiled1.id);
   assert.equal(compiled2.storyboardRevision,1);
   assert.equal(compiled2.recovery.fromTaskId,paramTaskId);

   // 4. Failure Return Path 2: Semantic failure -> UI "修改分镜" -> StoryboardReview edit rev 2 -> invalidates old approval -> re-approve -> new Prompt rev 2
   imageMode='semantic_error';
   const semTaskId=await submitContractTask('gen-sem-1',compiled2.id,{size:'1024x1024',quality:'medium'},'语义冲突镜头');
   await wait(`document.querySelector('[data-task-id="${semTaskId}"]')?.innerText.includes('语义/创意冲突 (semantic)')`);
   await click('修改分镜',`document.querySelector('[data-task-id="${semTaskId}"]')`);
   await wait(`!!document.querySelector('.storyboard-review [data-recovery-stage="semantic"]')`);
   await click('编辑分镜 Markdown',`document.querySelector('.storyboard-review')`);
   await evaluate(`(()=>{const e=document.querySelector('[aria-label="分镜 Markdown 候选"]');Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype,'value').set.call(e,e.value.replace('讲述者看向镜头介绍产品','讲述者微笑看向镜头展示产品细节'));e.dispatchEvent(new Event('input',{bubbles:true}))})()`);
   await click('校验并保存新 revision',`document.querySelector('.storyboard-review')`);
   await wait(`document.querySelector('.storyboard-review')?.innerText.includes('分镜 revision 2')`);
   await wait(`document.querySelector('.storyboard-review')?.innerText.includes('未批准')`);
   // Old contract2 is now invalidated because storyboard was edited to revision 2!
   const blockedOld=await post('/api/generate/image'+query,{
    requestId:'blocked-old-contract',capability_id:'image:seedream',contract_id:compiled2.id,prompt:promptText,params:{size:'1024x1024',quality:'medium'},references:[],count:1,
    output:{resources:[{key:'shot',mode:'create',name:'阻断镜头'}],composition:{name:'口播成片',type:{label:'视频项目'},placements:[{key:'shot',role:'keyframe'}]}}
   });
   assert.equal(blockedOld.status,409);

   // Propose and approve storyboard revision 2
   await click('提出此版本批准卡',`document.querySelector('.storyboard-review')`);
   await wait(`!![...document.querySelectorAll('.confirmation-card button')].find(x=>!x.disabled&&x.textContent==='批准分镜')`);
   await click('批准分镜');
   await wait(`document.querySelector('.storyboard-review')?.innerText.includes('此版本已批准')`);

   // Write Prompt revision 2 bound to storyboard revision 2
   const promptDoc2=(await post('/api/documents/write'+query,{
    requestId:'wf-prompt-v2',mode:'replace',format:'md',
    typedDocument:{kind:'prompt',pipelineId:expected.pipeline,expectedRevision:1,content:promptText,contract:{capabilityId:'image:seedream',params:{size:'1024x1024',quality:'medium'}}},
    output:{resources:[{key:'prompt',mode:'new_version',name:'final_video_prompt.md',baseVersionId:expected.promptV1}],composition:{name:'口播成片',type:{label:'视频项目'},placements:[{key:'prompt',role:'prompt'}]}}
   })).data;
   expected.promptV2=promptDoc2.delivery.versions[0].versionId;
   const promptView2=await get('/api/pipeline/prompt'+query+'&versionId='+expected.promptV2);
   const compiled3=promptView2.contract;
   assert.equal(compiled3.storyboardRevision,2);

   // 5. Failure Return Path 3: Upstream temporary failure -> no auto-retry -> human "原样重试" -> billing confirmation -> delivered media Version -> Canvas
   imageMode='upstream_error';
   const postsBeforeUpstream=imagePosts;
   const upTaskId=await submitContractTask('gen-upstream-1',compiled3.id,{size:'1024x1024',quality:'medium'},'主关键帧');
   await wait(`document.querySelector('[data-task-id="${upTaskId}"]')?.innerText.includes('上游临时故障 (upstream)')`);
   assert.equal(imagePosts,postsBeforeUpstream+1,'upstream failure must not auto-retry');
   imageMode='ok';
   await click('原样重试',`document.querySelector('[data-task-id="${upTaskId}"]')`);
   await wait(`document.querySelector('[data-task-id="${upTaskId}"]')?.innerText.includes('等待确认')`);
   const retryBill=(await get('/api/question'+query)).items.find(q=>q.kind==='billing'&&q.task_id===upTaskId&&q.state==='pending');
   assert(retryBill);
   await click('确认生成',`document.querySelector('[data-question-id="${retryBill.id}"]')`);
   await wait(`document.querySelector('[data-task-id="${upTaskId}"]')?.innerText.includes('已完成')`);
   const completedTask=(await get('/api/tasks'+query)).items.find(t=>t.id===upTaskId);
   expected.mediaV1=completedTask.outputs[0].version_id;
   expected.mediaResource=completedTask.outputs[0].resource_id;
   expected.upTaskId=upTaskId;

   // 6. Delivered media Version enters M3 ImageEditor (annotation -> mask -> new_version on same media Resource) preserving full SQL lineage!
   const canvasWithMedia=await get('/api/canvas/nodes'+query);
   const allNodes=Object.values(canvasWithMedia.nodes);
   const mediaNode=allNodes.find(n=>n.activeItem?.versionId===expected.mediaV1);
   const sbNode2=allNodes.find(n=>n.id===sbNode.id);
   const prNode2=allNodes.find(n=>n.activeItem?.versionId===expected.promptV2);
   assert(mediaNode,'delivered media version must be placed on Canvas');
   await post('/api/canvas/commands'+query,{requestId:'layout-media',expectedRevision:canvasWithMedia.revision,command:{type:'canvas.patch',nodes:[
    {id:sbNode2.id,before:sbNode2,after:{...sbNode2,position:{x:20,y:280},size:{width:460,height:460}}},
    {id:prNode2.id,before:prNode2,after:{...prNode2,position:{x:500,y:280},size:{width:460,height:460}}},
    {id:mediaNode.id,before:mediaNode,after:{...mediaNode,position:{x:980,y:20},size:{width:360,height:320}}}
   ],edges:[]}});
   await wait(`!!document.querySelector('[data-node-id="${mediaNode.id}"] img')`);
   await click('在图像编辑器打开',`document.querySelector('[data-node-id="${mediaNode.id}"]')`);
   await wait(`!!document.querySelector('.editor-stage canvas')`);
   await evaluate(`document.querySelector('.image-editor').scrollIntoView()`);
   await click('区域标注');
   const box=await evaluate(`(()=>{const r=document.querySelector('.editor-stage canvas').getBoundingClientRect();return {x:r.x,y:r.y}})()`);
   for(const [type,x,y] of [['mousePressed',20,20],['mouseMoved',100,90],['mouseReleased',100,90]]){
    await call('Input.dispatchMouseEvent',{type,x:box.x+x,y:box.y+y,button:'left',buttons:type==='mouseMoved'?1:undefined,clickCount:1});
   }
   await wait(`!!document.querySelector('textarea[aria-label="区域说明"]')`);
   await call('Input.insertText',{text:'局部光影微调'});
   await call('Input.dispatchKeyEvent',{type:'keyDown',key:'Enter',code:'Enter'});
   await wait(`!!document.querySelector('[aria-label="区域标注列表"]')`);
   await click('用标注创建修复蒙版');
   await wait(`!!document.querySelector('.image-editor .generation-composer')`);
   await click('刷新连接',`document.querySelector('.image-editor .generation-composer')`);
   await wait(`!!document.querySelector('.image-editor select[aria-label="图像连接"] option[value="seedream-edit"]')`);
   await fill('.image-editor select[aria-label="图像连接"]','seedream-edit');
   await fill('.image-editor textarea[aria-label="生成提示词"]','Refine lighting on product edge');
   await click('请求费用确认',`document.querySelector('.image-editor .generation-composer')`);
   await wait(`document.querySelector('.image-editor .generation-composer')?.innerText.includes('任务已创建，请在费用卡片上批准或拒绝。')`);
   const editBill=(await get('/api/question'+query)).items.find(q=>q.kind==='billing'&&q.state==='pending');
   assert(editBill);
   await click('确认生成',`document.querySelector('[data-question-id="${editBill.id}"]')`);
   await wait(`document.querySelector('[data-task-id="${editBill.task_id}"]')?.innerText.includes('已完成')`);
   assert.equal(imageEdits,1);
   const editTask=(await get('/api/tasks'+query)).items.find(t=>t.id===editBill.task_id);
   expected.mediaV2=editTask.outputs[0].version_id;
   assert.equal(editTask.outputs[0].resource_id,expected.mediaResource,'M3 editor repair must create a new Version on the same media Resource');
   assert.notEqual(expected.mediaV2,expected.mediaV1);

   // Verify full SQL lineage from mediaV2 all the way back to intent/script/storyboard/prompt/contract
   const lineage=await get('/api/pipeline/lineage'+query+'&versionId='+expected.mediaV2);
   assert.equal(lineage.intent.revision,1);
   assert.equal(lineage.script.revision,1);
   assert.equal(lineage.storyboard.revision,2);
   assert.equal(lineage.prompt.revision,2);
   assert.equal(lineage.contract.id,compiled3.id);
   assert(lineage.outputs.some(o=>o.versionId===expected.mediaV1));
   assert(lineage.outputs.some(o=>o.versionId===expected.mediaV2&&o.sourceVersionId===expected.mediaV1));
   expected.lineage=lineage;

   await writeFile(join(dir,'pipeline-recovery-result.json'),JSON.stringify({
    qualification:'synthetic local LLM/image fixture; M6 video adapter explicitly marked test-only (real ark-video is M7.1)',
    imagePosts,imageEdits,paramTaskId,semTaskId,upTaskId,editTaskId:editBill.task_id,
    mediaResource:expected.mediaResource,mediaV1:expected.mediaV1,mediaV2:expected.mediaV2,lineage,exceptions
   },null,2));
  }else{
   // Phase: restart — verify full persistence across physical desktop restart with 0 control POSTs
   await wait(`document.querySelector('[data-task-id="${expected.upTaskId}"]')?.innerText.includes('已完成')`);
   assert.equal(models.length,0);
   assert.equal(imagePosts,0);
   const lineageAfterRestart=await get('/api/pipeline/lineage'+query+'&versionId='+expected.mediaV2);
   assert.deepEqual(lineageAfterRestart,expected.lineage);
   assert.equal(requests.filter(r=>r.method==='POST'&&r.url.includes('/api/')&&new URL(r.url).pathname!=='/api/canvas/content').length,0);
   await writeFile(join(dir,'pipeline-restart-result.json'),JSON.stringify({lineageAfterRestart,requests,exceptions},null,2));
  }
 }
 assert.deepEqual(exceptions,[]);
 await evaluate(`document.querySelector('.object-surface').scrollIntoView({block:'start'})`);
 await writeFile(join(dir,`pipeline-${phase}.png`),Buffer.from((await call('Page.captureScreenshot',{format:'png'})).data,'base64'));
 await writeFile(join(dir,'pipeline-expected.json'),JSON.stringify(expected,null,2));
 console.log(`M6.5 pipeline workflow & failure recovery ${phase} passed: ${dir}`);
}catch(e){
 await writeFile(join(dir,`pipeline-${phase}-failure.txt`),String(e)+'\n'+JSON.stringify(requests)+'\n'+await evaluate('document.body.innerText'));
 throw e;
}finally{
 close();
 if(fixture)await new Promise(r=>fixture.close(r));
}
