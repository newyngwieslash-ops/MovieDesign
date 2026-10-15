import test from 'node:test';
import assert from 'node:assert/strict';
import { Editor } from '../src/editor.ts';
import { shapeFromData } from '../src/shapes.ts';
import type { ShapeData } from '../src/types.ts';

// Only the DOM/Canvas boundary is controlled here. Data/history/tools are real;
// actual Canvas pixels and frame counts are checked in render.ts in WebView2.
class Surface extends EventTarget {
  width = 1; height = 1; style: Record<string, string> = {}; tabIndex = 0;
  parentElement?: Surface; children: Surface[] = []; captured = new Set<number>();
  context = new Proxy({} as Record<string, unknown>, { get: (o, k: string) => o[k] ?? (() => {}), set: (o, k, v) => { o[k as string] = v; return true; } });
  append(child: Surface) { child.parentElement = this; this.children.push(child); }
  remove() { if (this.parentElement) this.parentElement.children = this.parentElement.children.filter(c => c !== this); this.parentElement = undefined; }
  getContext() { return this.context; }
  getBoundingClientRect() { return { left: 0, top: 0, width: parseFloat(this.style.width) || 200, height: parseFloat(this.style.height) || 100 }; }
  setPointerCapture(id: number) { this.captured.add(id); }
  hasPointerCapture(id: number) { return this.captured.has(id); }
  releasePointerCapture(id: number) { this.captured.delete(id); }
  focus() {}
}
const media = { addEventListener() {}, removeEventListener() {} };
const testWindow = Object.assign(new EventTarget(), { devicePixelRatio: 1, matchMedia: () => media });
let frameId = 0; const frames = new Map<number, () => void>();
function flushFrames() { const pending = [...frames.values()]; frames.clear(); pending.forEach(run => run()); }
Object.assign(globalThis, { document: { createElement: () => new Surface() }, window: testWindow, requestAnimationFrame: (run: () => void) => { frames.set(++frameId, run); return frameId; }, cancelAnimationFrame: (id: number) => frames.delete(id) });
const style = { stroke: '#000', strokeWidth: 1, fill: '#fff' };
const rect = (id = 'r'): ShapeData => ({ id, type: 'rectangle', x: 20, y: 20, width: 60, height: 40, style });
function setup(shapes: ShapeData[] = []) {
  const container = new Surface();
  const editor = new Editor(container as unknown as HTMLElement, { width: 200, height: 100, shapes });
  return { editor, main: editor.renderer.main as unknown as Surface, container };
}
function pointer(main: Surface, type: string, x: number, y: number, pointerId = 1, button = 0) {
  const e = new Event(type); Object.assign(e, { clientX: x, clientY: y, pointerId, button }); main.dispatchEvent(e);
}

test('begin is idempotent, one gesture undoes once, no-op commits preserve redo', () => {
  const { editor } = setup([rect()]);
  const shape = editor.getShape('r')!;
  editor.beginInteraction(); shape.move(10, 0); editor.beginInteraction(); shape.move(20, 0); editor.commitInteraction();
  assert.equal(editor.getState().shapes[0].type === 'rectangle' && editor.getState().shapes[0].x, 50);
  editor.undo(); assert.equal((editor.getShape('r')!.data as any).x, 20); assert.equal(editor.getState().canUndo, false);
  editor.beginInteraction(); editor.commitInteraction(); editor.commitInteraction();
  assert.equal(editor.getState().canUndo, false); assert.equal(editor.getState().canRedo, true);
  editor.redo(); assert.equal((editor.getShape('r')!.data as any).x, 50);
  editor.destroy();
});

test('reordering data keys without changing their values does not create history', () => {
  const { editor } = setup([rect()]); const shape = editor.getShape('r')!;
  editor.beginInteraction();
  shape.data = { style: { fill: '#fff', strokeWidth: 1, stroke: '#000' }, height: 40, width: 60, y: 20, x: 20, type: 'rectangle', id: 'r' };
  editor.commitInteraction(); assert.equal(editor.getState().canUndo, false); editor.destroy();
});

test('snapshots deep copy points/style, reuse same-type instances and replace obsolete fields', () => {
  const { editor } = setup([{ id: 'b', type: 'brush', points: [{ x: 1, y: 2 }], style: { ...style, selectionLineDash: [4] } }]);
  const shape = editor.getShape('b')!;
  editor.beginInteraction(); (shape.data as any).points[0].x = 50; shape.data.style.selectionLineDash![0] = 9; shape.data.style.fontSize = 99; editor.commitInteraction();
  editor.undo(); assert.equal(editor.getShape('b'), shape);
  assert.deepEqual(shape.data, { id: 'b', type: 'brush', points: [{ x: 1, y: 2 }], style: { ...style, selectionLineDash: [4] } });
  const state = editor.getState(); (state.shapes[0] as any).points[0].x = 999; state.style.stroke = 'bad';
  assert.equal((shape.data as any).points[0].x, 1); assert.equal(editor.getStyle().stroke, '#FF3B30');
  editor.redo(); assert.equal((shape.data as any).points[0].x, 50); assert.equal(shape.style.fontSize, 99);
  editor.beginInteraction(); shape.data = { ...rect('b') }; editor.commitInteraction(); editor.undo();
  assert.notEqual(editor.getShape('b'), shape); assert.equal(editor.getShape('b')!.type, 'brush'); editor.destroy();
});

