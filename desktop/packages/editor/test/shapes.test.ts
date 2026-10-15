import test from 'node:test';
import assert from 'node:assert/strict';
import { computeResize, distanceToSegment, hitHandle, normalizeBounds } from '../src/geometry.ts';
import { cloneShapeData, drawSelectionHandles, findShapeAt, shapeFromData } from '../src/shapes.ts';
import type { ShapeData } from '../src/types.ts';

const style = { stroke: '#000', strokeWidth: 1 };
const box = { x: 0, y: 0, width: 100, height: 100 };

// This records the Canvas boundary only; pixel rendering is checked in M3.2.
function recordingContext() {
  const calls: { name: string; args: unknown[] }[] = [];
  const values: Record<string, unknown> = {};
  const ctx = new Proxy(values, {
    get: (target, name: string) => name in target ? target[name] : (...args: unknown[]) => calls.push({ name, args }),
    set: (target, name: string, value) => { target[name] = value; return true; },
  }) as unknown as CanvasRenderingContext2D;
  return { ctx, calls };
}

test('normalizes reverse drags and measures degenerate and clamped segments', () => {
  assert.deepEqual(normalizeBounds({ x: 20, y: 30, width: -10, height: -20 }), { x: 10, y: 10, width: 10, height: 20 });
  assert.equal(distanceToSegment({ x: 3, y: 4 }, { x: 0, y: 0 }, { x: 0, y: 0 }), 5);
  assert.equal(distanceToSegment({ x: 13, y: 4 }, { x: 0, y: 0 }, { x: 10, y: 0 }), 5);
  assert.equal(distanceToSegment({ x: 5, y: 4 }, { x: 0, y: 0 }, { x: 10, y: 0 }), 4);
});

test('an unfilled rectangle has a hollow hit region with an eight pixel minimum', () => {
  const rect = shapeFromData({ id: 'r', type: 'rectangle', style, ...box });
  assert.equal(rect.hitTest({ x: 50, y: 50 }), false);
  assert.equal(rect.hitTest({ x: 1, y: 50 }), true);
  assert.equal(rect.hitTest({ x: -8, y: 50 }), true);
  assert.equal(rect.hitTest({ x: -8.01, y: 50 }), false);
  assert.equal(rect.hitTest({ x: 8, y: 50 }), false);
});

test('filled rectangles include their interior and the original expanded hit box', () => {
  const rect = shapeFromData({ id: 'r', type: 'rectangle', style: { ...style, fill: '#fff', strokeWidth: 12 }, ...box });
  assert.equal(rect.hitTest({ x: 50, y: 50 }), true);
  assert.equal(rect.hitTest({ x: -12, y: 50 }), true);
  assert.equal(rect.hitTest({ x: -12.01, y: 50 }), false);
});

test('ellipse fill uses the interior while hollow hit testing follows the inflated ring', () => {
  const ellipse = shapeFromData({ id: 'e', type: 'ellipse', style, ...box });
  assert.equal(ellipse.hitTest({ x: 50, y: 50 }), false);
  assert.equal(ellipse.hitTest({ x: -8, y: 50 }), true);
  assert.equal(ellipse.hitTest({ x: -8.01, y: 50 }), false);
  assert.equal(ellipse.hitTest({ x: 8, y: 50 }), false);
  const filled = shapeFromData({ ...ellipse.toData(), style: { ...style, fill: '#fff' } });
  assert.equal(filled.hitTest({ x: 50, y: 50 }), true);
  assert.equal(filled.hitTest({ x: -1, y: 50 }), false);
  ellipse.resize({ ...box, width: 0 });
  assert.equal(ellipse.hitTest({ x: 0, y: 50 }), false);
});

test('reverse rectangle and ellipse data survive moves and resizes without a geometry wrapper', () => {
  for (const type of ['rectangle', 'ellipse'] as const) {
    const shape = shapeFromData({ id: type, type, style, x: 20, y: 30, width: -10, height: -20 });
    assert.deepEqual(shape.getBounds(), { x: 10, y: 10, width: 10, height: 20 });
    shape.move(5, -5);
    assert.deepEqual(shape.getBounds(), { x: 15, y: 5, width: 10, height: 20 });
    shape.resize({ x: 30, y: 40, width: -20, height: -10 });
    assert.deepEqual(shape.getBounds(), { x: 10, y: 30, width: 20, height: 10 });
    assert.deepEqual(shape.toData(), { id: type, type, style, x: 30, y: 40, width: -20, height: -10 });
  }
});

