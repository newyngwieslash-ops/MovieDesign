// Offline checks only: compile and exercise each smoke's real send helper without a desktop or socket.
import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import vm from 'node:vm';
for(const name of ['chat','skills','storyboard','pipeline','tools','video']){
 const source=await readFile(new URL(`./smoke-${name}.mjs`,import.meta.url),'utf8'),start=source.indexOf('async function sendCurrent('),end=source.indexOf('\n}',start)+2,fn=source.slice(start,end);
 for(const scenario of ['approved','unapproved','foreign-supplier','foreign-card','early-inference']){
  const requests=[],models=[],message={id:'fresh-message',session_id:'session',role:'user',requestId:'fresh-request',state:'queued',billing_question_id:'fresh-card'},question={id:'fresh-card',project_id:'project',session_id:'session',chat_message_id:scenario==='foreign-card'?'old-message':message.id,kind:'billing',state:'pending',model:`${name}-fixture`,revision:3,questions:[{options:[{label:'确认本轮聊天费用'}]}]},card={dataset:{state:'pending'}};
  let clicks=0,pages=0;
  const button={textContent:'确认本轮聊天费用',disabled:false,click(){clicks++;card.dataset.state='approved';requests.push({method:'POST',url:'http://127.0.0.1:8001/api/question/fresh-card/reply?session_id=session',postData:JSON.stringify({requestId:'reply-id',answers:[['确认本轮聊天费用']],expected_revision:3})});models.push({})}};
  card.querySelectorAll=()=>[button];
  const browser={document:{querySelector:()=>card,querySelectorAll:()=>[]},fetch:async url=>({ok:true,json:async()=>{
   if(url.includes('/agent/status'))return {active:{base_url:scenario==='foreign-supplier'?'https://supplier.invalid':`http://127.0.0.1:12345/v1`,model_id:`${name}-fixture`}};
   if(url.includes('/api/question?'))return {items:[{id:'old-card',chat_message_id:'old-message'},question]};
   if(url.includes('/messages')){pages++;return url.includes('offset=0&')?{items:[{...message,id:'old-message',requestId:'old-request'}],coverage:{exhausted:false,next_offset:100}}:{items:[message],coverage:{exhausted:true,next_offset:101}};}
   throw Error(`Unexpected read ${url}`);
  }}),JSON};
  const evaluate=async expression=>{const value=await vm.runInNewContext(expression,browser);return value===undefined?undefined:JSON.parse(JSON.stringify(value));},wait=async expression=>assert(await evaluate(expression),expression);
  const click=async label=>{assert.equal(label,'发送消息');requests.push({method:'POST',url:'http://127.0.0.1:8001/api/chat/sessions/session/messages?project_id=project',postData:'{"requestId":"fresh-request"}'});if(scenario==='early-inference')models.push({});};
  const context={assert,requests,models,modelRequests:models,expected:{p:'project',s:'session'},fixture:{listening:true,address:()=>({port:12345})},evaluate,wait,click,URL,JSON};
  const run=Function(...Object.keys(context),`return (${fn})`)(...Object.values(context));
  if(['foreign-supplier','foreign-card','early-inference'].includes(scenario)){await assert.rejects(run());assert.equal(clicks,0);}
  else{assert.equal((await run(scenario==='approved')).id,message.id);assert.equal(clicks,scenario==='approved'?1:0);assert.equal(pages,2);}
 }
 console.log(`PASS offline sendCurrent ${name}: bound UI callback, unapproved, pagination, foreign supplier/card, early supplier POST`);
}

