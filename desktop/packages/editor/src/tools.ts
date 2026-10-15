import { computeResize, hitHandle, normalizeBounds } from './geometry.ts';
import { shapeFromData } from './shapes.ts';
import type { Shape } from './shapes.ts';
import type { Editor } from './editor.ts';
import type { Bounds, Point, ResizeCorner, ShapeData, ShapeType } from './types.ts';

export type ToolName = 'select' | ShapeType;
type Transform = { shape: Shape; original: ShapeData; bounds: Bounds; corner: ResizeCorner | null };

// Tools own only an in-flight gesture; Editor owns the document and transactions.
export class Tools {
  private editor: Editor;
  private name: ToolName;
  private start: Point | null = null;
  private working: Shape | null = null;
  private transform: Transform | null = null;
  constructor(editor: Editor, name: ToolName) { this.editor = editor; this.name = name; }
  get cursor(): string { return this.name === 'select' ? 'default' : this.name === 'text' ? 'text' : 'crosshair'; }
  down(point: Point): void {
    const editor = this.editor, selected = editor.getSelectedShape();
    const corner = selected ? hitHandle(point, selected.getBounds(), editor.getUiScale()) : null;
    // Creation tools can still manipulate an already selected shape.
    if (this.name === 'select' || corner || selected?.hitTest(point)) {
      const shape = corner || selected?.hitTest(point) ? selected : editor.findShapeAt(point);
      editor.select(shape?.id ?? null);
      if (shape) {
        this.start = { ...point }; this.transform = { shape, original: shape.toData(), bounds: shape.getBounds(), corner };
        editor.beginInteraction(); editor.renderer.setActiveShape(shape);
      }
      return;
    }
    editor.select(null);
    if (this.name === 'text') { editor.requestTextEdit({ kind: 'text', point: { ...point } }); return; }
    this.start = { ...point };
    const style = editor.getStyle(), id = `${this.name}_${crypto.randomUUID()}`;
    let data: ShapeData;
    switch (this.name) {
      case 'line': case 'arrow': data = { id, type: this.name, style, x1: point.x, y1: point.y, x2: point.x, y2: point.y }; break;
      case 'brush': data = { id, type: 'brush', style, points: [{ ...point }] }; break;
      case 'mosaic': {
        const mode = style.mosaicMode ?? 'mosaic', strength = Math.max(1, mode === 'blur' ? style.blurRadius ?? 8 : style.mosaicBlockSize ?? 12);
        data = style.mosaicShape === 'brush'
          ? { id, type: 'mosaic', style, shape: 'brush', mode, strength, brushSize: Math.max(2, style.mosaicBrushSize ?? 24), points: [{ ...point }] }
          : { id, type: 'mosaic', style, shape: 'rectangle', mode, strength, ...point, width: 0, height: 0 };
        break;
      }
      case 'tag': data = { id, type: 'tag', style: { ...style, fontSize: style.fontSize ?? 13 }, x: point.x + 7.5, y: point.y - 13, anchorX: point.x, anchorY: point.y, width: 44, height: 26, text: '' }; break;
      default: data = { id, type: this.name, style, ...point, width: 0, height: 0 };
    }
    this.working = shapeFromData(data); this.showPreview();
  }
  move(point: Point): void {
    if (!this.start) {
      if (this.name === 'select') {
        const selected = this.editor.getSelectedShape(), corner = selected ? hitHandle(point, selected.getBounds(), this.editor.getUiScale()) : null;
        this.editor.setCursor(corner ? corner === 'nw' || corner === 'se' ? 'nwse-resize' : 'nesw-resize' : this.editor.findShapeAt(point) ? 'move' : 'default');
      }
      return;
    }
    if (this.transform) {
      const { shape, original, bounds, corner } = this.transform;
      shape.data = structuredClone(original);
      if (corner) shape.resize(computeResize(bounds, corner, point));
      else shape.move(point.x - this.start.x, point.y - this.start.y);
      this.editor.renderer.drawPreview(shape);
      return;
    }
    const data = this.working?.data; if (!data) return;
    if ('points' in data) data.points.push({ ...point });
    else if (data.type === 'line' || data.type === 'arrow') { data.x2 = point.x; data.y2 = point.y; }
    else if (data.type === 'tag') {
      const dx = point.x - this.start.x, dy = point.y - this.start.y;
      if (Math.hypot(dx, dy) >= 8) {
        if (Math.abs(dx) >= Math.abs(dy)) { data.x = this.start.x + (dx >= 0 ? 7.5 : -7.5 - data.width); data.y = this.start.y - data.height / 2; }
        else { data.x = this.start.x - data.width / 2; data.y = this.start.y + (dy >= 0 ? 7.5 : -7.5 - data.height); }
      }
    } else Object.assign(data, normalizeBounds({ x: this.start.x, y: this.start.y, width: point.x - this.start.x, height: point.y - this.start.y }));
    this.showPreview();
  }
  up(point: Point): void {
    if (!this.start) return;
    if (this.transform) {
      this.move(point); this.editor.renderer.drawPreview(null); this.editor.renderer.setActiveShape(null); this.editor.commitInteraction();
    } else if (this.working) {
      const shape = this.working, data = shape.data;
      if (!('points' in data)) this.move(point);
      this.editor.setPreviewShape(null);
      if (data.type === 'tag') this.editor.requestTextEdit({ kind: 'tag', point: { ...this.start }, data: shape.toData() as Extract<ShapeData, { type: 'tag' }> });
      else {
        const bounds = shape.getBounds();
        const valid = data.type === 'line' || data.type === 'arrow' ? Math.hypot(data.x2 - data.x1, data.y2 - data.y1) >= 4
          : data.type === 'brush' ? data.points.length > 1
            : data.type === 'mosaic' && data.shape === 'brush' ? data.points.length > 0 : bounds.width >= 4 && bounds.height >= 4;
        if (valid) this.editor.addShapeInstance(shape);
      }
    }
    this.start = null; this.working = this.transform = null;
  }
  private showPreview(): void {
    const data = this.working?.data;
    // The original rectangular mosaic tool previews its outline, not processed pixels.
    this.editor.setPreviewShape(data?.type === 'mosaic' && data.shape === 'rectangle'
      ? shapeFromData({ id: 'preview', type: 'rectangle', x: data.x, y: data.y, width: data.width, height: data.height, style: { stroke: '#3370FF', strokeWidth: 1 } }) : this.working);
  }
  deactivate(): void {
    if (this.transform) this.editor.commitInteraction();
    this.start = null; this.working = this.transform = null;
    this.editor.setPreviewShape(null); this.editor.renderer.setActiveShape(null);
  }
}
