// Production WebView + pinned owned native OpenCode + local loopback ark-video supplier.
// End-to-end M7.2 smoke: 15s oral narration + background music video, preflight gates,
// collection retry without re-POST, Canvas <video> player, version revisioning, and SQL lineage.
import assert from 'node:assert/strict';
import {execFileSync} from 'node:child_process';
import {createServer} from 'node:http';
import {readFile,writeFile} from 'node:fs/promises';
import {join} from 'node:path';
import {connect} from './webview-cdp.mjs';

const [port,dir,origin,phase='initial']=process.argv.slice(2);
let expected,fixture,models=[],videoPosts=0,videoGets=0,downloadCalls=0,downloadMode='ok',lastVideoWire=null,mp4V1Bytes=null,mp4V2Bytes=null;
if(phase!=='initial'){
 expected=JSON.parse(await readFile(join(dir,'video-expected.json'),'utf8'));
 await supplier(expected.supplierPort);
}
const {call,evaluate,wait,requests,exceptions,close}=await connect(port,origin);
const click=async(text,scope='document')=>{await wait(`(()=>{const b=[...${scope}.querySelectorAll('button')].find(x=>!x.disabled&&x.textContent.trim()===${JSON.stringify(text)});return b&&!b.disabled})()`);await evaluate(`[...${scope}.querySelectorAll('button')].find(x=>!x.disabled&&x.textContent.trim()===${JSON.stringify(text)}).click()`)};
const get=path=>evaluate(`fetch('http://127.0.0.1:8001'+${JSON.stringify(path)}).then(async r=>{if(!r.ok)throw Error(await r.text());return r.json()})`);
const post=(path,body)=>evaluate(`fetch('http://127.0.0.1:8001'+${JSON.stringify(path)},{method:'POST',headers:{'Content-Type':'application/json'},body:${JSON.stringify(JSON.stringify(body))}}).then(async r=>({status:r.status,data:await r.json()}))`);
const fill=(selector,value)=>evaluate(`(()=>{const e=document.querySelector(${JSON.stringify(selector)});if(!e)throw Error('Missing '+${JSON.stringify(selector)});Object.getOwnPropertyDescriptor(Object.getPrototypeOf(e),'value').set.call(e,${JSON.stringify(value)});e.dispatchEvent(new Event(e.tagName==='SELECT'?'change':'input',{bubbles:true}))})()`);
const fillLabel=(label,value)=>evaluate(`(()=>{const e=[...document.querySelectorAll('label')].find(x=>x.textContent.startsWith(${JSON.stringify(label)})).querySelector('input');Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(e,${JSON.stringify(value)});e.dispatchEvent(new Event('input',{bubbles:true}))})()`);
const settled=()=>wait(`![...document.querySelectorAll('.canvas-toolbar button')].find(b=>b.textContent==='添加画框')?.disabled`);

const storyboard={
 panels:[
  {id:'p1',timeMs:0,framing:'近景',action:'讲述者手持产品向镜头口播核心卖点',transition:'切'},
  {id:'p2',timeMs:7000,framing:'中景',action:'随手势拉远展示产品全景与配乐收束',transition:'慢推'},
 ],
 shots:[
  {id:'s1',startMs:0,endMs:7000,panelIds:['p1'],narrativeRole:'引出核心卖点',startState:'人物静止持物',endState:'手势展开口播'},
  {id:'s2',startMs:7000,endMs:15000,panelIds:['p2'],narrativeRole:'细节展示与音乐尾奏',startState:'手势展开',endState:'微笑定格'},
 ],
 timeline:{durationMs:15000,shotIds:['s1','s2'],audioIntent:'15s人声口播+背景配乐'},
};

function ensureMP4Fixtures(){
 const v1Path=join(dir,'fixture-v1.mp4');
 const v2Path=join(dir,'fixture-v2.mp4');
 if(phase==='initial'){
  execFileSync('ffmpeg',['-y','-f','lavfi','-i','color=c=0x1e3a5f:s=1280x720:r=5:d=15','-f','lavfi','-i','sine=frequency=440:duration=15','-c:v','libopenh264','-pix_fmt','yuv420p','-c:a','aac','-b:a','64k','-movflags','+faststart',v1Path],{stdio:'ignore'});
  execFileSync('ffmpeg',['-y','-f','lavfi','-i','color=c=0x2a6f4e:s=1280x720:r=5:d=15','-f','lavfi','-i','sine=frequency=523:duration=15','-c:v','libopenh264','-pix_fmt','yuv420p','-c:a','aac','-b:a','64k','-movflags','+faststart',v2Path],{stdio:'ignore'});
 }
 return Promise.all([readFile(v1Path),readFile(v2Path)]);
}

