import { MosaicCache } from '../src/mosaic.ts';
import { shapeFromData } from '../src/shapes.ts';
import { Editor } from '../src/editor.ts';
import { Tools } from '../src/tools.ts';
import type { ShapeData, TextData, TagData } from '../src/types.ts';

const checks: { name: string; passed: boolean; detail?: string }[] = [];
const observations: Record<string, unknown> = {};
function check(name: string, run: () => void) {
  try { run(); checks.push({ name, passed: true }); }
  catch (error) { checks.push({ name, passed: false, detail: String(error) }); }
}
function assert(value: unknown, message: string): asserts value { if (!value) throw new Error(message); }
function canvas(width = 96, height = 96) {
  const c = document.createElement('canvas'); c.width = width; c.height = height; document.body.append(c); return c;
}
const context = (c: HTMLCanvasElement) => c.getContext('2d')!;
const pixel = (c: HTMLCanvasElement, x: number, y: number) => [...context(c).getImageData(x, y, 1, 1).data];
const same = (a: unknown, b: unknown) => JSON.stringify(a) === JSON.stringify(b);
const style = { stroke: '#f00000', strokeWidth: 4 };
const source = canvas(), sctx = context(source);
for (let y = 0; y < 96; y += 4) for (let x = 0; x < 96; x += 4) {
  sctx.fillStyle = (x / 4 + y / 4) % 2 ? '#fff' : '#000'; sctx.fillRect(x, y, 4, 4);
}
const cache = new MosaicCache(); cache.setSource(source, 1); cache.setSize(96, 96, devicePixelRatio);
const mosaicData: ShapeData = { id: 'm', type: 'mosaic', style, shape: 'rectangle', x: 24, y: 24, width: 48, height: 48, mode: 'mosaic', strength: 12 };