// Execute the actual hold/queue/cancel sequence statements with a delayed approved POST.
const chatSource=await readFile(process.argv[2]??new URL('./smoke-chat.mjs',import.meta.url),'utf8');
const section=(start,end)=>{const from=chatSource.indexOf(start),to=chatSource.indexOf(end,from);assert(from>=0&&to>from,'real smoke sequence must exist');return chatSource.slice(from,to);};
const sequences={
 queueFix:section("await send('hold first queue-fix')","   const beforeDraft="),
 referenced:section("await sendCurrent();await wait(`document.querySelector('.chat-history').innerText.includes('已受理')`);",";await click('撤回并编辑')")+';',
 cancellation:section('const beforeCancel=','   // Fresh session:'),
};
for(const [name,code] of Object.entries(sequences)){
 if(process.argv[3]&&name!==process.argv[3])continue;
 const requests=[],modelRequests=[],cards=new Map(),messages=[],asset={id:'asset'},version={id:'version'};let pending,held=false,started=0,cancelled=false;
 const original={references:[asset.id],annotations:[{assetId:asset.id,sourceVersionID:version.id,comment:'保持这个区域'}]};
 const browser={document:{querySelector:selector=>cards.get(selector.match(/data-question-id="([^"]+)"/)?.[1]),querySelectorAll:()=>[]},fetch:async url=>({ok:true,json:async()=>{
  if(url.includes('/agent/status'))return {active:{base_url:'http://127.0.0.1:12345/v1',model_id:'chat-fixture'}};
  if(url.includes('/api/question?'))return {items:messages.map(m=>({id:m.billing_question_id,project_id:'project',session_id:'session',chat_message_id:m.id,kind:'billing',state:cards.get(m.billing_question_id).dataset.state,model:'chat-fixture',revision:1,questions:[{options:[{label:'确认本轮聊天费用'}]}]}))};
  if(url.includes('/messages'))return {items:messages,coverage:{exhausted:true,next_offset:messages.length}};
  throw Error(`Unexpected read ${url}`);
 }})};
 const evaluate=async expression=>{const value=await vm.runInNewContext(expression,browser);return value===undefined?undefined:JSON.parse(JSON.stringify(value));};
 const wait=async expression=>{if(expression.includes('.chat-history'))return;assert(await evaluate(expression),expression);};
 const click=async label=>{
  if(label==='取消本次对话及队列'){assert.equal(started,1,'cancel must follow the approved hold supplier POST');assert(held);cancelled=true;return;}
  assert.equal(label,'发送消息');if(pending){pending();pending=null;}
  const id=String(messages.length+1),message={id,session_id:'session',role:'user',requestId:'send-'+id,state:'queued',billing_question_id:'card-'+id};messages.push(message);
  requests.push({method:'POST',url:'http://127.0.0.1:8001/api/chat/sessions/session/messages?project_id=project',postData:JSON.stringify({...original,requestId:message.requestId})});
  const card={dataset:{state:'pending'}};card.querySelectorAll=()=>[{textContent:'确认本轮聊天费用',disabled:false,click(){card.dataset.state='approved';requests.push({method:'POST',url:`http://127.0.0.1:8001/api/question/${message.billing_question_id}/reply?session_id=session`,postData:JSON.stringify({requestId:'approve-'+id,answers:[['确认本轮聊天费用']],expected_revision:1})});if(id==='1')pending=()=>{modelRequests.push({messages:[{role:'user',content:'previous approved hold'}]});started++;held=true;};}}];cards.set(message.billing_question_id,card);
 };
 const context={assert,requests,modelRequests,asset,version,expected:{p:'project',s:'session'},fixture:{listening:true,address:()=>({port:12345})},evaluate,wait,click,URL,JSON,writes:()=>requests,rejectedMessage:{id:'prior-completed'},waitDone:async()=>{},releaseHold:()=>{assert(held);held=false;},setTimeout:resolve=>{assert(pending,'bounded fixture wait must advance the actual delayed request');pending();pending=null;resolve();}};
 const helper=section('async function sendCurrent(','\ntry{');
 const fixtureWait=chatSource.match(/async function waitFixture\(count\)\{[^\n]+/)[0];
 await Function(...Object.keys(context),`return (async()=>{${fixtureWait}\n${helper}\nconst send=(text,approve=true)=>sendCurrent(approve);\n${code}})()`)(...Object.values(context));
 assert.equal(started,1);assert.equal(modelRequests.length,1,'only the approved first hold may reach the synthetic supplier');
 if(name==='cancellation')assert(cancelled);else{assert(held,'queue evidence requires first request to stay held');assert.equal(messages.length,2);if(name==='referenced')assert.equal(cards.get(messages[1].billing_question_id).dataset.state,'pending');}
 console.log(`PASS real smoke-chat sequence ${name}: delayed approved POST synchronized; queued current send has zero supplier POST`);
}

// Native user rejection is a cancelled original message, never a successful done turn.
const rejectionStart=chatSource.includes('const beforeReject=')?'const beforeReject=':"await click('拒绝',";
const rejection=section(rejectionStart,'const beforeCancel=');
for(const scenario of ['cancelled','wrong-message','not-sent','extra-post']){
 let clicked=0,reads=0;const modelRequests=[];
 const rejectedMessage={id:'rejected-original'},pendingCard='native-rejection';
 const browser={document:{querySelector:()=>({innerText:'已拒绝'})},fetch:async url=>({ok:true,json:async()=>{
  reads++;
  if(url.includes('/api/question'))return {items:[{id:'other-card',state:'rejected',native_send_state:'sent'},{id:pendingCard,state:'rejected',native_send_state:scenario==='not-sent'?'unknown':'sent'}]};
  if(scenario==='extra-post'&&!modelRequests.length)modelRequests.push({});
  return {items:[{id:scenario==='wrong-message'?'other-message':rejectedMessage.id,session_id:'session',state:'cancelled'}],coverage:{exhausted:true,next_offset:1}};
 }})};
 const evaluate=async expression=>vm.runInNewContext(expression,browser),wait=async expression=>assert(await evaluate(expression),'exact rejected original must become cancelled with delivered receipt');
 const context={assert,modelRequests,rejectedMessage,pendingCard,p:'project',s:'session',expected:{p:'project',s:'session'},evaluate,wait,click:async label=>{assert.equal(label,'拒绝');clicked++;},JSON};
 const done=chatSource.match(/const waitDone=id=>[^\r\n]+/)[0];
 const run=()=>Function(...Object.keys(context),`return (async()=>{${done}\n${rejection}})()`)(...Object.values(context));
 if(scenario==='cancelled'){await run();assert.equal(clicked,1);assert(reads>0);assert.equal(modelRequests.length,0);}
 else await assert.rejects(run());
 console.log(`PASS real rejection sequence ${scenario}: exact cancelled message, sent rejection, no continuation POST`);
}
