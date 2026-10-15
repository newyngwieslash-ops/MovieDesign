import test from 'node:test';
import assert from 'node:assert/strict';
import { preparePending, replayPending, readPending } from './recovery.ts';
test('key rotation survives restart with original identity and no persisted secret',async()=>{
 const body=JSON.stringify({requestId:'original-id',action:'update',config:{id:'provider',name:'original'},key:'synthetic-original-secret'});
 const saved=JSON.stringify(await preparePending(body));assert(!saved.includes('synthetic-original-secret'));
 const restored=readPending(saved)!;assert.deepEqual(JSON.parse(await replayPending(restored,'synthetic-original-secret')),JSON.parse(body));
 await assert.rejects(()=>replayPending(restored,'different'),/相同密钥/);
 assert.throws(()=>readPending('{"body":"{}","keyHash":null}'));
});
