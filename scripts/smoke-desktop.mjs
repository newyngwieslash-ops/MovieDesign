// Exercises the real WebView2 renderer, not a browser substitute. No extra test dependency.
import assert from 'node:assert/strict';
import { writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { setTimeout as delay } from 'node:timers/promises';

const [port, evidenceDir, expectedOrigin] = process.argv.slice(2);
assert(port && evidenceDir && expectedOrigin, 'usage: node smoke-desktop.mjs <debug-port> <evidence-dir> <expected-origin>');
let target;
for (let attempt = 0; attempt < 100; attempt++) {
  try {
    const targets = await fetch(`http://127.0.0.1:${port}/json/list`, { signal: AbortSignal.timeout(1000) }).then(r => r.json());
    target = targets.find(t => t.type === 'page' && t.url.startsWith(expectedOrigin));
    if (target) break;
  } catch { /* WebView is still starting. */ }
  await delay(200);
}
assert(target, 'MovieDesign WebView did not start');
const socket = new WebSocket(target.webSocketDebuggerUrl);
await new Promise((resolve, reject) => { socket.addEventListener('open', resolve, { once: true }); socket.addEventListener('error', reject, { once: true }); });
let sequence = 0;
const pending = new Map();
const exceptions = [];
socket.addEventListener('message', ({ data }) => {
  const message = JSON.parse(data);
  if (message.method === 'Runtime.exceptionThrown') exceptions.push(message.params);
  const callback = pending.get(message.id);
  if (callback) { pending.delete(message.id); callback(message); }
});
const call = (method, params = {}) => new Promise((resolve, reject) => {
  const id = ++sequence;
  const timer = setTimeout(() => { pending.delete(id); reject(new Error(`CDP ${method} timed out`)); }, 10000);
  pending.set(id, response => { clearTimeout(timer); response.error ? reject(new Error(JSON.stringify(response.error))) : resolve(response.result); });
  socket.send(JSON.stringify({ id, method, params }));
});
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
  socket.close();
}
