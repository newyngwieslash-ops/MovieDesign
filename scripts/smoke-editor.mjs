// Dev-only test document inside the actual native MovieDesign WebView2.
import assert from 'node:assert/strict';
import { writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { connect } from './webview-cdp.mjs';

const [port, evidenceDir, origin] = process.argv.slice(2);
assert(port && evidenceDir && origin, 'usage: node smoke-editor.mjs <debug-port> <evidence-dir> <dev-origin>');
assert.equal(origin, 'http://localhost:1420', 'editor harness is a dev WebView check');
const { call, evaluate, wait, exceptions, close } = await connect(port, origin);
try {
  await call('Page.navigate', { url: `${origin}/packages/editor/test/render.html` });
  await wait('Boolean(window.editorTestResult)', 'editor native Canvas checks');
  const result = await evaluate('window.editorTestResult');
  const png = result.exportPNG; delete result.exportPNG;
  await writeFile(join(evidenceDir, 'editor-canvas.json'), JSON.stringify({ ...result, exceptions }, null, 2));
  const pngBytes = Buffer.from(png.split(',')[1], 'base64');
  assert.equal(pngBytes.subarray(0, 8).toString('hex'), '89504e470d0a1a0a', 'native export is a PNG');
  assert.equal(pngBytes.readUInt32BE(16), 192); assert.equal(pngBytes.readUInt32BE(20), 192);
  await writeFile(join(evidenceDir, 'editor-export.png'), pngBytes);
  const screenshot = await call('Page.captureScreenshot', { format: 'png', captureBeyondViewport: true });
  await writeFile(join(evidenceDir, 'editor-canvas.png'), Buffer.from(screenshot.data, 'base64'));
  assert.deepEqual(exceptions, [], 'uncaught native WebView exceptions');
  assert(result.passed, JSON.stringify(result.checks.filter(c => !c.passed), null, 2));
  console.log(`M3.2 native WebView Canvas passed: ${result.checks.length} checks. Evidence: ${evidenceDir}`);
} finally { close(); }