async function supplier(listenPort=0){
 [mp4V1Bytes,mp4V2Bytes]=await ensureMP4Fixtures();
 fixture=createServer(async(req,res)=>{
  let raw='';for await(const c of req)raw+=c;
  if(req.method==='POST'&&req.url==='/v1/contents/generations/tasks'){
   videoPosts++;
   lastVideoWire=JSON.parse(raw);
   res.writeHead(200,{'content-type':'application/json'});
   res.end(JSON.stringify({id:'cgt-smoke-vid-001',status:'queued'}));
   return;
  }
  if(req.method==='GET'&&req.url==='/v1/contents/generations/tasks/cgt-smoke-vid-001'){
   videoGets++;
   res.writeHead(200,{'content-type':'application/json'});
   res.end(JSON.stringify({
    id:'cgt-smoke-vid-001',
    status:videoGets===1?'running':'succeeded',
    content:videoGets===1?{}:{video_url:`http://127.0.0.1:${fixture.address().port}/cdn/e2e-15s.mp4`}
   }));
   return;
  }
  if(req.method==='GET'&&req.url==='/cdn/e2e-15s.mp4'){
   downloadCalls++;
   if(downloadMode==='truncate'){
    res.writeHead(200,{'content-type':'video/mp4','content-length':String(mp4V1Bytes.length)});
    res.write(mp4V1Bytes.subarray(0,24));
    res.destroy();
    return;
   }
   res.writeHead(200,{'content-type':'video/mp4','content-length':String(mp4V1Bytes.length)});
   res.end(mp4V1Bytes);
   return;
  }
  if(req.method==='GET'){
   res.writeHead(200,{'content-type':'application/json'});
   res.end('{"data":[{"id":"video-fixture"}]}');
   return;
  }
  assert.equal(req.url,'/v1/chat/completions');
  const body=JSON.parse(raw),names=(body.tools??[]).map(t=>t.function.name).sort();
  assert.equal(names.length,19);assert(names.includes('question'));
  models.push(body);
  res.writeHead(200,{'content-type':'text/event-stream'});
  const emit=(delta,finish=null)=>res.write(`data: ${JSON.stringify({id:'fixture',object:'chat.completion.chunk',created:1,model:'video-fixture',choices:[{index:0,delta,finish_reason:finish}]})}\n\n`);
  if(models.length%2===1){
   const gate=models.length===1?'intent':models.length===3?'script':'storyboard';
   const typedDocument=gate==='storyboard'
    ?{kind:gate,pipelineId:expected.pipeline,expectedRevision:0,storyboard}
    :{kind:gate,pipelineId:expected.pipeline,expectedRevision:0,content:gate==='intent'?'创作意图：15秒人声口播配轻快背景音乐。':'完整脚本：讲述者手持产品向镜头口播核心卖点，随手势拉远展示全景。'};
   emit({role:'assistant',tool_calls:[{index:0,id:`${gate}-call`,type:'function',function:{name:'design_design_document_write',arguments:JSON.stringify({requestId:`native-vid-${gate}`,mode:'create',format:'md',typedDocument,output:{resources:[{key:gate==='storyboard'?'review':gate,mode:'create',name:gate==='storyboard'?'storyboard_review.md':gate+'.md'}],composition:{name:'口播成片',type:{label:'视频项目'},placements:[{key:gate==='storyboard'?'review':gate,role:gate}]}}})}}]});
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

async function stageAndDeliverRefImage(query,key,name,colorHex){
 const pngBase64=await evaluate(`(()=>{const c=document.createElement('canvas');c.width=320;c.height=180;const x=c.getContext('2d');x.fillStyle=${JSON.stringify(colorHex)};x.fillRect(0,0,320,180);return c.toDataURL('image/png').split(',')[1]})()`);
 const staged=await evaluate(`(async()=>{const bytes=Uint8Array.from(atob(${JSON.stringify(pngBase64)}),c=>c.charCodeAt(0));const fd=new FormData();fd.append('requestId',${JSON.stringify('stage-'+key)});fd.append('file',new Blob([bytes],{type:'image/png'}),${JSON.stringify(name+'.png')});const r=await fetch('http://127.0.0.1:8001/api/upload'+${JSON.stringify(query)},{method:'POST',body:fd});if(!r.ok)throw Error(await r.text());return r.json()})()`);
 const delivered=(await post('/api/deliver'+query,{
  requestId:'deliver-'+key,
  sources:[{key,path:staged.path}],
  output:{
   resources:[{key,mode:'create',name}],
   composition:{name:'参考图组',type:{label:'image'},placements:[{key,role:'reference'}]}
  }
 })).data;
 const v=delivered.delivery.versions[0];
 return {assetId:v.assetId,versionId:v.versionId};
}

try{
 await wait(`document.body?.innerText.includes('本地服务已连接')`);
 if(phase==='initial'){
  expected={supplierPort:await supplier(),pipeline:crypto.randomUUID()};
  await click('供应商设置');
  await fillLabel('LLM Base URL',`http://127.0.0.1:${expected.supplierPort}/v1`);
  await fillLabel('LLM Model ID','video-fixture');
  await fillLabel('LLM API 密钥','synthetic-video-llm-key');
  await click('保存 LLM 连接');
  await wait(`document.body.innerText.includes('重启后生效')`);
  await click('创作画布');
  await fill('#project-name','M7.2 15秒口播配乐成片与血缘验收');
  await evaluate(`document.querySelector('.project-picker form').requestSubmit()`);
  await wait(`!!document.querySelector('.chat-panel')`);
  expected.p=await evaluate(`document.querySelector('#project').value`);
  await click('新建对话');
  await wait(`!!document.querySelector('[aria-label="当前对话"]').value`);
  expected.s=await evaluate(`document.querySelector('[aria-label="当前对话"]').value`);
 }else{
  const query=`?project_id=${expected.p}`;
  await wait(`document.querySelector('[aria-label="当前对话"]')?.value===${JSON.stringify(expected.s)}`);
  await wait(`fetch('http://127.0.0.1:8001/api/agent/status').then(r=>r.json()).then(s=>!!s.host_ready)`);
  assert.equal(requests.filter(r=>r.method==='POST'&&r.url.includes('/api/')&&new URL(r.url).pathname!=='/api/canvas/content').length,0,'restart performs no control writes');

  if(phase==='recovery'){
   // 1. One-sentence workflow -> Intent -> Script -> Storyboard rev 1 -> Human approvals
   for(const [n,label] of ['意图','脚本','分镜'].entries()){
    await evaluate(`document.querySelector('.chat-composer .tiptap').editor.commands.setContent('<p>${n===0?'用这两张产品图做个15秒人声口播配轻快音乐的视频，先确认意图。':n===1?'意图已批准，请生成完整脚本。':'脚本已批准，请生成15秒语义分镜。'}</p>')`);
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

   // 2. Stage and deliver 2 ordered reference images into the project
   const ref1=await stageAndDeliverRefImage(query,'ref_hero','产品主图','#1f4e68');
   const ref2=await stageAndDeliverRefImage(query,'ref_detail','产品细节图','#b85d19');
   expected.ref1=ref1;
   expected.ref2=ref2;

   // 3. Configure video providers (10s-only, silent, and full 15s audio ark-video)
   const baseVidCfg={
    id:'seedance',name:'Seedance 2.0 Ark Video',kind:'video',protocol:'ark-video',
    base_url:`http://127.0.0.1:${expected.supplierPort}/v1`,model_id:'seedance-2.0',enabled:true,headers:{},
    capability:{max_prompt_bytes:8192,max_count:1,max_references:2,sizes:['1280x720'],edit:false,duration_seconds:[5,10,15],audio:true},
    pricing:{currency:'USD',unit:'second',unit_amount:10,minor_unit_scale:2,credit_numerator:1,credit_denominator:1,source:'declared',version:'v1',effective_at:0}
   };
   assert.equal((await post('/api/providers',{requestId:'cfg-seedance',action:'create',config:baseVidCfg,key:'synthetic-ark-video-key'})).status,200);
   const shortVidCfg={...baseVidCfg,id:'seedance-10s',name:'Seedance 10s Only',capability:{...baseVidCfg.capability,duration_seconds:[5,10]}};
   assert.equal((await post('/api/providers',{requestId:'cfg-seedance-10s',action:'create',config:shortVidCfg,key:'synthetic-ark-video-key'})).status,200);
   const silentVidCfg={...baseVidCfg,id:'seedance-silent',name:'Seedance Silent',capability:{...baseVidCfg.capability,audio:false}};
   assert.equal((await post('/api/providers',{requestId:'cfg-seedance-silent',action:'create',config:silentVidCfg,key:'synthetic-ark-video-key'})).status,200);

   // 4. Write exact final_video_prompt.md with 15s oral narration + music contract and ordered references
   const promptText='镜头一 [00:00-00:07] [近景]：讲述者手持产品向镜头口播核心卖点，轻快背景音乐渐入。\n镜头二 [00:07-00:15] [中景]：随手势拉远展示产品细节，口播收束，音乐自然尾奏。 ';
   // Verify preflight blocks 10s-only and silent video capabilities before billing
   const blockedShort=await post('/api/documents/write'+query,{
    requestId:'wf-prompt-short-blocked',mode:'create',format:'md',
    typedDocument:{kind:'prompt',pipelineId:expected.pipeline,expectedRevision:0,content:promptText,contract:{capabilityId:'video:seedance-10s',params:{duration:15,size:'1280x720',aspect_ratio:'16:9',generate_audio:true,audio_mode:'narration+music'},references:[ref1.assetId,ref2.assetId]}},
    output:{resources:[{key:'prompt',mode:'create',name:'final_video_prompt.md'}],composition:{name:'口播成片',type:{label:'视频项目'},placements:[{key:'prompt',role:'prompt'}]}}
   });
   assert.equal(blockedShort.data.delivery.status,'blocked');
   const blockedSilent=await post('/api/documents/write'+query,{
    requestId:'wf-prompt-silent-blocked',mode:'create',format:'md',
    typedDocument:{kind:'prompt',pipelineId:expected.pipeline,expectedRevision:0,content:promptText,contract:{capabilityId:'video:seedance-silent',params:{duration:15,size:'1280x720',aspect_ratio:'16:9',generate_audio:true,audio_mode:'narration+music'},references:[ref1.assetId,ref2.assetId]}},
    output:{resources:[{key:'prompt',mode:'create',name:'final_video_prompt.md'}],composition:{name:'口播成片',type:{label:'视频项目'},placements:[{key:'prompt',role:'prompt'}]}}
   });
   assert.equal(blockedSilent.data.delivery.status,'blocked');

   // Write valid 15s oral narration + music prompt & contract
   const promptDoc=(await post('/api/documents/write'+query,{
    requestId:'wf-vid-prompt-v1',mode:'create',format:'md',
    typedDocument:{kind:'prompt',pipelineId:expected.pipeline,expectedRevision:0,content:promptText,contract:{capabilityId:'video:seedance',params:{duration:15,size:'1280x720',aspect_ratio:'16:9',resolution:'720p',generate_audio:true,audio_mode:'narration+music'},references:[ref1.assetId,ref2.assetId]}},
    output:{resources:[{key:'prompt',mode:'create',name:'final_video_prompt.md'}],composition:{name:'口播成片',type:{label:'视频项目'},placements:[{key:'prompt',role:'prompt'}]}}
   })).data;
   assert.equal(promptDoc.delivery.status,'completed');
   expected.promptV1=promptDoc.delivery.versions[0].versionId;
   const promptView=await get('/api/pipeline/prompt'+query+'&versionId='+expected.promptV1);
   expected.contractId=promptView.contract.id;

   // Zoom out Canvas viewport so all 6 nodes (intent, script, storyboard, ref1, ref2, prompt) are in visibleNodes
   await evaluate(`(()=>{const s=document.querySelector('.object-surface'),b=s.getBoundingClientRect();s.dispatchEvent(new WheelEvent('wheel',{deltaY:1600,clientX:b.left+10,clientY:b.top+10,bubbles:true}))})()`);

   await wait(`!!document.querySelector('.prompt-review [data-video-adapter-status="ark-video-ready"]')`);
   await wait(`document.querySelector('.prompt-review')?.innerText.includes('Preflight 七项门禁：全部通过')`);

   // 5. Submit video generation task; first download interrupts mid-stream -> collection retry succeeds without re-POST!
   downloadMode='truncate';
   const genRes=await post('/api/generate/video'+query,{
    requestId:'gen-vid-15s-1',
    session_id:expected.s,
    capability_id:'video:seedance',
    contract_id:expected.contractId,
    prompt:promptText,
    params:{duration:15,size:'1280x720',aspect_ratio:'16:9',resolution:'720p',generate_audio:true,audio_mode:'narration+music'},
    references:[ref1.assetId,ref2.assetId],
    count:1,
    output:{
     resources:[{key:'oral_video',mode:'create',name:'15秒口播配乐成片'}],
     composition:{name:'口播成片',type:{label:'视频项目'},placements:[{key:'oral_video',role:'video'}]}
    }
   });
   assert.equal(genRes.status,202,JSON.stringify(genRes.data));
   expected.videoTaskId=genRes.data.task_id;

   const qItems=(await get('/api/question'+query)).items;
   const bill=qItems.find(q=>q.kind==='billing'&&q.task_id===expected.videoTaskId&&q.state==='pending');
   assert(bill,'billing question must exist for video generation');
   await click('确认生成',`document.querySelector('[data-question-id="${bill.id}"]')`);

   // Wait for interrupted download failure in TaskCenter
   await wait(`document.querySelector('[data-task-id="${expected.videoTaskId}"]')?.innerText.includes('未完成')`);
   assert.equal(videoPosts,1,'must POST video generation exactly once');
   assert.equal(lastVideoWire.content[0].text,promptText,'submitted prompt bytes must be byte-identical without mutation');
   assert.equal(lastVideoWire.content.length,3,'must include 1 text + 2 ordered reference image_url items');
   assert.equal(lastVideoWire.duration,15);
   assert.equal(lastVideoWire.generate_audio,true);
   assert.equal(lastVideoWire.audio_mode,'narration+music');

   // Switch downloadMode to 'ok' and click "继续收集原结果" -> retries collection on same attempt without re-POSTing!
   downloadMode='ok';
   await click('继续收集原结果',`document.querySelector('[data-task-id="${expected.videoTaskId}"]')`);
   await wait(`document.querySelector('[data-task-id="${expected.videoTaskId}"]')?.innerText.includes('已完成')`);
   assert.equal(videoPosts,1,'collection retry must NEVER re-POST video generation');

   const taskDone=await get(`/api/tasks/${expected.videoTaskId}`+query);
   assert.equal(taskDone.status,'completed');
   expected.videoV1=taskDone.outputs[0].version_id;
   expected.videoAsset1=taskDone.outputs[0].asset_id;
   expected.videoResource=taskDone.outputs[0].resource_id;

   // 6. Stage and deliver revised video Version (v2) on the same video Resource + verify idempotent replay & conflict
   const stagedV2=await evaluate(`(async()=>{const bytes=Uint8Array.from(atob(${JSON.stringify(mp4V2Bytes.toString('base64'))}),c=>c.charCodeAt(0));const fd=new FormData();fd.append('requestId','stage-vid-v2');fd.append('file',new Blob([bytes],{type:'video/mp4'}),'oral-v2.mp4');const r=await fetch('http://127.0.0.1:8001/api/upload'+${JSON.stringify(query)},{method:'POST',body:fd});if(!r.ok)throw Error(await r.text());return r.json()})()`);
   const delBody={
    requestId:'deliver-vid-v2',
    sources:[{key:'oral_video',path:stagedV2.path}],
    output:{
     resources:[{key:'oral_video',mode:'new_version',name:'15秒口播配乐成片',baseVersionId:expected.videoV1}],
     composition:{name:'口播成片',type:{label:'视频项目'},placements:[{key:'oral_video',role:'video'}]}
    }
   };
   const delRes1=await post('/api/deliver'+query,delBody);
   assert.equal(delRes1.status,200);
   expected.videoV2=delRes1.data.delivery.versions[0].versionId;
   assert.equal(delRes1.data.delivery.versions[0].resourceId,expected.videoResource);
   assert.notEqual(expected.videoV2,expected.videoV1);

   // Idempotent replay with same requestId -> returns identical videoV2
   const delReplay=await post('/api/deliver'+query,delBody);
   assert.equal(delReplay.status,200);
   assert.equal(delReplay.data.delivery.versions[0].versionId,expected.videoV2);

   // Conflicting replay with same requestId -> 409
   const delConflict=await post('/api/deliver'+query,{
    ...delBody,
    output:{...delBody.output,resources:[{key:'oral_video',mode:'new_version',name:'冲突改名',baseVersionId:expected.videoV1}]}
   });
   assert.equal(delConflict.status,409);

   // 7. Verify Video player node on Canvas, <video> decoding, and version switching
   const canvasAfter=await get('/api/canvas/nodes'+query);
   const vidNode=Object.values(canvasAfter.nodes).find(n=>n.activeItem?.resourceId===expected.videoResource||n.activeItem?.versionId===expected.videoV2||n.activeItem?.versionId===expected.videoV1);
   assert(vidNode,'delivered video node must exist on Canvas');
   expected.videoNodeId=vidNode.id;

   await wait(`(()=>{const v=document.querySelector('.video-player-card video');return v&&v.readyState>=1&&v.videoWidth===1280&&v.videoHeight===720&&Math.abs(v.duration-15)<0.5})()`);
   await wait(`document.querySelector('.video-player-card .video-meta')?.innerText.includes('15s · 1280×720')&&document.querySelector('.video-player-card .video-meta')?.innerText.includes('音轨 1')`);

   // Switch video node version back to v1 and then back to v2 via Canvas property panel
   await evaluate(`document.querySelector('[data-node-id="${expected.videoNodeId}"]').focus()`);
   await wait(`document.querySelector('select[aria-label="画布采用版本"]')?.options.length>=2`);
   await fill('select[aria-label="画布采用版本"]',expected.videoV1);
   await settled();
   await wait(`document.querySelector('.video-player-card')?.getAttribute('data-version-id')===${JSON.stringify(expected.videoV1)}`);
   await fill('select[aria-label="画布采用版本"]',expected.videoV2);
   await settled();
   await wait(`document.querySelector('.video-player-card')?.getAttribute('data-version-id')===${JSON.stringify(expected.videoV2)}`);

   // 8. Verify full SQL lineage from videoV2 back to intent/script/storyboard/prompt/contract + ordered references
   const lineage=await get(`/api/pipeline/lineage${query}&versionId=${expected.videoV2}`);
   assert.equal(lineage.intent?.documentVersionId,expected.intentCard.creative.documentVersionId);
   assert.equal(lineage.script?.documentVersionId,expected.scriptCard.creative.documentVersionId);
   assert.equal(lineage.storyboard?.documentVersionId,expected.sbRev1Version);
   assert.equal(lineage.prompt?.documentVersionId,expected.promptV1);
   assert.equal(lineage.contract?.id,expected.contractId);
   assert.equal(lineage.references?.length,2);
   assert.equal(lineage.references[0].assetId,ref1.assetId);
   assert.equal(lineage.references[1].assetId,ref2.assetId);
   assert(lineage.outputs.some(o=>o.versionId===expected.videoV1));
   assert(lineage.outputs.some(o=>o.versionId===expected.videoV2));
   expected.lineage=lineage;
   await writeFile(join(dir,'video-evidence.json'),JSON.stringify({expected,lineage,videoPosts,videoGets,downloadCalls},null,2));
  }else{
   // Phase 3: Physical restart recovery verification
   assert.equal(videoPosts,0,'physical restart must not resubmit completed video task');
   await wait(`(()=>{const c=document.querySelector('.video-player-card'),v=c?.querySelector('video');return c?.getAttribute('data-version-id')===${JSON.stringify(expected.videoV2)}&&v&&v.readyState>=1&&v.videoWidth===1280&&v.videoHeight===720})()`);
   await wait(`document.querySelector('.video-player-card .video-meta')?.innerText.includes('15s · 1280×720')&&document.querySelector('.video-player-card .video-meta')?.innerText.includes('音轨 1')`);
   const lineageAfterRestart=await get(`/api/pipeline/lineage${query}&versionId=${expected.videoV2}`);
   assert.deepEqual(lineageAfterRestart,expected.lineage);
  }
 }
 assert.equal(exceptions.length,0,`Frontend exceptions: ${exceptions.join('; ')}`);
 await evaluate(`document.querySelector('.object-surface')?.scrollIntoView({block:'start'})`);
 await writeFile(join(dir,'video-expected.json'),JSON.stringify(expected,null,2));
 const shot=await call('Page.captureScreenshot',{format:'png'});
 await writeFile(join(dir,`video-${phase}.png`),Buffer.from(shot.data,'base64'));
 console.log(`M7.2 oral video & lineage ${phase} passed: ${dir}`);
}catch(e){
 await writeFile(join(dir,`video-${phase}-failure.txt`),String(e)+'\n'+JSON.stringify(requests)+'\n'+await evaluate('document.body?.innerText||""'));
 throw e;
}finally{
 close();
 if(fixture)await new Promise(r=>fixture.close(r));
}
