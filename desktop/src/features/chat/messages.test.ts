import test from 'node:test';
import assert from 'node:assert/strict';
import {assetReferences, messageBody, readPending, updatePending, historyState, reduceEvent} from './messages.ts';
const document = {type:'doc',content:[{type:'blockquote',content:[{type:'bulletList',content:[{type:'listItem',content:[{type:'paragraph',content:[{type:'mention',attrs:{path:'asset:a'}},{type:'mention',attrs:{path:'asset:b'}},{type:'mention',attrs:{path:'asset:a'}},{type:'mention',attrs:{path:'file:c'}},{type:'mention',attrs:{path:'asset:'}}]}]}]}]}]};
test('references traverse nested blocks and retain unique mention order',()=>assert.deepEqual(assetReferences(document),['a','b']));
test('wire input owns original document and annotation bytes after draft edits',()=>{
 const doc=structuredClone(document),annotations=[{assetId:'a',sourceVersionID:'version-1',bounds:{x:4},comment:'original'}];
 const wire=messageBody('original-id',doc,'original',annotations);doc.content=[];annotations[0].comment='changed';
 assert.equal(JSON.parse(wire).requestId,'original-id');assert.deepEqual(JSON.parse(wire).references,['a','b']);assert.equal(JSON.parse(wire).annotations[0].comment,'original');assert.equal(JSON.parse(wire).annotations[0].sourceVersionID,'version-1');
});
const message={id:'m',session_id:'s',requestId:'r',role:'user',content:{text:'hello'},state:'accepted',created_at:1};
const event=(id:number,payload:object,type='chat_message',session='s')=>({id,project_id:'p',session_id:session,type,payload:{session_id:session,message_id:'m',step:1,part_id:'part',part_type:'text',type:'content_delta',...payload}});
test('delta replay, snapshot replacement, session separation and terminal late frames',()=>{
 let s=historyState('p','s',[message]);s=reduceEvent(s,event(10,{content_delta:'哈哈'}));s=reduceEvent(s,event(10,{content_delta:'哈哈'}));
 assert.equal(s.parts.m[0].content,'哈哈');s=reduceEvent(s,event(11,{content_delta:'x'},'chat_message','other'));assert.equal(s.parts.m[0].content,'哈哈');
 s=reduceEvent(s,event(12,{type:'content_replace',content:'<img src=x onerror=alert(1)>'}));assert.equal(s.parts.m[0].content,'<img src=x onerror=alert(1)>');
 s=reduceEvent(s,event(13,{done:true,status:'done'},'chat_done'));s=reduceEvent(s,event(14,{content_delta:'late'}));assert.equal(s.parts.m[0].content,'<img src=x onerror=alert(1)>');assert.equal(s.messages[0].state,'done');
});
test('history snapshots replace live parts and distinguish steps/tools',()=>{
 let s=historyState('p','s',[message,{...message,id:'assistant',role:'assistant',content:{reply_to:'m',parts:[{session_id:'s',message_id:'m',step:1,part_id:'part',part_type:'text',type:'content_delta',content:'full'}]}}]);
 assert.equal(s.parts.m[0].content,'full');s=reduceEvent(s,event(1,{step:2,part_id:'part',content_delta:'next'}));s=reduceEvent(s,event(2,{part_id:'tool',call_id:'call',part_type:'tool',type:'tool_call',tool:'query',state:'running'}));s=reduceEvent(s,event(3,{part_id:'tool',call_id:'call',part_type:'tool',type:'tool_result',tool:'query',state:'completed',tool_output:'safe'}));
 assert.deepEqual(s.parts.m.map((p:any)=>p.content),['full','next',undefined]);assert.equal(s.parts.m[2].state,'completed');
});
test('pending records reject corrupted, wrong owner, malformed wire and changed references',()=>{
 const body=messageBody('r',document,'hello',[]);const intent={schema:1,projectID:'p',kind:'send',sessionID:'s',body};
 assert.equal(readPending(JSON.stringify(intent),'p','send')?.body,body);
 for(const raw of ['', '{}',JSON.stringify({...intent,projectID:'other'}),JSON.stringify({...intent,body:JSON.stringify({requestId:'r',text:'changed'})}),JSON.stringify({...intent,body:JSON.stringify({...JSON.parse(body),references:['wrong']})})])assert.throws(()=>readPending(raw,'p','send'));
});
test('pending compare before store/clear prevents stale requests deleting newer recovery',()=>{
 const map=new Map<string,string>(),storage={getItem:(k:string)=>map.get(k)??null,setItem:(k:string,v:string)=>map.set(k,v),removeItem:(k:string)=>map.delete(k)};
 const old={id:'old'},newer={id:'new'};updatePending(storage,'key',null,old);assert.equal(map.get('key'),'{"id":"old"}');
 map.set('key',JSON.stringify(newer));assert.throws(()=>updatePending(storage,'key',old,null));assert.equal(map.get('key'),'{"id":"new"}');
});
