import assert from 'node:assert/strict';
import { writeFile, readFile } from 'node:fs/promises';
import { join } from 'node:path';
import { connect } from './webview-cdp.mjs';
const [port, evidenceDir, origin, phase] = process.argv.slice(2);
const { call, evaluate, wait, requests, exceptions, close } = await connect(port, origin);
const click = async name => { await evaluate(`(()=>{const b=[...document.querySelectorAll('button')].find(b=>b.textContent.trim()===${JSON.stringify(name)});if(!b||b.disabled)throw Error('unavailable '+${JSON.stringify(name)});b.click()})()`); };
const api = (project, path) => evaluate(`fetch('http://127.0.0.1:8001${path}?project_id=${project}').then(r=>r.json())`);
const canvas = 'document.querySelector(".editor-stage canvas")';
const frame = () => evaluate('new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r)))');
const png = () => evaluate(`${canvas}.toDataURL()`);
async function point(x, y, type, clicks = 1) {
  const bounds = await evaluate(`(()=>{const r=${canvas}.getBoundingClientRect();return {x:r.x,y:r.y}})()`);
  await call('Input.dispatchMouseEvent', { type, x: bounds.x + x, y: bounds.y + y, button: 'left', buttons: type === 'mouseMoved' ? 1 : undefined, clickCount: clicks });
}
async function draw(name, x, y, ex, ey) {
  await click(name); await point(x, y, 'mousePressed'); await point(ex, ey, 'mouseMoved'); await point(ex, ey, 'mouseReleased'); await frame();
}
const key = (key, modifiers = 0) => call('Input.dispatchKeyEvent', { type: 'keyDown', key, code: key, modifiers });
try {
  await wait(`document.body.innerText.includes('本地服务已连接')`);
  if (phase === 'product') {
    assert(!await evaluate(`document.querySelector('.milestone').textContent.includes('M2')`));
    await evaluate(`(()=>{const e=document.querySelector('#project-name');Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(e,'编辑验收');e.dispatchEvent(new Event('input',{bubbles:true}));})()`);
    await evaluate(`document.querySelector('.project-picker form').requestSubmit()`); await wait(`!!document.querySelector('.asset-library')`);
    const project = await evaluate(`document.querySelector('#project').value`);
    const source = await evaluate(`(()=>{const c=document.createElement('canvas');c.width=600;c.height=400;const x=c.getContext('2d');x.fillStyle='#eee';x.fillRect(0,0,600,400);for(let y=0;y<400;y+=10)for(let x1=0;x1<600;x1+=10){x.fillStyle=(x1+y)%20?'#ddd':'#fff';x.fillRect(x1,y,10,10)}return c.toDataURL()})()`);
    const path = join(evidenceDir, 'editor-source.png'); await writeFile(path, Buffer.from(source.split(',')[1], 'base64'));
    const { root } = await call('DOM.getDocument'); const { nodeId } = await call('DOM.querySelector', { nodeId: root.nodeId, selector: 'input[aria-label="导入素材"]' });
    await call('DOM.setFileInputFiles', { nodeId, files: [path] }); await wait(`document.body.innerText.includes('导入完成')`);
    const first = (await api(project, '/api/versions')).items[0];
    await click('编辑图像'); await wait(`${canvas} && ![...document.querySelectorAll('button')].find(b=>b.textContent==='矩形').disabled`);
    await evaluate(`document.querySelector('.image-editor').scrollIntoView()`); await frame();
    const tools = [['矩形',20,30,90,80],['椭圆',125,30,190,80],['直线',225,30,290,75],['箭头',325,30,390,75],['画笔',425,30,480,75],['马赛克',20,130,90,180]];
    for (const args of tools) { const before = await png(); await draw(...args); assert.notEqual(await png(), before, `${args[0]} changed real pixels`); await click('撤销'); await frame(); assert.equal(await png(), before, `${args[0]} single undo`); await click('重做'); await frame(); }
    await draw('文字',130,130,130,130); await wait(`!!document.querySelector('.canvas-text-input')`);
    const ta = await evaluate(`(()=>{const s=getComputedStyle(document.querySelector('.canvas-text-input'));return {color:s.color,caret:s.caretColor,line:s.lineHeight}})()`); assert.equal(ta.color,'rgba(0, 0, 0, 0)'); assert.notEqual(ta.caret,'rgba(0, 0, 0, 0)');
    await call('Input.imeSetComposition', { text: '中文', selectionStart: 2, selectionEnd: 2 });
    await evaluate(`document.querySelector('.canvas-text-input').dispatchEvent(new KeyboardEvent('keydown',{key:'Enter',isComposing:true,bubbles:true}))`);
    assert(await evaluate(`!!document.querySelector('.canvas-text-input')`), 'composing Enter did not commit');
    await call('Input.insertText', { text: '中文编辑' }); await frame();
    await writeFile(join(evidenceDir, 'editor-chinese.png'), Buffer.from((await call('Page.captureScreenshot', { format:'png' })).data,'base64'));
    await key('Enter'); await wait(`!document.querySelector('.canvas-text-input')`); await frame();
    const withText = await png(); await draw('文字',130,240,130,240); await wait(`!!document.querySelector('.canvas-text-input')`); await call('Input.insertText',{text:'丢弃'}); await key('Escape'); await frame(); assert.equal(await png(),withText,'new text Escape has no pixels/history');
    await point(140,140,'mousePressed',2); await point(140,140,'mouseReleased',2); await wait(`!!document.querySelector('.canvas-text-input')`); await call('Input.insertText',{text:'取消更改'}); await key('Escape'); await frame();
    // Selection handles may differ; verify the reopened actual DOM text rather than overlay pixels.
    await point(140,140,'mousePressed',2); await point(140,140,'mouseReleased',2); await wait(`!!document.querySelector('.canvas-text-input')`); assert.equal(await evaluate(`document.querySelector('.canvas-text-input').value`),'中文编辑'); await key('Enter');
    await draw('标签',280,150,290,150); await wait(`!!document.querySelector('.canvas-text-input')`); await call('Input.insertText',{text:'标签'}); await key('Enter'); await frame();
    await point(295,150,'mousePressed',2); await point(295,150,'mouseReleased',2); await wait(`document.querySelector('.canvas-text-input')?.getAttribute('aria-label')==='标签文字'`); await key('Escape'); await frame();
    await click('撤销'); await frame(); await point(295,150,'mousePressed',2); await point(295,150,'mouseReleased',2); await wait(`!!document.querySelector('.canvas-text-input')`); assert.equal(await evaluate(`document.querySelector('.canvas-text-input').value`),'标签','tag Escape commits deletion and one undo restores'); await key('Enter');
    await draw('标签',290,320,300,320); await wait(`!!document.querySelector('.canvas-text-input')`); await call('Input.insertText',{text:'取消标签'}); await key('Escape'); await frame();
    await draw('区域标注',380,230,480,300); await wait(`!!document.querySelector('textarea[aria-label="区域说明"]')`); await call('Input.insertText',{text:'修改背景'}); await key('Enter'); await wait(`document.querySelectorAll('[aria-label="区域标注列表"] li').length===1`);
    await click('选择'); await point(380,245,'mousePressed'); await point(380,245,'mouseReleased');
    await evaluate(`document.querySelector('#project-name').focus()`); await key('Delete'); assert.equal(await evaluate(`document.querySelectorAll('[aria-label="区域标注列表"] li').length`),1,'focused input skips editor shortcuts'); await evaluate(`${canvas}.focus()`);
    await evaluate(`${canvas}.dispatchEvent(new KeyboardEvent('keydown',{key:'Delete',isComposing:true,bubbles:true}))`); assert.equal(await evaluate(`document.querySelectorAll('[aria-label="区域标注列表"] li').length`),1);
    await key('Delete'); await wait(`!document.querySelector('[aria-label="区域标注列表"]')`); await click('撤销'); await wait(`document.querySelectorAll('[aria-label="区域标注列表"] li').length===1`);
    // Inject a native storage refusal before the first export POST. The original bytes/IDs must survive in memory for explicit continuation.
    await evaluate(`window.realOpen=indexedDB.open.bind(indexedDB); indexedDB.open=()=>{throw Error('controlled quota refusal')}`);
    const beforePosts = requests.filter(r=>r.method==='POST'&&r.url.startsWith('http://127.0.0.1:8001/')).length; await click('保存新版本'); await wait(`document.body.innerText.includes('controlled quota refusal')`);
    assert.equal(requests.filter(r=>r.method==='POST'&&r.url.startsWith('http://127.0.0.1:8001/')).length,beforePosts,'storage refusal sent zero POST');
    await evaluate(`indexedDB.open=window.realOpen;window.realSet=Storage.prototype.setItem;Storage.prototype.setItem=function(k,v){if(k.startsWith('moviedesign.import.'))throw Error('controlled intent refusal');return window.realSet.call(this,k,v)}`);
    await click('重新查询/续投递'); await wait(`document.body.innerText.includes('controlled intent refusal')`); assert.equal(requests.filter(r=>r.method==='POST'&&r.url.startsWith('http://127.0.0.1:8001/')).length,beforePosts,'intent refusal sent zero POST');
    await evaluate(`Storage.prototype.setItem=window.realSet;window.realFetch=window.fetch;window.exportAttempts=[];window.fetch=async(...args)=>{if(String(args[0]).includes('/api/upload?')){window.exportAttempts.push(args[1].body.get('requestId'));throw Error('controlled upload response unknown')}return window.realFetch(...args)}`);
    await click('重新查询/续投递'); await wait(`document.body.innerText.includes('controlled upload response unknown')`);
    const op = await evaluate(`JSON.parse(localStorage.getItem('moviedesign.import.v1.${project}'))`); assert(op.generatedPNG); assert.equal(op.target.baseVersionID,first.id);
    const bytes = await evaluate(`(async()=>{const m=await import('/src/features/assets/exportBytes.ts');const op=JSON.parse(localStorage.getItem('moviedesign.import.v1.${project}'));const file=await m.recoverExport(op);return {sha:await m.fileDigest(file),size:file.size}})()`); assert.equal(bytes.sha,op.file.sha256); assert.equal(bytes.size,op.file.size);
    const storageChecks=await evaluate(`(async()=>{const m=await import('/src/features/assets/exportBytes.ts'),op=${JSON.stringify(op)},file=await m.recoverExport(op);let mismatched=false;try{await m.recoverExport({...op,file:{...op.file,sha256:'wrong'}})}catch{mismatched=true}const other={...op,uploadID:crypto.randomUUID()};await m.persistExport(other,file);await m.removeExport(other);const db=await new Promise((res,rej)=>{const r=indexedDB.open('moviedesign-editor-exports',1);r.onsuccess=()=>res(r.result);r.onerror=()=>rej(r.error)});await new Promise((res,rej)=>{const t=db.transaction('png','readwrite');t.objectStore('png').put({blob:file,file:null},other.projectID+'/'+other.uploadID);t.oncomplete=res;t.onerror=rej});db.close();let corrupt=false;try{await m.recoverExport(other)}catch(e){corrupt=e.message.includes('损坏')}await m.removeExport(other);const kept=await m.fileDigest(await m.recoverExport(op));return {mismatched,corrupt,kept}})()`); assert.deepEqual(storageChecks,{mismatched:true,corrupt:true,kept:op.file.sha256},'native IDB verifies identity, damaged metadata and upload-owned cleanup');
    await writeFile(join(evidenceDir,'editor-product-expected.json'),JSON.stringify({project,first,op,bytes,ta,source,storageChecks},null,2));
    await writeFile(join(evidenceDir,'editor-product-before-restart.png'),Buffer.from((await call('Page.captureScreenshot',{format:'png',captureBeyondViewport:true})).data,'base64'));
  } else {
    const expected=JSON.parse(await readFile(join(evidenceDir,'editor-product-expected.json'),'utf8')), {project,op,first}=expected;
    await wait(`document.body.innerText.includes('结果待确认')`); assert.equal(requests.filter(r=>r.method==='POST'&&r.url.startsWith('http://127.0.0.1:8001/')).length,0,'restart never auto retries');
    const bytes=await evaluate(`(async()=>{const m=await import('/src/features/assets/exportBytes.ts');window.recoveryFile=await m.recoverExport(${JSON.stringify(op)});return {sha:await m.fileDigest(window.recoveryFile),size:window.recoveryFile.size}})()`); assert.deepEqual(bytes,expected.bytes);
    await evaluate(`window.nativeFetch=window.fetch;window.observedUpload=null;window.fetch=async(...args)=>{if(String(args[0]).includes('/api/upload?')){const m=await import('/src/features/assets/exportBytes.ts');window.observedUpload={id:args[1].body.get('requestId'),sha:await m.fileDigest(args[1].body.get('file'))}}return window.nativeFetch(...args)}`);
    await evaluate(`window.realDelete=IDBObjectStore.prototype.delete;IDBObjectStore.prototype.delete=function(){throw Error('controlled cleanup refusal')}`);
    await click('重新查询/续投递'); await wait(`document.body.innerText.includes('导入完成') && !document.body.innerText.includes('结果待确认')`);
    const versions=(await api(project,'/api/versions')).items; assert.equal(versions.length,2); const revision=versions.find(v=>v.id!==first.id); assert.equal(revision.resource_id,first.resource_id); assert.equal(revision.source_version_id,first.id);
    assert(await evaluate(`document.body.innerText.includes('作品保存结果已确认')`),'cleanup failure still reports known success'); assert.equal(await evaluate(`document.querySelector('.object-details').dataset.versionId`),revision.id,'known result selected despite cleanup failure');
    await evaluate(`IDBObjectStore.prototype.delete=window.realDelete`);
    const upload=requests.find(r=>r.method==='POST'&&r.url.includes('/api/upload?')); assert(upload); const observedUpload=await evaluate('window.observedUpload'); assert.deepEqual(observedUpload,{id:op.uploadID,sha:op.file.sha256},'same upload identity and exact sent bytes after restart');
    // Restore the observed upload intent/bytes to model a lost result, then explicitly replay all boundaries.
    await evaluate(`(async()=>{const m=await import('/src/features/assets/exportBytes.ts');await m.persistExport(${JSON.stringify(op)},window.recoveryFile);localStorage.setItem('moviedesign.import.v1.${project}',${JSON.stringify(JSON.stringify(op))})})()`);
    await call('Page.reload'); await wait(`document.body.innerText.includes('结果待确认')`); await click('重新查询/续投递'); await wait(`document.body.innerText.includes('导入完成') && !document.body.innerText.includes('结果待确认')`);
    assert.equal((await api(project,'/api/versions')).items.length,2,'same upload/commit/deliver replay creates no extra version');
    const assets=(await api(project,'/api/assets')).items; const source=assets.find(a=>a.id===first.asset_id);
    const sourceData=await evaluate(`fetch('http://127.0.0.1:8001/api/files/content?project_id=${project}&path=${encodeURIComponent(source.path)}').then(r=>r.blob()).then(b=>new Promise(r=>{const f=new FileReader();f.onload=()=>r(f.result);f.readAsDataURL(b)}))`); assert.equal(sourceData,expected.source,'source immutable');
    await writeFile(join(evidenceDir,'editor-product-recovered.png'),Buffer.from((await call('Page.captureScreenshot',{format:'png'})).data,'base64'));
    await writeFile(join(evidenceDir,'editor-product.json'),JSON.stringify({bytes,observedUpload,versions,requests:requests.filter(r=>r.method==='POST'),exceptions},null,2));
  }
  assert.deepEqual(exceptions,[]); console.log(`M3.4 native product ${phase} passed; ${evidenceDir}`);
} catch(error) { await writeFile(join(evidenceDir,`editor-product-failure-${phase}.png`),Buffer.from((await call('Page.captureScreenshot',{format:'png'})).data,'base64')); await writeFile(join(evidenceDir,`editor-product-failure-${phase}.json`),JSON.stringify({error:String(error),body:await evaluate('document.body.innerText').catch(()=>''),requests,exceptions},null,2)); throw error; }
finally { close(); }