test('all resize corners can cross their opposite corner', () => {
  const cases = [
    ['nw', { x: 120, y: 130 }, { x: 100, y: 100, width: 20, height: 30 }],
    ['ne', { x: -20, y: 130 }, { x: -20, y: 100, width: 20, height: 30 }],
    ['sw', { x: 120, y: -30 }, { x: 100, y: -30, width: 20, height: 30 }],
    ['se', { x: -20, y: -30 }, { x: -20, y: -30, width: 20, height: 30 }],
  ] as const;
  for (const [corner, point, expected] of cases) assert.deepEqual(computeResize(box, corner, point), expected);
});

test('line and arrow hit testing clamps endpoints and handles zero segments', () => {
  for (const type of ['line', 'arrow'] as const) {
    const shape = shapeFromData({ id: type, type, style, x1: 10, y1: 10, x2: 10, y2: 10 });
    assert.equal(shape.hitTest({ x: 10, y: 18 }), true);
    assert.equal(shape.hitTest({ x: 10, y: 18.01 }), false);
    shape.resize({ x: 0, y: 0, width: 100, height: 0 });
    assert.equal(shape.hitTest({ x: 108, y: 0 }), true);
    assert.equal(shape.hitTest({ x: 109, y: 0 }), false);
  }
});

test('endpoint resizing preserves direction and original line versus arrow degeneracy rules', () => {
  for (const type of ['line', 'arrow'] as const) {
    const shape = shapeFromData({ id: type, type, style, x1: 100, y1: 0, x2: 0, y2: 50 });
    shape.resize({ x: 10, y: 20, width: 20, height: 40 });
    shape.move(1, 2);
    assert.deepEqual(shape.toData(), { id: type, type, style, x1: 31, y1: 22, x2: 11, y2: 62 });
    for (const endpoints of [{ x1: 5, y1: 0, x2: 5, y2: 100 }, { x1: 0, y1: 5, x2: 100, y2: 5 }]) {
      const flat = shapeFromData({ id: type, type, style, ...endpoints });
      flat.resize({ x: 10, y: 20, width: 30, height: 40 });
      const bounds = flat.getBounds();
      assert.ok(Object.values(bounds).every(Number.isFinite));
      assert.deepEqual(bounds, type === 'line' ? { x: 10, y: 20, width: 30, height: 40 }
        : endpoints.x1 === endpoints.x2 ? { x: 10, y: 20, width: 0, height: 40 } : { x: 10, y: 20, width: 30, height: 0 });
    }
  }
});

test('arrow bounds follow endpoints and drawing uses the original head dimensions', () => {
  for (const [strokeWidth, bodyX, baseX, halfWidth] of [[1, 94, 88, 4], [5, 90, 80, 7.5]]) {
    const arrow = shapeFromData({ id: 'a', type: 'arrow', style: { ...style, strokeWidth }, x1: 0, y1: 0, x2: 100, y2: 0 });
    assert.deepEqual(arrow.getBounds(), { x: 0, y: 0, width: 100, height: 0 });
    const { ctx, calls } = recordingContext();
    arrow.draw(ctx);
    assert.deepEqual(calls.filter(c => c.name === 'lineTo').map(c => c.args), [[bodyX, 0], [baseX, halfWidth], [baseX, -halfWidth]]);
    assert.equal(calls.at(-1)?.name, 'restore');
  }
});

test('line drawing uses its endpoints and hit padding grows with thick strokes', () => {
  const line = shapeFromData({ id: 'l', type: 'line', style: { ...style, strokeWidth: 12 }, x1: 10, y1: 20, x2: 80, y2: 20 });
  const { ctx, calls } = recordingContext();
  line.draw(ctx);
  assert.deepEqual(calls.filter(c => c.name === 'moveTo' || c.name === 'lineTo').map(c => c.args), [[10, 20], [80, 20]]);
  assert.equal(ctx.lineWidth, 12);
  assert.equal(line.hitTest({ x: 50, y: 32 }), true);
  assert.equal(line.hitTest({ x: 50, y: 32.01 }), false);
  assert.equal(calls.at(-1)?.name, 'restore');
});

test('selection outline scales padding and dash without mutating the saved style', () => {
  const shape = shapeFromData({ id: 'r', type: 'rectangle', style: { ...style, selectionStroke: '#123', selectionLineDash: [6, 2] }, ...box });
  const { ctx, calls } = recordingContext();
  shape.drawSelection(ctx, 2);
  assert.deepEqual(calls.find(c => c.name === 'setLineDash')?.args, [[3, 1]]);
  assert.deepEqual(calls.find(c => c.name === 'strokeRect')?.args, [-1, -1, 102, 102]);
  assert.equal(ctx.lineWidth, 0.5);
  assert.equal(ctx.strokeStyle, '#123');
  assert.deepEqual(shape.toData().style.selectionLineDash, [6, 2]);
  assert.equal(calls.at(-1)?.name, 'restore');
});