test('cancelInteraction clears the transaction without reverting mutations or creating history', () => {
  const { editor } = setup([rect()]); editor.beginInteraction(); editor.getShape('r')!.move(10, 5); editor.cancelInteraction(); editor.commitInteraction();
  assert.deepEqual(editor.getShape('r')!.getBounds(), { x: 30, y: 25, width: 60, height: 40 });
  assert.equal(editor.getState().canUndo, false); editor.destroy();
});

test('duplicate ids are rejected atomically at construction and insertion', () => {
  assert.throws(() => setup([rect(), rect()]), /Duplicate/);
  const { editor } = setup([rect()]); assert.throws(() => editor.addShape(rect()), /Duplicate/);
  assert.equal(editor.getState().shapes.length, 1); assert.equal(editor.getState().canUndo, false); editor.destroy();
});

test('pointer capture uses original delta, ignores other pointers and pointercancel commits', () => {
  const { editor, main } = setup([rect()]);
  pointer(main, 'pointerdown', 50, 40); assert.equal(main.captured.has(1), true);
  pointer(main, 'pointermove', 60, 45); pointer(main, 'pointermove', 70, 50); pointer(main, 'pointermove', 190, 90, 2);
  assert.deepEqual(editor.getShape('r')!.getBounds(), { x: 40, y: 30, width: 60, height: 40 });
  pointer(main, 'pointercancel', 70, 50); assert.equal(main.captured.size, 0);
  editor.undo(); assert.deepEqual(editor.getShape('r')!.getBounds(), { x: 20, y: 20, width: 60, height: 40 }); editor.destroy();
});

test('each corner resizes from the original data, including crossing the opposite corner', () => {
  for (const [x, y, tx, ty, expected] of [[20, 20, 90, 70, { x: 80, y: 60, width: 10, height: 10 }], [80, 20, 100, 10, { x: 20, y: 10, width: 80, height: 50 }], [20, 60, 10, 90, { x: 10, y: 20, width: 70, height: 70 }], [80, 60, 100, 90, { x: 20, y: 20, width: 80, height: 70 }]] as const) {
    const { editor, main } = setup([rect()]); editor.select('r');
    pointer(main, 'pointerdown', x, y); pointer(main, 'pointermove', tx, ty); pointer(main, 'pointerup', tx, ty);
    assert.deepEqual(editor.getShape('r')!.getBounds(), expected); editor.undo(); assert.deepEqual(editor.getShape('r')!.getBounds(), { x: 20, y: 20, width: 60, height: 40 }); editor.destroy();
  }
});

test('new shape validity keeps horizontal lines, rejects small boxes and distinguishes the two brushes', () => {
  const { editor, main } = setup();
  editor.setTool('rectangle'); pointer(main, 'pointerdown', 30, 30); pointer(main, 'pointerup', 32, 80); assert.equal(editor.getState().shapes.length, 0);
  pointer(main, 'pointerdown', 50, 60); pointer(main, 'pointerup', 10, 10); assert.deepEqual(editor.getState().shapes[0], { id: editor.getState().shapes[0].id, type: 'rectangle', x: 10, y: 10, width: 40, height: 50, style: editor.getStyle() });
  editor.setTool('line'); pointer(main, 'pointerdown', 100, 20); pointer(main, 'pointerup', 140, 20); assert.equal(editor.getState().shapes.length, 2);
  editor.setTool('brush'); pointer(main, 'pointerdown', 160, 40); pointer(main, 'pointerup', 160, 40); assert.equal(editor.getState().shapes.length, 2);
  editor.setStyle({ mosaicShape: 'brush' }); editor.setTool('mosaic'); pointer(main, 'pointerdown', 180, 80); pointer(main, 'pointerup', 180, 80); assert.equal(editor.getState().shapes.length, 3);
  editor.undo(); assert.equal(editor.getState().shapes.length, 2); editor.destroy();
});

test('change and selection events unsubscribe; text/tag request editing without committing empty text', () => {
  const { editor, main } = setup([rect()]); let changes = 0; const selected: (string | null)[] = []; const requests: any[] = [];
  const stop = editor.on('change', () => changes++); const selectHandler = (id: string | null) => selected.push(id);
  editor.on('selection', selectHandler); editor.on('text-edit', request => requests.push(request));
  editor.select('r'); editor.deleteShapes(['r']); assert.deepEqual(selected, ['r', null]); assert.equal(changes, 2);
  stop(); editor.off('selection', selectHandler); editor.undo(); assert.equal(changes, 2);
  editor.setTool('text'); pointer(main, 'pointerdown', 130, 20); pointer(main, 'pointerup', 130, 20);
  editor.setTool('tag'); pointer(main, 'pointerdown', 130, 50); pointer(main, 'pointermove', 100, 50); pointer(main, 'pointerup', 100, 50);
  assert.equal(requests.length, 2); assert.equal(requests[0].kind, 'text'); assert.deepEqual(requests[0].point, { x: 130, y: 20 });
  assert.equal(requests[1].kind, 'tag'); assert.equal(requests[1].data.x, 78.5);
  assert.equal(editor.getState().shapes.length, 1); assert.equal(editor.getState().canUndo, false); editor.destroy();
});

