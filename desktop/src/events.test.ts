import test from 'node:test';
import assert from 'node:assert/strict';
import { readEvents } from './events.ts';
test('SSE chunk boundaries and duplicate IDs do not lose or repeat a decision', async () => {
 const a = {id:1,project_id:'p',session_id:'a',type:'question_asked',payload:{text:'中文'}};
 const b = {id:2,project_id:'p',session_id:'b',type:'question_resolved',payload:{}};
 const wire = new TextEncoder().encode(`: connected\r\n\r\nid: 1\r\nevent: question_asked\r\ndata: ${JSON.stringify(a)}\r\n\r\nid: 2\ndata: ${JSON.stringify(b)}\n\n`);
 const response = new Response(new ReadableStream({start(c){for(const byte of wire)c.enqueue(new Uint8Array([byte]));c.close()}}));
 const got: unknown[]=[];await readEvents(response,e=>got.push(e));assert.deepEqual(got,[a,b]);
});
import { createEvents } from './events.ts';
import { setTimeout as delay } from 'node:timers/promises';
test('one stream advances filtered-session cursor, deduplicates and reconnects with Last-Event-ID', async () => {
 const saved = new Map<string,string>(), requests: RequestInit[] = []; let stream: ReadableStreamDefaultController<Uint8Array>;
 const hub = createEvents({getItem:k=>saved.get(k)??null,setItem:(k,v)=>{saved.set(k,v)}}, async (_url,init) => {requests.push(init!);return new Response(new ReadableStream({start(c){stream=c;init!.signal!.addEventListener('abort',()=>c.close())}}))});
 const got: number[]=[];let connected=0;
 const stop=hub.subscribe('p',e=>got.push(e.id),()=>connected++,'a');const stop2=hub.subscribe('p',()=>{},()=>{});
 await delay(0);assert.equal(requests.length,1);
 const emit=(id:number,session:string,project='p')=>stream.enqueue(new TextEncoder().encode(`id: ${id}\ndata: ${JSON.stringify({id,project_id:project,session_id:session,type:'question_asked',payload:{}})}\n\n`));
 emit(1,'a');emit(1,'a');emit(2,'b');emit(9,'a','wrong');await delay(0);
 assert.deepEqual(got,[1]);assert.equal(saved.get('moviedesign.events.v1.p'),'2');stream!.close();await delay(550);
 assert.equal(requests.length,2);assert.deepEqual(requests[1].headers,{'Last-Event-ID':'2'});assert.equal(connected,2);stop2();stop();
 const stopNew=hub.subscribe('other',()=>{},()=>{});await delay(0);assert.deepEqual(requests[2].headers,{});stopNew();
});
test('failed cursor read leaves no leaked subscription when changing project', async () => {
 let fail=true; const storage={getItem:()=>{if(fail)throw Error('unavailable');return null},setItem:()=>{}};
 const hub=createEvents(storage,async(_url,init)=>new Response(new ReadableStream({start(c){init!.signal!.addEventListener('abort',()=>c.close())}})));
 assert.throws(()=>hub.subscribe('bad',()=>{},()=>{}));fail=false;const stop=hub.subscribe('good',()=>{},()=>{});await delay(0);stop();
});
test('retired project 400 cannot reset the active project cursor', async()=>{
 const saved=new Map([['moviedesign.events.v1.old','4'],['moviedesign.events.v1.new','8']]);let release:(r:Response)=>void;
 const hub=createEvents({getItem:k=>saved.get(k)??null,setItem:(k,v)=>{saved.set(k,v)}},async(url,init)=>String(url).endsWith('old')?new Promise<Response>(r=>{release=r}):new Response(new ReadableStream({start(c){init!.signal!.addEventListener('abort',()=>c.close())}})));
 const old=hub.subscribe('old',()=>{},()=>{});old();const current=hub.subscribe('new',()=>{},()=>{});release!(new Response('',{status:400}));await delay(0);assert.equal(saved.get('moviedesign.events.v1.new'),'8');current();
});
test('bad event closes the reader before reconnect can open another stream',async()=>{
 let cancelled=false;
 const response=new Response(new ReadableStream({start(c){c.enqueue(new TextEncoder().encode('id: 1\ndata: invalid\n\n'))},cancel(){cancelled=true}}));
 await assert.rejects(readEvents(response,()=>{}));assert.equal(cancelled,true);
});
