import test from 'node:test';
import assert from 'node:assert/strict';
import { LRU, MosaicCache } from '../src/mosaic.ts';
import * as shapes from '../src/shapes.ts';
const { shapeFromData } = shapes;
import type { TagData, TextData } from '../src/types.ts';

const style = { stroke: '#000', strokeWidth: 1 };

test('ninth cache item evicts the oldest', () => {
  const cache = new LRU<number>();
  for (let i = 0; i < 9; i++) cache.put(String(i), i);
  assert.equal(cache.get('0'), undefined);
  assert.equal(cache.get('8'), 8);
});

test('a cache hit promotes even zero and overwrite promotes without growing', () => {
  const cache = new LRU<number>();
  for (let i = 0; i < 8; i++) cache.put(String(i), i);
  assert.equal(cache.get('0'), 0);
  cache.put('8', 8);
  assert.equal(cache.get('1'), undefined);
  cache.put('2', 22);
  cache.put('9', 9);
  assert.equal(cache.get('3'), undefined);
  assert.equal(cache.get('2'), 22);
  cache.clear();
  assert.equal(cache.get('0'), undefined);
});

test('source identity, revision, dimensions and DPR invalidate processed memory', () => {
  // Opaque identity tokens only: this test never asks Node to render Canvas pixels.
  const image = { width: 64, height: 64 } as HTMLCanvasElement;
  const processed = {} as HTMLCanvasElement;
  const cache = new MosaicCache();
  cache.setSource(image, 1);
  cache.setSize(64, 64, 1);
  cache.put('mosaic-8', processed);
  cache.setSource(image, 1);
  cache.setSize(64, 64, 1);
  assert.equal(cache.get('mosaic-8'), processed);
  cache.setSource(image, 2);
  assert.equal(cache.get('mosaic-8'), undefined);
  cache.put('x', processed);
  cache.setSource({ width: 64, height: 64 } as HTMLCanvasElement, 2);
  assert.equal(cache.get('x'), undefined);
  cache.put('x', processed);
  cache.setSize(64, 64, 2);
  assert.equal(cache.get('x'), undefined);
  cache.put('x', processed);
  cache.setSize(80, 64, 2);
  assert.equal(cache.get('x'), undefined);
  cache.setSource(image, 2); cache.put('x', processed);
  image.width = 90; cache.setSource(image, 2);
  assert.equal(cache.get('x'), undefined);
});

test('brush snapshots own points, move and resize with original degenerate rules', () => {
  const input = { id: 'b', type: 'brush' as const, style, points: [{ x: 0, y: 0 }, { x: 20, y: 10 }] };
  const brush = shapeFromData(input);
  input.points[0].x = 500;
  assert.deepEqual(brush.getBounds(), { x: 0, y: 0, width: 20, height: 10 });
  brush.resize({ x: 10, y: 20, width: 40, height: 30 });
  brush.move(1, 2);
  assert.deepEqual(brush.toData(), { ...input, points: [{ x: 11, y: 22 }, { x: 51, y: 52 }] });
  assert.equal(brush.hitTest({ x: 11, y: 30 }), true);
  assert.equal(shapeFromData({ ...input, points: [] }).hitTest({ x: 0, y: 0 }), false);
  const single = shapeFromData({ ...input, points: [{ x: 1, y: 2 }] });
  single.resize({ x: 20, y: 20, width: 10, height: 10 });
  assert.deepEqual(single.getBounds(), { x: 1, y: 2, width: 0, height: 0 });
});

test('filled text padding matches bounds, resizing changes font with eight pixel minimum', () => {
  const data: TextData = { id: 't', type: 'text', style: { ...style, fontSize: 20, textVariant: 'filled' }, x: 30, y: 40, width: 80, height: 35.2, text: '中文\ntext' };
  const text = shapeFromData(data);
  assert.deepEqual(text.getBounds(), { x: 21, y: 36.4, width: 80, height: 35.2 });
  assert.equal(text.hitTest({ x: 13, y: 40 }), true);
  assert.equal(text.hitTest({ x: 12.99, y: 40 }), false);
  text.resize({ x: 10, y: 20, width: 1, height: 1 });
  const resized = text.toData() as TextData;
  assert.equal(resized.style.fontSize, 8);
  assert.equal(resized.x, 14);
  assert.equal(resized.y, 22);
  assert.equal(resized.style.textVariant, 'filled');
  assert.equal('geometry' in resized, false);
});

test('tag wraps characters including CJK and newlines without losing text', () => {
  const measure = { measureText: (text: string) => ({ width: [...text].length * 10 }) };
  assert.deepEqual(shapes.wrapTagLines('中文换行\nA😀B', measure, 20), ['中文', '换行', 'A😀', 'B']);
  assert.deepEqual(shapes.wrapTagLines('a\n\nb', measure, Infinity), ['a', '', 'b']);
});

test('tag placement flips then clamps, bounds include anchor, resize translates only', () => {
  const data: TagData = { id: 'tag', type: 'tag', style, x: 97.5, y: 40, width: 40, height: 20, anchorX: 90, anchorY: 50, text: '标注', maxWidth: 60 };
  assert.equal(shapes.keepTagInCanvas(data, 100, 100), true);
  assert.deepEqual([data.x, data.y], [42.5, 40]);
  data.width = 200;
  shapes.keepTagInCanvas(data, 100, 100);
  assert.equal(data.x, 4);
  const tag = shapeFromData({ ...data, width: 40, x: 42.5 });
  assert.deepEqual(tag.getBounds(), { x: 42.5, y: 40, width: 50, height: 20 });
  assert.equal(tag.hitTest({ x: 34.5, y: 40 }), true);
  tag.resize({ x: 10, y: 20, width: 500, height: 500 });
  const moved = tag.toData() as TagData;
  assert.deepEqual([moved.x, moved.y, moved.width, moved.height, moved.anchorX, moved.anchorY], [10, 20, 40, 20, 57.5, 30]);
});

test('mosaic rectangle keeps filled hit semantics and brush retains width on resize', () => {
  const rectangle = shapeFromData({ id: 'mr', type: 'mosaic', style, shape: 'rectangle', mode: 'mosaic', strength: 8, x: 20, y: 20, width: -10, height: -10 });
  assert.equal(rectangle.hitTest({ x: 15, y: 15 }), true);
  assert.equal(rectangle.hitTest({ x: 9, y: 15 }), false);
  const brush = shapeFromData({ id: 'mb', type: 'mosaic', style, shape: 'brush', mode: 'blur', strength: 4, brushSize: 10, points: [{ x: 10, y: 10 }, { x: 30, y: 20 }] });
  assert.equal(brush.hitTest({ x: 10, y: 2 }), true);
  assert.equal(brush.hitTest({ x: 10, y: 1.99 }), false);
  brush.resize({ x: 20, y: 30, width: 50, height: 40 });
  assert.deepEqual(brush.toData(), { id: 'mb', type: 'mosaic', style, shape: 'brush', mode: 'blur', strength: 4, brushSize: 10, points: [{ x: 25, y: 35 }, { x: 65, y: 65 }] });
  assert.deepEqual(Object.keys(JSON.parse(JSON.stringify(brush.toData()))).sort(), ['brushSize', 'id', 'mode', 'points', 'shape', 'strength', 'style', 'type']);
});
