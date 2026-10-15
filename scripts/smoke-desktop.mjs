// Exercises the real WebView2 renderer, not a browser substitute. No extra test dependency.
import assert from 'node:assert/strict';
import { writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { setTimeout as delay } from 'node:timers/promises';
import { connect } from './webview-cdp.mjs';

const [port, evidenceDir, expectedOrigin] = process.argv.slice(2);
assert(port && evidenceDir && expectedOrigin, 'usage: node smoke-desktop.mjs <debug-port> <evidence-dir> <expected-origin>');
const { call, exceptions, close } = await connect(port, expectedOrigin);
try {
  await call('Runtime.enable');
  let body = '';
  for (let attempt = 0; attempt < 100; attempt++) {
    const result = await call('Runtime.evaluate', { expression: 'document.body.innerText', returnByValue: true });
    body = result.result.value ?? '';
    if (body.includes('本地服务已连接')) break;
    await delay(200);
  }
  assert(body.includes('本地服务已连接'), `Frontend did not connect: ${body}`);
  assert(body.includes('留白，也是创作的开始'), 'Empty canvas missing');
  const result = await call('Runtime.evaluate', {
    expression: `(async () => ({ origin: location.origin, health: await fetch('http://127.0.0.1:8001/api/health').then(r => { if (!r.ok) throw new Error('HTTP '+r.status); return r.json(); }) }))()`,
    returnByValue: true, awaitPromise: true,
  });
  assert(!result.exceptionDetails, JSON.stringify(result.exceptionDetails));
  assert.equal(result.result.value.origin, expectedOrigin);
  assert.deepEqual(result.result.value.health, { status: 'ok', idle: true, safe_to_suspend: true });
  assert.deepEqual(exceptions, [], 'Uncaught renderer exceptions');
  const screenshot = await call('Page.captureScreenshot', { format: 'png' });
  await writeFile(join(evidenceDir, 'desktop.png'), Buffer.from(screenshot.data, 'base64'));
  await writeFile(join(evidenceDir, 'renderer.json'), JSON.stringify({ ...result.result.value, body, exceptions }, null, 2));
  console.log(`Desktop smoke passed: real WebView, ${expectedOrigin}, HTTP health, canvas. Evidence: ${evidenceDir}`);
} finally {
  close();
}
