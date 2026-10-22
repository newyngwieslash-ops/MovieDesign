// Production WebView + owned sidecars + loopback suppliers.
// M7.3 interruption/recovery/cancellation matrix, TaskCenter billing/attempt/stage UI,
// non-overlapping Canvas nodes, compact resolved confirmation cards, and /health safe_to_suspend.
import assert from 'node:assert/strict';
import {execFileSync} from 'node:child_process';
import {createServer} from 'node:http';
import {readFile,writeFile} from 'node:fs/promises';
import {join} from 'node:path';
import {connect} from './webview-cdp.mjs';

const [port,dir,origin,phase='initial']=process.argv.slice(2);
let expected,fixture,imgPosts=0,imgDownloads=0,truncateNextImg=false,videoPosts=0,videoGets={},videoDeletes=0,videoStates={},mp4Bytes=null,pngBytes=null;
if(phase!=='initial'){
 expected=JSON.parse(await readFile(join(dir,'interruptions-expected.json'),'utf8'));
 videoStates=expected.videoStates??{};
 await startSupplier(expected.supplierPort);
}
const {call,evaluate,wait,requests,exceptions,close}=await connect(port,origin);
const click=async(text,scope='document')=>{await wait(`(()=>{const b=[...${scope}.querySelectorAll('button')].find(x=>!x.disabled&&x.textContent.trim()===${JSON.stringify(text)});return b&&!b.disabled})()`);await evaluate(`[...${scope}.querySelectorAll('button')].find(x=>!x.disabled&&x.textContent.trim()===${JSON.stringify(text)}).click()`)};
const get=path=>evaluate(`fetch('http://127.0.0.1:8001'+${JSON.stringify(path)}).then(async r=>{if(!r.ok)throw Error(await r.text());return r.json()})`);
const post=(path,body)=>evaluate(`fetch('http://127.0.0.1:8001'+${JSON.stringify(path)},{method:'POST',headers:{'Content-Type':'application/json'},body:${JSON.stringify(JSON.stringify(body))}}).then(async r=>({status:r.status,data:await r.json()}))`);
const fill=(selector,value)=>evaluate(`(()=>{const e=document.querySelector(${JSON.stringify(selector)});if(!e)throw Error('Missing '+${JSON.stringify(selector)});Object.getOwnPropertyDescriptor(Object.getPrototypeOf(e),'value').set.call(e,${JSON.stringify(value)});e.dispatchEvent(new Event(e.tagName==='SELECT'?'change':'input',{bubbles:true}))})()`);

async function ensureMediaFixtures(){
 const mp4Path=join(dir,'fixture-int.mp4');
 if(phase==='initial'){
  execFileSync('ffmpeg',['-y','-f','lavfi','-i','color=c=0x1e3a5f:s=1280x720:r=5:d=15','-f','lavfi','-i','sine=frequency=440:duration=15','-c:v','libopenh264','-pix_fmt','yuv420p','-c:a','aac','-b:a','64k','-movflags','+faststart',mp4Path],{stdio:'ignore'});
 }
 return readFile(mp4Path);
}

async function startSupplier(listenPort=0){
 mp4Bytes=await ensureMediaFixtures();
 fixture=createServer(async(req,res)=>{
  let raw='';for await(const c of req)raw+=c;
  if(req.method==='POST'&&req.url==='/images/generations'){
   imgPosts++;
   res.writeHead(200,{'content-type':'application/json'});
   res.end(JSON.stringify({data:[{url:`http://127.0.0.1:${fixture.address().port}/cdn/img.png`}]}));
   return;
  }
  if(req.method==='GET'&&req.url==='/cdn/img.png'){
   imgDownloads++;
   if(truncateNextImg){
    truncateNextImg=false;
    res.writeHead(200,{'content-type':'image/png','content-length':String(pngBytes.length+100),'connection':'close'});
    res.end(pngBytes.subarray(0,12));
    return;
   }
   res.writeHead(200,{'content-type':'image/png','content-length':String(pngBytes.length)});
   res.end(pngBytes);
   return;
  }
  if(req.method==='POST'&&req.url==='/v1/contents/generations/tasks'){
   videoPosts++;
   const body=JSON.parse(raw);
   const promptText=body.content?.[0]?.text??'';
   const id=promptText.includes('QUEUED_CANCEL')?'cgt-int-queued':promptText.includes('RUNNING_CANCEL')?'cgt-int-running':'cgt-int-restart';
   if(!videoStates[id])videoStates[id]=id==='cgt-int-queued'?'queued':'running';
   res.writeHead(200,{'content-type':'application/json'});
   res.end(JSON.stringify({id,status:videoStates[id]}));
   return;
  }
  if(req.method==='GET'&&req.url?.startsWith('/v1/contents/generations/tasks/')){
   const id=req.url.split('/').pop();
   videoGets[id]=(videoGets[id]??0)+1;
   const st=videoStates[id]??'running';
   res.writeHead(200,{'content-type':'application/json'});
   res.end(JSON.stringify({
    id,
    status:st,
    content:st==='succeeded'?{video_url:`http://127.0.0.1:${fixture.address().port}/cdn/${id}.mp4`}:{}
   }));
   return;
  }
  if(req.method==='DELETE'&&req.url?.startsWith('/v1/contents/generations/tasks/')){
   const id=req.url.split('/').pop();
   videoDeletes++;
   videoStates[id]='cancelled';
   res.writeHead(200,{'content-type':'application/json'});
   res.end('{}');
   return;
  }
  if(req.method==='GET'&&req.url?.startsWith('/cdn/cgt-int-')){
   res.writeHead(200,{'content-type':'video/mp4','content-length':String(mp4Bytes.length)});
   res.end(mp4Bytes);
   return;
  }
  res.writeHead(404);res.end();
 });
 await new Promise(r=>fixture.listen(listenPort,'127.0.0.1',r));
 return fixture.address().port;
}

