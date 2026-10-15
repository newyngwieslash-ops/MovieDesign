import test from 'node:test';
import assert from 'node:assert/strict';
import { HistoryManager } from '../src/history.ts';

test('undo and redo restore snapshots and a new edit clears redo', () => {
  const history = new HistoryManager<number>(50);
  history.push(0);
  assert.equal(history.undo(100), 0);
  assert.equal(history.redo(0), 100);
  assert.equal(history.undo(100), 0);
  history.push(1);
  assert.equal(history.redo(2), undefined);
});

test('empty snapshots are valid and both stacks isolate nested mutable data', () => {
  const history = new HistoryManager<{ points: { x: number }[]; style: { dash: number[] } }[]>();
  history.push([]);
  const current = [{ points: [{ x: 2 }], style: { dash: [3] } }];
  assert.deepEqual(history.undo(current), []);
  current[0].points[0].x = 99; current[0].style.dash[0] = 99;
  const restored = history.redo([])!;
  assert.deepEqual(restored, [{ points: [{ x: 2 }], style: { dash: [3] } }]);
  history.push(restored); restored[0].style.dash[0] = 42;
  assert.equal(history.undo([])![0].style.dash[0], 3);
});

test('the default stack retains the newest fifty snapshots and clear releases both stacks', () => {
  const history = new HistoryManager<number>();
  for (let i = 0; i < 55; i++) history.push(i);
  for (let i = 54; i >= 5; i--) assert.equal(history.undo(i + 1), i);
  assert.equal(history.undo(5), undefined);
  assert.equal(history.canUndo, false); assert.equal(history.canRedo, true);
  history.clear(); assert.equal(history.canRedo, false);
  assert.throws(() => new HistoryManager(0), RangeError);
});
