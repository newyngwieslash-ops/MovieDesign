import test from 'node:test';
import assert from 'node:assert/strict';
import { annotationShapeIds, collectAnnotations } from './annotations.ts';
import type { ShapeData } from '../../../packages/editor/src/types.ts';

const records = [{ id: 'a', assetId: 'actual-image', rectangleId: 'r', tagId: 't' }];
const style = { stroke: '#f00', strokeWidth: 2 };
const shapes: ShapeData[] = [
  { id: 'r', type: 'rectangle', x: 10, y: 12, width: 30, height: 20, style },
  { id: 't', type: 'tag', x: 40, y: 12, width: 20, height: 10, anchorX: 40, anchorY: 12, text: '修改背景', style },
];
test('either paired ID expands deletion; absent pairs stay absent without losing identity', () => {
  for (const id of ['r', 't']) assert.deepEqual(annotationShapeIds(records, [id]), new Set(['r', 't']));
  assert.deepEqual(annotationShapeIds(records, ['other']), new Set(['other']));
  assert.deepEqual(collectAnnotations(records, [], { width: 96, height: 96 }, { width: 192, height: 192 }), []);
  const restored = collectAnnotations(records, shapes, { width: 96, height: 96 }, { width: 192, height: 192 });
  assert.deepEqual(restored, [{ ...records[0], comment: '修改背景', bounds: { x: 20, y: 24, width: 60, height: 40 } }]);
  const changed = structuredClone(shapes); if (changed[1].type === 'tag') changed[1].text = '新说明';
  assert.equal(collectAnnotations(records, changed, { width: 96, height: 96 }, { width: 192, height: 192 })[0].comment, '新说明');
  assert.equal(collectAnnotations(records, shapes, { width: 96, height: 96 }, { width: 192, height: 192 })[0].comment, '修改背景');
});
