import {test} from 'node:test';
import assert from 'node:assert/strict';
import {sendIntent,completedPatch,canDecide,readControlIntent,clearControlIntent,type Task} from './tasks.ts';
import {emptyDocument,type Node} from '../canvas/canvas.ts';
test('storage failure and different unresolved intent prevent control POST; replay preserves original body',async()=>{
 let calls=0;let raw:string|null=null;const intent={path:'/api/generate/image',body:'{"requestId":"original","prompt":"keep  whitespace"}'};
 const send=async(value:unknown)=>{calls++;assert.deepEqual(value,intent)};
 await assert.rejects(sendIntent({getItem:()=>null,setItem:()=>{throw Error('quota')}},'key',intent,send));assert.equal(calls,0);
 const storage={getItem:()=>raw,setItem:(_k:string,v:string)=>{raw=v}};
 await sendIntent(storage,'key',intent,send);await sendIntent(storage,'key',intent,send);assert.equal(calls,2);
 await assert.rejects(sendIntent(storage,'key',{...intent,body:'changed'},send));assert.equal(calls,2);
});
test('completed output replaces only matching intent and preserves moved position; never changes selected version',()=>{
 const doc=emptyDocument();doc.nodes.n={id:'n',kind:'placeholder',expectedKind:'image',draft:null,parentId:null,position:{x:333,y:222},size:{width:400,height:500},zIndex:3,hidden:false,pendingGeneration:{requestId:'r',outputKey:'main'}} as Node;
 const task={id:'t',request_id:'r',status:'completed',outputs:[{output_key:'main',state:'delivered',resource_id:'resource',composition_id:'composition',version_id:'version'}]} as Task;
 const command=completedPatch(doc,[task]);assert.equal(command?.type,'canvas.patch');if(command?.type!=='canvas.patch')return;
 const next=command.nodes[0].after!;assert.deepEqual(next.position,{x:333,y:222});assert.equal(next.kind,'composition');assert.equal('expectedKind' in next,false);doc.nodes.n=next;assert.equal(completedPatch(doc,[task]),null);
});
test('empty/cross-owner control journals cannot become fresh intents and cleanup cannot erase a newer request',()=>{
 assert.equal(readControlIntent(null,'resolve','t'),null);for(const raw of ['', '{}','null'])assert.throws(()=>readControlIntent(raw,'resolve','t'));
 for(const action of ['retry','cancel','query','modify_params','modify_storyboard']){
  const intent={path:'/api/tasks/t/resolve',body:JSON.stringify({requestId:'one',action,expected_revision:4})};assert.deepEqual(readControlIntent(JSON.stringify(intent),'resolve','t'),intent);assert.throws(()=>readControlIntent(JSON.stringify(intent),'resolve','other'));
 }
 assert.throws(()=>readControlIntent(JSON.stringify({path:'/api/tasks/t/resolve',body:JSON.stringify({requestId:'one',action:'unknown_action',expected_revision:4})}),'resolve','t'));
 const intent={path:'/api/tasks/t/resolve',body:JSON.stringify({requestId:'one',action:'retry',expected_revision:4})};
 let value=JSON.stringify({...intent,body:'newer'}),removed=false;assert.equal(clearControlIntent({getItem:()=>value,removeItem:()=>{removed=true}},'key',intent),false);assert.equal(removed,false);
});
test('expired billing still permits explicit rejection, while busy/storage/original intent guards apply',()=>{
 assert.equal(canDecide('pending',true,false,false,false,true),true);
 assert.equal(canDecide('pending',true,false,false,false,false),false);
 for(const guard of [[true,false,false],[false,true,false],[false,false,true]])assert.equal(canDecide('pending',true,...guard as [boolean,boolean,boolean],true),false);
 assert.equal(canDecide('approved',false,false,false,false,true),false);
});