test('destroy releases captured pointer, DOM, listeners and history, and disables input', () => {
  const { editor, main, container } = setup([rect()]); pointer(main, 'pointerdown', 50, 40); editor.destroy(); editor.destroy();
  assert.equal(main.captured.size, 0); assert.equal(container.children.length, 0);
  pointer(main, 'pointermove', 90, 80); pointer(main, 'pointerup', 90, 80);
  assert.equal(editor.getState().shapes.length, 0); assert.equal(editor.getState().canUndo, false);
});

test('tool and global style changes notify consumers, and creation tool changes clear selection', () => {
  const { editor } = setup([rect()]); const states: string[] = [];
  editor.on('change', state => states.push(`${state.tool}:${state.style.stroke}`));
  editor.setStyle({ stroke: '#abc' }); editor.setTool('ellipse');
  assert.deepEqual(states, ['select:#abc', 'ellipse:#abc']);
  editor.select('r'); editor.setTool('rectangle'); assert.equal(editor.getState().selectedId, null); editor.destroy();
});

test('unrelated mosaic style edits preserve existing processing strength and brush size', () => {
  const { editor } = setup([{ id: 'm', type: 'mosaic', shape: 'brush', mode: 'blur', strength: 17, brushSize: 7, points: [{ x: 20, y: 20 }], style }]);
  editor.select('m'); editor.setStyle({ stroke: '#abc' });
  assert.equal((editor.getShape('m')!.data as any).strength, 17); assert.equal((editor.getShape('m')!.data as any).brushSize, 7);
  editor.setStyle({ blurRadius: 4, mosaicBrushSize: 12 }); assert.equal((editor.getShape('m')!.data as any).strength, 4);
  editor.undo(); assert.equal((editor.getShape('m')!.data as any).strength, 17); editor.destroy();
});

test('deletion during drag releases the pointer and ends the preview before the deletion transaction', () => {
  const { editor, main } = setup([rect()]); pointer(main, 'pointerdown', 50, 40); pointer(main, 'pointermove', 60, 45);
  editor.deleteShapes(['r']); assert.equal(main.captured.size, 0); assert.equal(editor.getState().shapes.length, 0);
  pointer(main, 'pointermove', 100, 90); pointer(main, 'pointerup', 100, 90);
  editor.undo(); assert.deepEqual(editor.getShape('r')!.getBounds(), { x: 30, y: 25, width: 60, height: 40 });
  editor.undo(); assert.deepEqual(editor.getShape('r')!.getBounds(), { x: 20, y: 20, width: 60, height: 40 }); editor.destroy();
});

test('deletion inside an explicit transaction preserves its original snapshot', () => {
  const { editor } = setup([rect()]); editor.beginInteraction();
  editor.addShapeInstanceWithoutHistory(shapeFromData(rect('second'))); editor.deleteShapes(['r']);
  assert.deepEqual(editor.getState().shapes.map(shape => shape.id), ['second']); editor.undo();
  assert.deepEqual(editor.getState().shapes.map(shape => shape.id), ['r']); editor.destroy();
});

test('undo clearing selection emits exactly one final change snapshot', () => {
  const { editor } = setup(); editor.addShape(rect()); editor.select('r'); const changes: number[] = [];
  editor.on('change', state => changes.push(state.shapes.length)); editor.undo();
  assert.deepEqual(changes, [0]); assert.equal(editor.getState().selectedId, null); editor.destroy();
});

test('preloaded image inputs also require anonymous CORS and valid natural dimensions', async () => {
  const { editor } = setup();
  await assert.rejects(editor.loadImage({ naturalWidth: 100, naturalHeight: 50, crossOrigin: null } as HTMLImageElement), /anonymous/);
  await assert.rejects(editor.loadImage({ naturalWidth: 0, naturalHeight: 0, crossOrigin: 'anonymous' } as HTMLImageElement), /natural dimensions/);
  assert.equal(editor.renderer.main.style.height, '100px'); editor.destroy();
});

test('DPR is sampled on the next frame even when resize precedes the property update', () => {
  const { editor, main } = setup();
  flushFrames(); testWindow.dispatchEvent(new Event('resize')); testWindow.devicePixelRatio = 2; flushFrames();
  assert.equal(main.width, 400); assert.equal(main.height, 200); assert.equal(main.style.width, '200px');
  editor.destroy(); testWindow.devicePixelRatio = 1; testWindow.dispatchEvent(new Event('resize'));
  assert.equal(main.width, 0); assert.equal(main.height, 0);
});
