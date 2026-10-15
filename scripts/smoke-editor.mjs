// Dev-only test document inside the actual native MovieDesign WebView2.
import assert from 'node:assert/strict';
import { writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { connect } from './webview-cdp.mjs';
import { createServer } from 'node:http';

const [port, evidenceDir, origin, phase] = process.argv.slice(2);
if (phase?.startsWith('product')) { await import('./smoke-editor-product.mjs'); process.exit(0); }
assert(port && evidenceDir && origin, 'usage: node smoke-editor.mjs <debug-port> <evidence-dir> <dev-origin>');
assert.equal(origin, 'http://localhost:1420', 'editor harness is a dev WebView check');
const { call, evaluate, wait, exceptions, close } = await connect(port, origin);
// Test-only image server: actual anonymous CORS and delayed HTTP responses.
const requests = {};
const imageServer = createServer((req, res) => {
  const path = new URL(req.url, 'http://localhost').pathname; requests[path] = (requests[path] ?? 0) + 1;
  if (path !== '/denied.svg') res.setHeader('Access-Control-Allow-Origin', '*');
  if (path === '/stats') { res.setHeader('Content-Type', 'application/json'); res.end(JSON.stringify(requests)); return; }
  res.setHeader('Cache-Control', 'no-store'); res.setHeader('Content-Type', 'image/svg+xml');
  const respond = () => {
    if (path === '/slow-error.svg') { res.writeHead(500); res.end('test failure'); return; }
    const fill = path === '/blue.svg' ? '#00f' : '#f00';
    const checker = Array.from({ length: 24 }, (_, y) => Array.from({ length: 24 }, (_, x) => `<rect x="${x * 8}" y="${y * 8}" width="8" height="8" fill="${(x + y) % 2 ? '#fff' : '#000'}"/>`).join('')).join('');
    res.end(path === '/checker.svg' ? `<svg xmlns="http://www.w3.org/2000/svg" width="192" height="192">${checker}</svg>` : `<svg xmlns="http://www.w3.org/2000/svg" width="160" height="80"><rect width="160" height="80" fill="${fill}"/></svg>`);
  };
  if (path.startsWith('/slow-')) setTimeout(respond, 200); else respond();
});
await new Promise(resolve => imageServer.listen(0, '127.0.0.1', resolve));
const imageOrigin = `http://127.0.0.1:${imageServer.address().port}`;
try {
  await call('Page.navigate', { url: `${origin}/packages/editor/test/render.html?imageServer=${encodeURIComponent(imageOrigin)}` });
  await wait('Boolean(window.editorTestResult)', 'editor native Canvas checks');
  const originalDpr = await evaluate('devicePixelRatio');
  await evaluate('window.dprProbe={changes:0,resizes:0}; window.addEventListener("resize",()=>window.dprProbe.resizes++);matchMedia(`(resolution: ${devicePixelRatio}dppx)`).addEventListener("change",()=>window.dprProbe.changes++)');
  await call('Emulation.setDeviceMetricsOverride', { width: 1024, height: 768, deviceScaleFactor: 2.5, mobile: false });
  await evaluate('window.dprProbe.frames=0; requestAnimationFrame(()=>window.dprProbe.frames++); new Promise(resolve=>setTimeout(resolve,250))');
  const dprAutomatic = await evaluate('({dpr:devicePixelRatio,width:window.editorInputFixture.editor.renderer.main.width,visibility:document.visibilityState,probe:{...window.dprProbe}})');
  // CDP emulation does not reliably deliver OS resolution events in WebView2.
  // Verify the registered handler with an explicit resize after DPR has updated.
  await evaluate('window.dispatchEvent(new Event("resize"))');
  await wait('window.editorInputFixture.editor.renderer.main.width === 500', 'DPR change reallocates physical buffers');
  await call('Emulation.clearDeviceMetricsOverride');
  await evaluate('window.dispatchEvent(new Event("resize"))');
  await wait(`window.editorInputFixture.editor.renderer.main.width === Math.round(200 * ${originalDpr})`, 'DPR listener rebinds after monitor change');
  await call('Input.dispatchMouseEvent', { type: 'mousePressed', x: 60, y: 50, button: 'left', clickCount: 1 });
  assert.equal(await evaluate('window.editorInputFixture.editor.renderer.main.hasPointerCapture(1)'), true, 'trusted pointer was not captured');
  await call('Input.dispatchMouseEvent', { type: 'mouseMoved', x: 80, y: 60, button: 'left', buttons: 1 });
  await call('Input.dispatchMouseEvent', { type: 'mouseMoved', x: 90, y: 65, button: 'left', buttons: 1 });
  await call('Input.dispatchMouseEvent', { type: 'mouseReleased', x: 90, y: 65, button: 'left', clickCount: 1 });
  const pointerState = await evaluate('window.editorInputFixture.editor.getState()');
  assert.deepEqual({ x: pointerState.shapes[0].x, y: pointerState.shapes[0].y }, { x: 50, y: 35 });
  assert.equal(await evaluate('window.editorInputFixture.editor.renderer.main.hasPointerCapture(1)'), false, 'trusted pointer capture was not released');
  await evaluate('window.editorInputFixture.editor.undo()');
  assert.equal(await evaluate('window.editorInputFixture.editor.getState().shapes[0].x'), 20);
  await evaluate('window.editorInputFixture.editor.destroy(); window.editorInputFixture.host.remove()');
  const result = await evaluate('window.editorTestResult');
  result.checks.push({ name: 'trusted native pointer capture, absolute drag, release and single undo', passed: true });
  result.checks.push({ name: 'controlled resize events at changed native DPR rebuild physical buffers in both directions', passed: true });
  result.observations.dprChanges = [originalDpr, 2.5, originalDpr];
  result.observations.dprEvents = await evaluate('window.dprProbe');
  result.observations.dprBeforeControlledEvent = dprAutomatic;
  const png = result.exportPNG; delete result.exportPNG;
  const nativePNG = result.observations.editorExportPNG; delete result.observations.editorExportPNG;
  await writeFile(join(evidenceDir, 'editor-canvas.json'), JSON.stringify({ ...result, exceptions }, null, 2));
  const pngBytes = Buffer.from(png.split(',')[1], 'base64');
  assert.equal(pngBytes.subarray(0, 8).toString('hex'), '89504e470d0a1a0a', 'native export is a PNG');
  assert.equal(pngBytes.readUInt32BE(16), 192); assert.equal(pngBytes.readUInt32BE(20), 192);
  await writeFile(join(evidenceDir, 'editor-export.png'), pngBytes);
  if (nativePNG) {
    await writeFile(join(evidenceDir, 'editor-native-export.png'), Buffer.from(nativePNG.split(',')[1], 'base64'));
  }
  const screenshot = await call('Page.captureScreenshot', { format: 'png', captureBeyondViewport: true });
  await writeFile(join(evidenceDir, 'editor-canvas.png'), Buffer.from(screenshot.data, 'base64'));
  assert.deepEqual(exceptions, [], 'uncaught native WebView exceptions');
  assert(result.passed, JSON.stringify(result.checks.filter(c => !c.passed), null, 2));
  console.log(`M3 native WebView Canvas passed: ${result.checks.length} checks. Evidence: ${evidenceDir}`);
} finally { close(); await new Promise(resolve => imageServer.close(resolve)); }