check('rectangle clips processed source; outside checkerboard is untouched', () => {
  for (const mode of ['mosaic', 'blur'] as const) {
    const out = canvas(); context(out).drawImage(source, 0, 0);
    shapeFromData({ ...mosaicData, mode }).draw(context(out), cache);
    const before = sctx.getImageData(0, 0, 96, 96).data, after = context(out).getImageData(0, 0, 96, 96).data;
    for (let y = 0; y < 96; y++) for (let x = 0; x < 96; x++) if (x < 24 || x >= 72 || y < 24 || y >= 72) {
      const i = (y * 96 + x) * 4;
      assert(before.slice(i, i + 4).every((v, j) => v === after[i + j]), `${mode} outside mask changed at ${x},${y}`);
    }
    assert(!same(pixel(out, 32, 32), pixel(source, 32, 32)), `${mode} inside mask was not processed`);
  }
});
check('modes differ, hits reuse canvas, ninth item evicts oldest and hit promotes', () => {
  cache.clear();
  const first = cache.getProcessedSource('mosaic', 12)!;
  assert(cache.getProcessedSource('mosaic', 12) === first, 'same key reprocessed');
  const blur = cache.getProcessedSource('blur', 12)!;
  assert(blur !== first, 'two modes shared a result');
  assert(!same([...context(blur).getImageData(0, 0, 96, 96).data], [...context(first).getImageData(0, 0, 96, 96).data]), 'mode pixels equal');
  cache.clear();
  for (let i = 2; i < 10; i++) cache.getProcessedSource('mosaic', i);
  const promoted = cache.get('mosaic-2');
  cache.getProcessedSource('blur', 3);
  assert(cache.get('mosaic-3') === undefined && cache.get('mosaic-2') === promoted, 'hit not promoted or ninth not evicted');
});
check('revision, source identity, dimensions and DPR force real reprocessing', () => {
  let previous = cache.getProcessedSource('mosaic', 12)!;
  for (const mutate of [() => cache.setSource(source, 2), () => cache.setSize(96, 96, devicePixelRatio + 1), () => cache.setSize(80, 70, 1), () => cache.setSource(canvas(80, 70), 2)]) {
    mutate(); const next = cache.getProcessedSource('mosaic', 12)!;
    assert(next !== previous, 'stale processed image'); previous = next;
  }
  assert(previous.width === 80 && previous.height === 70, 'logical processing dimensions wrong');
  cache.setSource(source, 3); cache.setSize(96, 96, devicePixelRatio);
});
check('edge-clamped blur matches original finite padding and resets filter', () => {
  const solid = canvas(30, 30); context(solid).fillStyle = '#4488cc'; context(solid).fillRect(0, 0, 30, 30);
  const local = new MosaicCache(); local.setSource(solid, 1);
  const blurred = local.getProcessedSource('blur', 5)!;
  const edge = pixel(blurred, 0, 0);
  // A solid 50x50 is the independent edge-clamped reference for 30x30 + 2*10 padding.
  // Original radius=5 uses finite padding=10, so the filter's tail need not yield alpha=255 at the crop corner.
  const padded = canvas(50, 50), reference = canvas(30, 30);
  context(padded).fillStyle = '#4488cc'; context(padded).fillRect(0, 0, 50, 50);
  context(reference).filter = 'blur(5px)'; context(reference).drawImage(padded, -10, -10);
  const expectedEdge = pixel(reference, 0, 0);
  observations.blur = { edge, expectedEdge, center: pixel(blurred, 15, 15) };
  assert(same(edge, expectedEdge), `edge/corner replication differs from original finite-pad reference: ${edge} / ${expectedEdge}`);
  assert(pixel(blurred, 15, 15)[3] === 255, 'solid center lost opacity');
  assert(context(blurred).filter === 'none', 'filter leaked');
});
check('brush mask uses quadratic midpoint path, leaves outside unchanged and reuses scratch canvas', () => {
  const out = canvas(), ctx = context(out); ctx.drawImage(source, 0, 0);
  const data: ShapeData = { id: 'mb', type: 'mosaic', style, shape: 'brush', mode: 'mosaic', strength: 12, brushSize: 10, points: [{ x: 12, y: 70 }, { x: 40, y: 10 }, { x: 80, y: 70 }] };
  const brush = shapeFromData(data);
  const originalCreate = document.createElement.bind(document);
  let allocated = 0;
  document.createElement = ((tag: string, options?: ElementCreationOptions) => { if (tag === 'canvas') allocated++; return originalCreate(tag, options); }) as typeof document.createElement;
  try { brush.draw(ctx, cache); const afterFirst = allocated; brush.draw(ctx, cache); assert(allocated === afterFirst, 'second draw allocated brush buffer or reprocessed source'); }
  finally { document.createElement = originalCreate; }
  assert(!same(pixel(out, 41, 33), pixel(source, 41, 33)), 'quadratic curve mask absent');
  assert(same(pixel(out, 40, 10), pixel(source, 40, 10)), 'control point treated as straight segment');
  assert(same(pixel(out, 90, 90), pixel(source, 90, 90)), 'outside brush mask changed');
  const snapshot = JSON.stringify(brush.toData()); assert(!snapshot.includes('canvas') && !snapshot.includes('Tmp'), 'memory entered data');
  if (brush.data.type === 'mosaic' && brush.data.shape === 'brush') {
    brush.data.points = [{ x: 60, y: 60 }]; brush.data.mode = 'blur';
    ctx.clearRect(0, 0, 96, 96); ctx.drawImage(source, 0, 0); brush.draw(ctx, cache);
    assert(!same(pixel(out, 60, 60), pixel(source, 60, 60)), 'single-point blur mask absent');
    assert(same(pixel(out, 41, 33), pixel(source, 41, 33)), 'reused mask retained old curve');
  }
});
check('free brush renders a quadratic curve and single point', () => {
  const out = canvas(), ctx = context(out);
  shapeFromData({ id: 'b', type: 'brush', style, points: [{ x: 12, y: 70 }, { x: 40, y: 10 }, { x: 80, y: 70 }] }).draw(ctx);
  // First quadratic: start(12,70), control(40,10), end(60,40), t=.5 => (38,32.5).
  assert(pixel(out, 38, 33)[3] > 0, 'smoothed quadratic pixel absent');
  assert(pixel(out, 40, 10)[3] === 0, 'control point became polyline vertex');
  shapeFromData({ id: 'dot', type: 'brush', style: { ...style, strokeWidth: 10 }, points: [{ x: 85, y: 85 }] }).draw(ctx);
  assert(pixel(out, 85, 85)[3] === 255, 'single point not drawn');
});
check('text variants measure filled padding and draw in real Canvas', () => {
  for (const variant of ['plain', 'filled', 'outlined'] as const) {
    const out = canvas(200, 90), ctx = context(out);
    const shape = shapeFromData({ id: variant, type: 'text', x: 20, y: 15, width: 0, height: 0, text: '中文 Ab\n第二行', style: { ...style, fontSize: 20, textVariant: variant } });
    shape.draw(ctx);
    const data = shape.toData() as TextData, bounds = shape.getBounds();
    assert(data.width > 30 && data.height >= 56, 'text bounds were not measured');
    assert([...ctx.getImageData(0, 0, 200, 90).data].some((value, i) => i % 4 === 3 && value > 0), 'text pixels absent');
    if (variant === 'filled') assert(pixel(out, Math.ceil(bounds.x + 1), Math.ceil(bounds.y + 1))[3] === 255 && bounds.width === data.width, 'filled card and bounds disagree');
  }
});
check('short filled text card covers its full advertised bounds', () => {
  const out = canvas(), ctx = context(out);
  const shape = shapeFromData({ id: 'short', type: 'text', x: 20, y: 20, width: 0, height: 0, text: '.', style: { ...style, fontSize: 20, textVariant: 'filled' } });
  shape.draw(ctx); const bounds = shape.getBounds();
  assert(pixel(out, Math.floor(bounds.x + bounds.width - 1), Math.ceil(bounds.y + 1))[3] === 255, 'short filled card does not cover its bounds');
});
check('tag wraps CJK, computes pointer/anchor and clamps card in supplied canvas', () => {
  const out = canvas(180, 120);
  const shape = shapeFromData({ id: 'tag', type: 'tag', x: 170, y: 45, width: 44, height: 26, anchorX: 160, anchorY: 60, text: '中文换行测试', maxWidth: 50, style: { ...style, fontSize: 13 } });
  shape.draw(context(out), { canvasWidth: 180, canvasHeight: 120 });
  const data = shape.toData() as TagData;
  assert(data.height > 40 && data.width <= 50, 'CJK did not wrap');
  assert(data.x >= 4 && data.x + data.width <= 176 && data.y >= 4 && data.y + data.height <= 116, 'card escaped canvas');
  assert(pixel(out, Math.round(data.anchorX), Math.round(data.anchorY))[0] > 200, 'anchor not drawn');
});
check('tag pointers and corrected anchors follow all four card sides', () => {
  for (const [side, anchorX, anchorY] of [['left', 0, 80], ['right', 190, 80], ['top', 80, 0], ['bottom', 80, 190]] as const) {
    const out = canvas(200, 200), shape = shapeFromData({ id: side, type: 'tag', x: 70, y: 70, width: 40, height: 20, anchorX, anchorY, text: 'AB', style });
    shape.draw(context(out)); const d = shape.toData() as TagData;
    const cx = d.x + d.width / 2, cy = d.y + d.height / 2;
    const x = side === 'left' ? d.x - 2 : side === 'right' ? d.x + d.width + 2 : cx;
    const y = side === 'top' ? d.y - 2 : side === 'bottom' ? d.y + d.height + 2 : cy;
    assert(pixel(out, Math.round(x), Math.round(y))[3] > 0, `${side} pointer missing`);
    assert(Math.abs(d.anchorX - (side === 'left' ? d.x - 7.5 : side === 'right' ? d.x + d.width + 7.5 : cx)) < 0.001, `${side} anchor x incorrect`);
    assert(Math.abs(d.anchorY - (side === 'top' ? d.y - 7.5 : side === 'bottom' ? d.y + d.height + 7.5 : cy)) < 0.001, `${side} anchor y incorrect`);
  }
});
check('tag rounded corners retain the original quadratic geometry', () => {
  const out = canvas(), reference = canvas(), shape = shapeFromData({ id: 'corner', type: 'tag', x: 20, y: 20, width: 40, height: 20, anchorX: 80, anchorY: 30, text: 'AB', style });
  shape.draw(context(out));
  const { width: w, height: h } = shape.toData() as TagData, ctx = context(reference);
  ctx.beginPath(); ctx.moveTo(26, 20); ctx.lineTo(20 + w - 6, 20); ctx.quadraticCurveTo(20 + w, 20, 20 + w, 26);
  ctx.lineTo(20 + w, 20 + h - 6); ctx.quadraticCurveTo(20 + w, 20 + h, 20 + w - 6, 20 + h);
  ctx.lineTo(26, 20 + h); ctx.quadraticCurveTo(20, 20 + h, 20, 20 + h - 6); ctx.lineTo(20, 26); ctx.quadraticCurveTo(20, 20, 26, 20); ctx.closePath();
  // Include the original pointer subpath: Chromium rasterizes a compound path
  // differently from an isolated rounded rectangle, even at the opposite corner.
  ctx.moveTo(20 + w, 20 + h / 2 - 4); ctx.lineTo(24 + w, 20 + h / 2); ctx.lineTo(20 + w, 20 + h / 2 + 4); ctx.closePath();
  ctx.fillStyle = 'rgba(0, 0, 0, 0.6)'; ctx.fill();
  const actual = [...context(out).getImageData(20, 20, 6, 6).data], expected = [...ctx.getImageData(20, 20, 6, 6).data];
  assert(same(actual, expected), 'corner differs from original quadratic path');
});
check('all eight objects restore context state for subsequent drawing', () => {
  const out = canvas(180, 120), ctx = context(out);
  const objects: ShapeData[] = [
    { id: 'r', type: 'rectangle', style, x: 5, y: 5, width: 20, height: 20 },
    { id: 'e', type: 'ellipse', style, x: 5, y: 5, width: 20, height: 20 },
    { id: 'l', type: 'line', style, x1: 5, y1: 5, x2: 25, y2: 25 },
    { id: 'a', type: 'arrow', style, x1: 5, y1: 5, x2: 25, y2: 25 },
    { id: 'b', type: 'brush', style, points: [{ x: 10, y: 10 }] },
    { id: 't', type: 'text', style, x: 5, y: 5, width: 0, height: 0, text: 'A' },
    { id: 'tag', type: 'tag', style, x: 30, y: 30, width: 40, height: 20, anchorX: 20, anchorY: 40, text: '标注' }, mosaicData,
    { id: 'mb', type: 'mosaic', style, shape: 'brush', mode: 'blur', strength: 5, brushSize: 12, points: [{ x: 20, y: 20 }, { x: 70, y: 30 }] },
  ];
  ctx.fillStyle = '#00ff00'; ctx.strokeStyle = '#00aaff'; ctx.lineWidth = 7; ctx.font = '11px monospace'; ctx.textAlign = 'right'; ctx.textBaseline = 'bottom';
  ctx.filter = 'none'; ctx.globalCompositeOperation = 'source-over'; ctx.setLineDash([3, 2]);
  const state = () => [ctx.fillStyle, ctx.strokeStyle, ctx.lineWidth, ctx.font, ctx.textAlign, ctx.textBaseline, ctx.filter, ctx.globalCompositeOperation, ctx.getLineDash(), [...['a', 'b', 'c', 'd', 'e', 'f'].map(k => (ctx.getTransform() as unknown as Record<string, number>)[k])]];
  const before = state();
  for (const data of objects) { shapeFromData(data).draw(ctx, cache); assert(same(state(), before), `${data.type} leaked context state`); }
  ctx.fillRect(160, 100, 10, 10); assert(same(pixel(out, 165, 105), [0, 255, 0, 255]), 'later fill corrupted');
});