try{
 await wait(`document.body?.innerText.includes('本地服务已连接')`);
 const pngBase64=await evaluate(`(()=>{const c=document.createElement('canvas');c.width=320;c.height=240;const x=c.getContext('2d');x.fillStyle='#24527a';x.fillRect(0,0,320,240);return c.toDataURL('image/png').split(',')[1]})()`);
 pngBytes=Buffer.from(pngBase64,'base64');

 if(phase==='initial'){
  const supplierPort=await startSupplier(0);
  const imgConfig={id:'int-img',name:'中断测试图像源',kind:'image',protocol:'openai-images',base_url:`http://127.0.0.1:${supplierPort}`,model_id:'fixture-img',enabled:true,headers:{},capability:{max_prompt_bytes:65536,max_count:2,max_references:0,sizes:['1024x1024'],edit:false,duration_seconds:[],audio:false},pricing:{currency:'USD',unit:'image',unit_amount:10,minor_unit_scale:2,credit_numerator:1,credit_denominator:1,source:'local-fixture',version:'v1',effective_at:0}};
  const vidConfig={id:'int-vid',name:'中断测试视频源',kind:'video',protocol:'ark-video',base_url:`http://127.0.0.1:${supplierPort}/v1`,model_id:'seedance-2.0',enabled:true,headers:{},capability:{max_prompt_bytes:16000,max_count:1,max_references:2,sizes:['1280x720'],edit:false,duration_seconds:[5,10,15],audio:true},pricing:{currency:'CNY',unit:'second',unit_amount:24,minor_unit_scale:2,credit_numerator:1,credit_denominator:10,source:'local-fixture',version:'v1',effective_at:0}};
  assert.equal((await post('/api/providers',{requestId:crypto.randomUUID(),action:'create',config:imgConfig})).status,200);
  assert.equal((await post('/api/providers',{requestId:crypto.randomUUID(),action:'create',config:vidConfig,key:'synthetic-ark-video-key'})).status,200);

  await fill('#project-name','中断恢复验收');
  await evaluate(`document.querySelector('.project-picker form').requestSubmit()`);
  await wait(`!!document.querySelector('.generation-composer')`);
  const p=await evaluate(`document.querySelector('#project').value`);
  const q=`?project_id=${encodeURIComponent(p)}`;

  // 1. Submit Task 1 via GenerationComposer and cancel before send -> verify actual_amount=0, compact resolved card, and first node position.
  await click('刷新连接');
  await wait(`!!document.querySelector('select[aria-label="图像连接"] option[value="int-img"]')`);
  await fill('select[aria-label="图像连接"]','int-img');
  await fill('input[aria-label="生成场景名称"]','发送前取消场景');
  await fill('textarea[aria-label="生成提示词"]','Before-send cancellation check');
  await click('请求费用确认');
  await wait(`document.querySelectorAll('[data-question-id]').length===1`);
  let tasksList=(await get('/api/tasks'+q)).items;
  assert.equal(tasksList.length,1);
  const task1=tasksList[0];
  await click('取消任务',`document.querySelector('[data-task-id="${task1.id}"]')`);
  await wait(`document.querySelector('[data-task-id="${task1.id}"]')?.dataset.chargeStatus==='not_submitted'`);
  await wait(`document.querySelector('[data-task-id="${task1.id}"]').innerText.includes('USD 0.00')`);
  const staleCard=(await get('/api/question'+q)).items[0];
  await wait(`document.querySelector('[data-question-id="${staleCard.id}"]')?.dataset.compact==='true'`);
  assert.equal(await evaluate(`document.querySelector('[data-question-id="${staleCard.id}"] details.resolved-card-details')?.open`),false,'resolved confirmation card details must be collapsed by default');
  assert.equal(imgPosts,0);

  // 2. Submit Task 2 via GenerationComposer -> verify non-overlapping Canvas node position + collection retry without re-POST.
  await fill('input[aria-label="生成场景名称"]','下载中断恢复场景');
  await fill('textarea[aria-label="生成提示词"]','Collection interruption retry check');
  truncateNextImg=true;
  await click('请求费用确认');
  await wait(`document.querySelectorAll('[data-question-id]').length===2`);
  const canvasAfter2=await get('/api/canvas/nodes'+q);
  const rootNodes=Object.values(canvasAfter2.nodes).filter(n=>n.parentId===null);
  assert.equal(rootNodes.length,2);
  assert(rootNodes[0].position.x!==rootNodes[1].position.x||rootNodes[0].position.y!==rootNodes[1].position.y,'successive GenerationComposer nodes must not stack on identical coordinates');

  const card2=(await get('/api/question'+q)).items.find(x=>x.state==='pending');
  await click('确认生成',`document.querySelector('[data-question-id="${card2.id}"]')`);
  await wait(`document.querySelector('[data-task-id="${card2.task_id}"]')?.innerText.includes('继续收集原结果')`);
  assert.equal(imgPosts,1);
  assert.equal(imgDownloads,1);
  await click('继续收集原结果',`document.querySelector('[data-task-id="${card2.task_id}"]')`);
  await wait(`document.querySelector('[data-task-id="${card2.task_id}"]')?.innerText.includes('已完成')`);
  assert.equal(imgPosts,1,'collection retry must never re-POST image generation');
  assert.equal(imgDownloads,2);

  // 3. Submit Task 3 (ark-video queued) -> cancel while queued -> calls DELETE once, sets TASK_CANCELLED_QUEUED & remote_cancelled_queued.
  const genQueued=await post('/api/generate/video'+q,{
   requestId:'vid-queued-task',
   session_id:'int-session',
   capability_id:'video:int-vid',
   prompt:'QUEUED_CANCEL 15s video prompt',
   count:1,
   params:{duration:15,aspect_ratio:'16:9',resolution:'720p',size:'1280x720',generate_audio:true,audio_mode:'narration+music'},
   references:[],
   output:{resources:[{key:'v1',mode:'create',name:'排队取消视频'}],composition:{name:'排队取消视频组',type:{label:'VideoGroup1'},placements:[{key:'v1',role:'primary'}]}}
  });
  assert.equal(genQueued.status,202,JSON.stringify(genQueued.data));
  const task3Id=genQueued.data.task_id;
  await wait(`!!document.querySelector('[data-task-id="${task3Id}"]')`);
  const card3=(await get('/api/question'+q)).items.find(x=>x.task_id===task3Id&&x.state==='pending');
  await click('确认生成',`document.querySelector('[data-question-id="${card3.id}"]')`);
  await wait(`document.querySelector('[data-task-id="${task3Id}"]')?.dataset.upstreamId==='cgt-int-queued'`);
  await click('取消任务',`document.querySelector('[data-task-id="${task3Id}"]')`);
  await wait(`document.querySelector('[data-task-id="${task3Id}"]')?.dataset.chargeStatus==='remote_cancelled_queued'`);
  assert.equal(videoDeletes,1,'queued ark-video cancellation must call DELETE once');
  await wait(`document.querySelector('[data-task-id="${task3Id}"]').innerText.includes('CNY 0.00')`);

  // 4. Submit Task 4 (ark-video running) -> cancel while running -> 0 additional DELETEs, RUNNING_UNCANCELLABLE, then late completion creates delivery card.
  const genRunning=await post('/api/generate/video'+q,{
   requestId:'vid-running-task',
   session_id:'int-session',
   capability_id:'video:int-vid',
   prompt:'RUNNING_CANCEL 15s video prompt',
   count:1,
   params:{duration:15,aspect_ratio:'16:9',resolution:'720p',size:'1280x720',generate_audio:true,audio_mode:'narration+music'},
   references:[],
   output:{resources:[{key:'v2',mode:'create',name:'执行中取消视频'}],composition:{name:'执行中取消视频组',type:{label:'VideoGroup2'},placements:[{key:'v2',role:'primary'}]}}
  });
  assert.equal(genRunning.status,202,JSON.stringify(genRunning.data));
  const task4Id=genRunning.data.task_id;
  await wait(`!!document.querySelector('[data-task-id="${task4Id}"]')`);
  const card4=(await get('/api/question'+q)).items.find(x=>x.task_id===task4Id&&x.state==='pending');
  await click('确认生成',`document.querySelector('[data-question-id="${card4.id}"]')`);
  await wait(`document.querySelector('[data-task-id="${task4Id}"]')?.dataset.upstreamId==='cgt-int-running'`);
  await click('取消任务',`document.querySelector('[data-task-id="${task4Id}"]')`);
  await wait(`document.querySelector('[data-task-id="${task4Id}"]')?.dataset.chargeStatus==='local_cancelled_remote_may_charge'`);
  assert.equal(videoDeletes,1,'running ark-video cancellation must not call DELETE');
  videoStates['cgt-int-running']='succeeded';
  await wait(`document.body.innerText.includes('采用取消后返回的结果')||document.body.innerText.includes('本地已取消但供应商返回了生成结果')`,10000);

  // 5. Submit Task 5 (ark-video running across physical desktop restart) -> approve so upstream_id='cgt-int-restart' is persisted before desktop closes.
  const genRestart=await post('/api/generate/video'+q,{
   requestId:'vid-restart-task',
   session_id:'int-session',
   capability_id:'video:int-vid',
   prompt:'RESTART_RESUME 15s video prompt',
   count:1,
   params:{duration:15,aspect_ratio:'16:9',resolution:'720p',size:'1280x720',generate_audio:true,audio_mode:'narration+music'},
   references:[],
   output:{resources:[{key:'v3',mode:'create',name:'重启续轮询视频'}],composition:{name:'重启续轮询视频组',type:{label:'VideoGroup3'},placements:[{key:'v3',role:'primary'}]}}
  });
  assert.equal(genRestart.status,202,JSON.stringify(genRestart.data));
  const task5Id=genRestart.data.task_id;
  await wait(`!!document.querySelector('[data-task-id="${task5Id}"]')`);
  const card5=(await get('/api/question'+q)).items.find(x=>x.task_id===task5Id&&x.state==='pending');
  await click('确认生成',`document.querySelector('[data-question-id="${card5.id}"]')`);
  await wait(`document.querySelector('[data-task-id="${task5Id}"]')?.dataset.upstreamId==='cgt-int-restart'`);
  assert.equal(videoPosts,3);

  await writeFile(join(dir,'interruptions-expected.json'),JSON.stringify({
   project:p,
   supplierPort,
   task1Id:task1.id,
   task2Id:card2.task_id,
   task3Id,
   task4Id,
   task5Id,
   videoStates
  },null,2));
 }else if(phase==='recovery'){
  await fill('#project',expected.project);
  await wait(`!!document.querySelector('[data-task-id="${expected.task5Id}"]')`);
  // Startup Recover() must have queried GET for cgt-int-restart without ever calling POST.
  assert.equal(videoPosts,0,'physical restart must never re-POST accepted video task');
  videoStates['cgt-int-restart']='succeeded';
  await wait(`document.querySelector('[data-task-id="${expected.task5Id}"]')?.innerText.includes('已完成')`,15000);
  assert.equal(videoPosts,0,'poll resumption after restart must complete with 0 POSTs');
  const health=await get('/api/health');
  assert.equal(health.status,'ok');
  assert.equal(health.safe_to_suspend,true,'gateway must report safe_to_suspend=true after all tasks settle');
  expected.videoStates=videoStates;
  await writeFile(join(dir,'interruptions-expected.json'),JSON.stringify(expected,null,2));
 }else{
  await fill('#project',expected.project);
  await wait(`document.querySelector('[data-task-id="${expected.task5Id}"]')?.innerText.includes('已完成')`);
  assert.equal(videoPosts,0);
  assert.equal(imgPosts,0);
  assert.equal(requests.filter(r=>r.method==='POST'&&(r.url.includes('/api/tasks')||r.url.includes('/api/generate')||r.url.includes('/api/question'))).length,0,'second restart performs zero control writes');
  const health=await get('/api/health');
  assert.equal(health.status,'ok');
  assert.equal(health.idle,true);
  assert.equal(health.safe_to_suspend,true);
 }

 assert.deepEqual(exceptions,[]);
 await writeFile(join(dir,`interruptions-${phase}.png`),Buffer.from((await call('Page.captureScreenshot',{format:'png'})).data,'base64'));
 await writeFile(join(dir,`interruptions-${phase}.json`),JSON.stringify({
  phase,
  origin,
  imgPosts,
  imgDownloads,
  videoPosts,
  videoGets,
  videoDeletes,
  exceptions
 },null,2));
 console.log(`M7.3 native interruptions ${phase} passed: ${dir}`);
}catch(e){
 await writeFile(join(dir,`interruptions-${phase}-failure.txt`),String(e)+'\n'+await evaluate('document.body?.innerText??""').catch(()=>''));
 throw e;
}finally{
 close();
 if(fixture)await new Promise(r=>fixture.close(r));
}
