import test from 'node:test';
import assert from 'node:assert/strict';
import {readIntent,clearIntent} from './pending.ts';
test('damaged or foreign confirmation storage cannot become a fresh approval',()=>{
 const q={id:'question',session_id:'s',task_id:'task'};
 for(const raw of ['','{','null','{}',JSON.stringify({path:'https://evil.test',body:'{}'}),JSON.stringify({path:'/api/question/other/reply?session_id=s',body:JSON.stringify({requestId:'a',expected_revision:1,answers:[['A']]})})])assert.throws(()=>readIntent({getItem:()=>raw},'key',q));
 assert.throws(()=>readIntent({getItem:()=>{throw Error('unavailable')}},'key',q));
 const intent={path:'/api/question/question/reply?session_id=s',body:JSON.stringify({requestId:'a',expected_revision:1,answers:[['A']]})};assert.deepEqual(readIntent({getItem:()=>JSON.stringify(intent)},'key',q),intent);
 let removed=false;clearIntent({getItem:()=>JSON.stringify({...intent,body:'newer'}),removeItem:()=>{removed=true}},'key',intent);assert.equal(removed,false);
 clearIntent({getItem:()=>JSON.stringify(intent),removeItem:()=>{removed=true}},'key',intent);assert.equal(removed,true);
});