const exported = canvas(192, 192), exportCtx = context(exported);
check('native PNG export redraws source and masked object at double resolution', () => {
  exportCtx.drawImage(source, 0, 0, 192, 192);
  const outside = pixel(exported, 16, 16), inside = pixel(exported, 64, 64);
  exportCtx.scale(2, 2);
  shapeFromData(mosaicData).draw(exportCtx, cache);
  assert(same(pixel(exported, 16, 16), outside), 'export outside mask wrong');
  assert(!same(pixel(exported, 64, 64), inside), 'export mask absent');
});
const frame = () => new Promise<void>(resolve => requestAnimationFrame(() => resolve()));
async function nativeCheck(name: string, run: () => Promise<void>) {
  try { await run(); checks.push({ name, passed: true }); }
  catch (error) { checks.push({ name, passed: false, detail: String(error) }); }
}
const server = new URLSearchParams(location.search).get('imageServer');
await nativeCheck('Renderer coalesces invalidations and drag draws only its active object after static exclusion', async () => {
  const host = document.createElement('div'); document.body.append(host);
  const editor = new Editor(host, { width: 200, height: 100, shapes: Array.from({ length: 50 }, (_, i) => ({ id: `drag${i}`, type: 'rectangle', x: 20, y: 20, width: 60, height: 40, style: { ...style, fill: '#fff' } })) });
  try {
    await frame();
    const main = editor.renderer.main.getContext('2d')!, originalDraw = main.drawImage.bind(main); let copies = 0;
    main.drawImage = ((...args: Parameters<CanvasRenderingContext2D['drawImage']>) => { copies++; originalDraw(...args); }) as typeof main.drawImage;
    for (let i = 0; i < 100; i++) editor.renderer.invalidate();
    await frame(); assert(copies === 3, `100 invalidations made ${copies / 3} composites`); main.drawImage = originalDraw;
    const counts = new Map<string, number>();
    for (const data of editor.getState().shapes) {
      const shape = editor.getShape(data.id)!, draw = shape.draw.bind(shape);
      shape.draw = (ctx, helpers) => { counts.set(shape.id, (counts.get(shape.id) ?? 0) + 1); draw(ctx, helpers); };
    }
    editor.select('drag49'); const tool = new Tools(editor, 'select'); tool.down({ x: 50, y: 40 }); await frame();
    for (let i = 1; i <= 100; i++) { tool.move({ x: 50 + i / 2, y: 40 + i / 4 }); await frame(); }
    assert(counts.get('drag0') === 1 && counts.get('drag49') === 101, `drag redrew static objects: ${JSON.stringify([...counts])}`);
    tool.up({ x: 100, y: 65 }); await frame();
    assert(counts.get('drag0') === 2 && counts.get('drag49') === 102, 'commit did not restore static membership once');
    assert(same(editor.getShape('drag49')!.getBounds(), { x: 70, y: 45, width: 60, height: 40 }), 'drag accumulated deltas');
    observations.renderer = { invalidations: 100, composites: copies / 3, dragFrames: 100, inactiveDraws: counts.get('drag0'), activeDraws: counts.get('drag49') };
  } finally { editor.destroy(); host.remove(); }
});
await nativeCheck('Editor exports natural dimensions and omits selection/preview at DPR and UI tiers', async () => {
  assert(server, 'native runner image server missing');
  const host = document.createElement('div'); document.body.append(host);
  const editor = new Editor(host, { width: 96, height: 96, shapes: [mosaicData] });
  try {
    await editor.loadImage(`${server}/checker.svg`); editor.setUiScale(2); editor.select('m');
    editor.setPreviewShape(shapeFromData({ id: 'excluded', type: 'rectangle', x: 0, y: 0, width: 96, height: 96, style: { stroke: '#0f0', strokeWidth: 5, fill: '#0f0' } }));
    await frame();
    const bitmap = await createImageBitmap(await editor.toBlob());
    assert(bitmap.width === 192 && bitmap.height === 192, `export ${bitmap.width}x${bitmap.height}`);
    const out = canvas(192, 192); context(out).drawImage(bitmap, 0, 0); bitmap.close();
    const original = new Image(); original.crossOrigin = 'anonymous'; original.src = `${server}/checker.svg`;
    await original.decode();
    const reference = canvas(192, 192), ref = context(reference); ref.drawImage(original, 0, 0); ref.scale(2, 2); shapeFromData(mosaicData).draw(ref, cache);
    assert(same(pixel(out, 16, 16), pixel(reference, 16, 16)), `preview exported: ${pixel(out, 16, 16)} vs ${pixel(reference, 16, 16)}`);
    assert(same(pixel(out, 64, 64), pixel(reference, 64, 64)), 'mosaic logical source was multiplied by DPR');
    assert(same(pixel(out, 47, 47), pixel(reference, 47, 47)), 'selection exported');
    observations.editorExport = { width: 192, height: 192, physicalWidth: editor.renderer.main.width, logicalWidth: 96, uiScale: 2 };
    observations.editorExportPNG = out.toDataURL('image/png');
  } finally { editor.destroy(); host.remove(); }
});
await nativeCheck('real image requests reject missing CORS once and ignore stale success/error responses', async () => {
  assert(server, 'native runner image server missing');
  const host = document.createElement('div'); document.body.append(host); const editor = new Editor(host, { width: 80, height: 80 });
  try {
    const stale = editor.loadImage(`${server}/slow-red.svg`), latest = editor.loadImage(`${server}/blue.svg`);
    await Promise.all([stale, latest]);
    assert(same(editor.getSize(), { width: 80, height: 40 }), 'logical size getter used natural or physical pixels');
    const exported = editor.renderer.exportComposed(); assert(exported.width === 160 && exported.height === 80, 'stale image changed size');
    assert(same(pixel(exported, 10, 10), [0, 0, 255, 255]), 'stale image overwrote latest pixels');
    const staleError = editor.loadImage(`${server}/slow-error.svg`); await editor.loadImage(`${server}/blue.svg?revision=2`); await staleError;
    let rejected = false; try { await editor.loadImage(`${server}/denied.svg`); } catch { rejected = true; }
    assert(rejected, 'missing CORS was retried without anonymous mode');
    const afterFailure = editor.renderer.exportComposed(); assert(same(pixel(afterFailure, 10, 10), [0, 0, 255, 255]), 'failed load erased existing image');
    const stats = await (await fetch(`${server}/stats`)).json() as Record<string, number>;
    assert(stats['/denied.svg'] === 1, 'CORS rejection was retried'); observations.imageLoads = stats;
  } finally { editor.destroy(); host.remove(); }
});
await nativeCheck('destroy cancels queued frames, clears owned canvases and settles pending image loads', async () => {
  assert(server, 'native runner image server missing');
  const host = document.createElement('div'); document.body.append(host); const editor = new Editor(host, { width: 80, height: 80 });
  const pending = editor.loadImage(`${server}/slow-red.svg?destroy=1`); let changes = 0; editor.on('change', () => changes++);
  let paints = 0; const ctx = editor.renderer.main.getContext('2d')!, originalDraw = ctx.drawImage.bind(ctx);
  ctx.drawImage = ((...args: Parameters<CanvasRenderingContext2D['drawImage']>) => { paints++; originalDraw(...args); }) as typeof ctx.drawImage;
  editor.destroy(); await pending; await frame(); await new Promise(resolve => setTimeout(resolve, 250));
  assert(host.childElementCount === 0 && changes === 0 && paints === 0, 'destroy left DOM, handlers or scheduled paint');
  for (const layer of [editor.renderer.main, editor.renderer.bgLayer, editor.renderer.shapeLayer, editor.renderer.previewLayer]) assert(layer.width === 0 && layer.height === 0, 'owned buffer retained dimensions');
  let rejected = false; try { await editor.toBlob(); } catch { rejected = true; } assert(rejected, 'destroyed export succeeded');
  host.remove(); observations.destroy = { changes, paints, detached: true };
});
const result = { passed: checks.every(c => c.passed), checks, observations, userAgent: navigator.userAgent, dpr: devicePixelRatio, exportPNG: exported.toDataURL('image/png') };
(window as unknown as { editorTestResult: typeof result }).editorTestResult = result;
document.querySelector('#result')!.textContent = JSON.stringify({ ...result, exportPNG: '(returned to CDP runner)' }, null, 2);
// Trusted pointer capture is exercised by the existing CDP runner after these checks.
const inputHost = document.createElement('div'); inputHost.style.cssText = 'position:fixed;left:10px;top:10px;z-index:100;background:white'; document.body.append(inputHost);
const inputEditor = new Editor(inputHost, { width: 200, height: 100, shapes: [{ id: 'trusted', type: 'rectangle', x: 20, y: 20, width: 60, height: 40, style: { ...style, fill: '#fff' } }] });
(window as unknown as { editorInputFixture: { editor: Editor; host: HTMLElement } }).editorInputFixture = { editor: inputEditor, host: inputHost };