test('box drawing keeps signed rectangle dimensions and positive ellipse radii', () => {
  for (const type of ['rectangle', 'ellipse'] as const) {
    const shape = shapeFromData({ id: type, type, style: { ...style, fill: '#fff' }, x: 100, y: 50, width: -100, height: -50 });
    const { ctx, calls } = recordingContext();
    shape.draw(ctx);
    assert.deepEqual(calls.find(c => c.name === (type === 'rectangle' ? 'fillRect' : 'ellipse'))?.args,
      type === 'rectangle' ? [100, 50, -100, -50] : [50, 25, 50, 25, 0, 0, Math.PI * 2]);
    assert.equal(calls.at(-1)?.name, 'restore');
    const empty = recordingContext();
    shape.resize({ ...box, height: 0 });
    shape.draw(empty.ctx);
    assert.equal(empty.calls.length, 0);
  }
  const { ctx, calls } = recordingContext();
  shapeFromData({ id: 'a', type: 'arrow', style, x1: 1, y1: 2, x2: 1, y2: 2 }).draw(ctx);
  assert.equal(calls.length, 0);
});

test('deserialization and snapshots own independent nested style data', () => {
  const input = { id: 'r', type: 'rectangle' as const, style: { ...style, selectionLineDash: [4, 4] }, ...box };
  const shape = shapeFromData(input);
  input.style.selectionLineDash[0] = 99;
  input.x = 500;
  const snapshot = shape.toData();
  snapshot.style.selectionLineDash![0] = 88;
  snapshot.style.stroke = 'red';
  shape.move(3, 4);
  assert.deepEqual(shape.toData(), { id: 'r', type: 'rectangle', style: { ...style, selectionLineDash: [4, 4] }, x: 3, y: 4, width: 100, height: 100 });
});

test('cloneShapeData preserves flat media fields and deeply copies point arrays', () => {
  const data: ShapeData[] = [
    { id: 'b', type: 'brush', style, points: [{ x: 1, y: 2 }] },
    { id: 'm', type: 'mosaic', style, shape: 'brush', points: [{ x: 3, y: 4 }], brushSize: 24, mode: 'blur', strength: 8 },
    { id: 'mr', type: 'mosaic', style, shape: 'rectangle', ...box, mode: 'mosaic', strength: 12 },
    { id: 't', type: 'text', style: { ...style, textVariant: 'filled', fontSize: 18 }, ...box, text: '中文' },
    { id: 'tag', type: 'tag', style, ...box, anchorX: 2, anchorY: 3, text: '标注', maxWidth: 100 },
  ];
  for (const original of data) {
    const copy = cloneShapeData(original);
    assert.deepEqual(copy, original);
    copy.style.stroke = 'red';
    assert.equal(original.style.stroke, '#000');
    if ('points' in copy && 'points' in original) {
      copy.points[0].x = 99;
      assert.notEqual(original.points[0].x, 99);
    }
    assert.equal('geometry' in copy, false);
  }
});

test('selection finds the topmost hit and handles retain screen size at different zooms', () => {
  const bottom = shapeFromData({ id: 'bottom', type: 'rectangle', style: { ...style, fill: '#fff' }, ...box });
  const top = shapeFromData({ ...bottom.toData(), id: 'top' });
  assert.equal(findShapeAt([bottom, top], { x: 50, y: 50 }), top);
  assert.equal(findShapeAt([bottom, top], { x: 500, y: 500 }), undefined);
  for (const scale of [0, 0.5, 1, 2]) {
    const { ctx, calls } = recordingContext();
    drawSelectionHandles(ctx, box, scale);
    const size = scale > 0 ? 8 / scale : 8;
    assert.deepEqual(calls.filter(c => c.name === 'rect').map(c => c.args), [
      [-size / 2, -size / 2, size, size], [100 - size / 2, -size / 2, size, size],
      [-size / 2, 100 - size / 2, size, size], [100 - size / 2, 100 - size / 2, size, size],
    ]);
    assert.equal(ctx.lineWidth, scale > 0 ? 1.5 / scale : 1.5);
    assert.equal(hitHandle({ x: size, y: size }, box, scale), 'nw');
    assert.equal(hitHandle({ x: size + 0.1, y: size }, box, scale), null);
  }
});

test('the same factory accepts media shapes and keeps empty brush geometry finite', () => {
  const brush = shapeFromData({ id: 'b', type: 'brush', style, points: [] });
  assert.deepEqual(brush.getBounds(), { x: 0, y: 0, width: 0, height: 0 });
  assert.equal(brush.hitTest({ x: 0, y: 0 }), false);
});
