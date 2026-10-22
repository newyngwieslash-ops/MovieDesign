// Real production WebView/native IPC with controlled cached policy and unknown
// receipt rows in this run's isolated database. Never calls a real installer.
import assert from 'node:assert/strict';
import {writeFile} from 'node:fs/promises';
import {join} from 'node:path';
import {DatabaseSync} from 'node:sqlite';
import {connect} from './webview-cdp.mjs';
const [port,dir,origin,phase='initial']=process.argv.slice(2);
const {evaluate,wait,call,exceptions,close}=await connect(port,origin);
const key='moviedesign.update.pending.v1';
const intent={candidateId:'controlled-recovery-candidate',version:'2.0.0',decision:'approve',requestId:'controlled-recovery-decision',installRequestId:'controlled-recovery-install'};
try {
 await wait(`document.body?.innerText.includes('本地服务已连接')`);
 await evaluate(`[...document.querySelectorAll('button')].find(b=>b.textContent==='版本与更新').click()`);
 const native=await evaluate(`(async()=>{
  const api=window.__TAURI_INTERNALS__;
  const policy=await api.invoke('updater_policy');
  const report=await api.invoke('updater_check_and_gate',{requestId:crypto.randomUUID(),currentVersion:'999.0.0'});
  return {policy,report};
 })()`);
 assert.equal(native.report.action,'manual_only_blocked');
 assert.equal(native.report.currentVersion,native.policy.currentVersion);
 assert.notEqual(native.report.currentVersion,'999.0.0','frontend version spoof ignored');
 assert.equal(native.report.candidateId,undefined);
 const privateStatus=await evaluate(`fetch('http://127.0.0.1:8001/internal/updates/decision',{method:'POST',body:JSON.stringify({requestId:'webview-probe'})}).then(r=>r.status)`);
 assert.equal(privateStatus,403);
 if(phase==='initial') {
  await evaluate(`document.querySelector('[data-testid="update-check-btn"]').click()`);
  await wait(`document.querySelector('[data-testid="native-updater-report"]')?.textContent.includes('manual_only_blocked')`);
  assert.equal(await evaluate(`!!document.querySelector('[data-testid="update-approve-btn"]')`),false);
  const db=new DatabaseSync(join(dir,'moviedesign.db'));
  try {
   const insert=db.prepare(`INSERT INTO write_requests(project_id,operation,request_id,input_json,state,response_json) VALUES('_app',?,?,?,'completed',?)`);
   const decision={requestId:intent.requestId,candidateId:intent.candidateId,version:intent.version,currentVersion:native.report.currentVersion,decision:'approve'};
   insert.run('updates.decision',intent.requestId,JSON.stringify(decision),JSON.stringify({...decision,status:'approve'}));
   const install={requestId:intent.installRequestId,candidateId:intent.candidateId,receiptId:intent.requestId,version:intent.version,currentVersion:native.report.currentVersion};
   insert.run('updates.install',intent.installRequestId,JSON.stringify(install),JSON.stringify({...install,status:'install_started'}));
  }finally{db.close();}
  await evaluate(`localStorage.setItem(${JSON.stringify(key)},${JSON.stringify(JSON.stringify(intent))})`);
 } else {
  await wait(`document.body.innerText.includes('已保存原更新请求')`);
  assert.equal(await evaluate(`document.querySelector('[data-testid="update-check-btn"]').disabled`),true);
  await evaluate(`[...document.querySelectorAll('button')].find(b=>b.textContent==='查询原更新请求').click()`);
  await wait(`document.body.innerText.includes('原安装请求：install_started')`);
  // Lost localStorage cannot hide the durable native ledger or cause auto-install.
  await evaluate(`localStorage.removeItem(${JSON.stringify(key)});location.reload()`);
  await wait(`document.body?.innerText.includes('本地服务已连接')`);
  await evaluate(`[...document.querySelectorAll('button')].find(b=>b.textContent==='版本与更新').click()`);
  await wait(`[...document.querySelectorAll('summary')].some(s=>s.textContent==='应用更新原请求账本')`);
  await evaluate(`[...document.querySelectorAll('summary')].find(s=>s.textContent==='应用更新原请求账本').click()`);
  await wait(`document.body.innerText.includes('controlled-recovery-install')`);
  assert.equal(await evaluate(`!!document.querySelector('[data-testid="update-approve-btn"]')`),false);
 }
 assert.deepEqual(exceptions,[]);
 const db=new DatabaseSync(join(dir,'moviedesign.db'),{readOnly:true});
 let installRows;try{installRows=db.prepare(`SELECT count(*) AS n FROM write_requests WHERE operation='updates.install'`).get().n;assert.equal(installRows,1,'only seeded unknown intent; no repeated installation');}finally{db.close();}
 const shot=await call('Page.captureScreenshot',{format:'png'});
 await writeFile(join(dir,`updates-${phase}.png`),Buffer.from(shot.data,'base64'));
 await writeFile(join(dir,`updates-${phase}.json`),JSON.stringify({phase,native,privateStatus,controlledRecoveryReceipt:true,actualInstallCalls:0,installRows,ui:await evaluate(`({pending:localStorage.getItem(${JSON.stringify(key)}),body:document.body.innerText})`),exceptions},null,2));
 console.log(`Updates WebView ${phase}: native manual_only/current version/private control; durable unknown recovery; zero real installs. ${dir}`);
}finally{close();}
