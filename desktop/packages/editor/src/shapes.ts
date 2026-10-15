import { distanceToSegment, normalizeBounds, pointInBounds, pointInEllipse } from './geometry.ts';
import type { ArrowData, Bounds, EllipseData, LineData, Point, RectangleData, ShapeData, ShapeStyle } from './types.ts';

export function cloneShapeData<T extends ShapeData>(data: T): T {
  return structuredClone(data);
}

export abstract class Shape<T extends ShapeData = ShapeData> {
  data: T;
  constructor(data: T) { this.data = cloneShapeData(data); }
  get id() { return this.data.id; }
  get type() { return this.data.type; }
  get style() { return this.data.style; }
  toData(): T { return cloneShapeData(this.data); }
  abstract draw(ctx: CanvasRenderingContext2D): void;
  abstract hitTest(point: Point): boolean;
  abstract getBounds(): Bounds;
  abstract move(dx: number, dy: number): void;
  abstract resize(bounds: Bounds): void;

  drawSelection(ctx: CanvasRenderingContext2D, uiScale = 1): void {
    const b = this.getBounds(), inv = uiScale > 0 ? 1 / uiScale : 1;
    ctx.save();
    ctx.strokeStyle = this.style.selectionStroke ?? '#3370FF';
    ctx.lineWidth = inv;
    ctx.setLineDash((this.style.selectionLineDash ?? [4, 4]).map(value => value * inv));
    ctx.strokeRect(b.x - 2 * inv, b.y - 2 * inv, b.width + 4 * inv, b.height + 4 * inv);
    ctx.restore();
  }
}

class RectangleShape<T extends RectangleData | EllipseData = RectangleData> extends Shape<T> {
  draw(ctx: CanvasRenderingContext2D): void {
    const { x, y, width, height, style } = this.data;
    if (width === 0 || height === 0) return;
    ctx.save();
    ctx.lineWidth = style.strokeWidth;
    ctx.strokeStyle = style.stroke;
    if (style.fill) { ctx.fillStyle = style.fill; ctx.fillRect(x, y, width, height); }
    ctx.strokeRect(x, y, width, height);
    ctx.restore();
  }
  hitTest(p: Point): boolean {
    const b = this.getBounds(), padding = Math.max(8, this.style.strokeWidth);
    return pointInBounds(p, b, padding) && (Boolean(this.style.fill) || !pointInBounds(p, b, -padding));
  }
  getBounds(): Bounds { return normalizeBounds(this.data); }
  move(dx: number, dy: number): void { this.data.x += dx; this.data.y += dy; }
  resize(b: Bounds): void {
    this.data.x = b.x; this.data.y = b.y;
    this.data.width = b.width; this.data.height = b.height;
  }
}

class EllipseShape extends RectangleShape<EllipseData> {
  draw(ctx: CanvasRenderingContext2D): void {
    const { x, y, width, height, style } = this.data;
    if (width === 0 || height === 0) return;
    ctx.save();
    ctx.lineWidth = style.strokeWidth;
    ctx.strokeStyle = style.stroke;
    ctx.beginPath();
    ctx.ellipse(x + width / 2, y + height / 2, Math.abs(width / 2), Math.abs(height / 2), 0, 0, Math.PI * 2);
    if (style.fill) { ctx.fillStyle = style.fill; ctx.fill(); }
    ctx.stroke();
    ctx.restore();
  }
  hitTest(p: Point): boolean {
    const b = this.getBounds();
    if (this.style.fill) return pointInEllipse(p, b);
    if (b.width === 0 || b.height === 0) return false;
    const pad = Math.max(8, this.style.strokeWidth);
    return pointInEllipse(p, { x: b.x - pad, y: b.y - pad, width: b.width + 2 * pad, height: b.height + 2 * pad })
      && !pointInEllipse(p, { x: b.x + pad, y: b.y + pad, width: b.width - 2 * pad, height: b.height - 2 * pad });
  }
}

class LineShape<T extends LineData | ArrowData = LineData> extends Shape<T> {
  draw(ctx: CanvasRenderingContext2D): void {
    const { x1, y1, x2, y2, style } = this.data;
    ctx.save();
    ctx.lineWidth = style.strokeWidth;
    ctx.strokeStyle = style.stroke;
    ctx.lineCap = 'round';
    ctx.beginPath();
    ctx.moveTo(x1, y1);
    ctx.lineTo(x2, y2);
    ctx.stroke();
    ctx.restore();
  }
  hitTest(p: Point): boolean {
    const { x1, y1, x2, y2 } = this.data;
    return distanceToSegment(p, { x: x1, y: y1 }, { x: x2, y: y2 }) <= Math.max(8, this.style.strokeWidth);
  }
  getBounds(): Bounds {
    const { x1, y1, x2, y2 } = this.data;
    return normalizeBounds({ x: x1, y: y1, width: x2 - x1, height: y2 - y1 });
  }
  move(dx: number, dy: number): void {
    this.data.x1 += dx; this.data.y1 += dy;
    this.data.x2 += dx; this.data.y2 += dy;
  }
  resize(b: Bounds): void {
    const cur = this.getBounds();
    if ((cur.width === 0 && cur.height === 0) || (this.type === 'line' && (cur.width === 0 || cur.height === 0))) {
      this.data.x1 = b.x; this.data.y1 = b.y;
      this.data.x2 = b.x + b.width; this.data.y2 = b.y + b.height;
      return;
    }
    const sx = cur.width === 0 ? 1 : b.width / cur.width;
    const sy = cur.height === 0 ? 1 : b.height / cur.height;
    this.data.x1 = b.x + (this.data.x1 - cur.x) * sx;
    this.data.y1 = b.y + (this.data.y1 - cur.y) * sy;
    this.data.x2 = b.x + (this.data.x2 - cur.x) * sx;
    this.data.y2 = b.y + (this.data.y2 - cur.y) * sy;
  }
}

class ArrowShape extends LineShape<ArrowData> {
  draw(ctx: CanvasRenderingContext2D): void {
    const { x1, y1, x2, y2, style } = this.data;
    const dx = x2 - x1, dy = y2 - y1, len = Math.hypot(dx, dy);
    if (len === 0) return;
    const headLen = Math.max(12, 4 * style.strokeWidth), headWidth = Math.max(8, 3 * style.strokeWidth);
    const ux = dx / len, uy = dy / len;
    const baseX = x2 - ux * headLen, baseY = y2 - uy * headLen;
    ctx.save();
    ctx.lineWidth = style.strokeWidth;
    ctx.strokeStyle = style.stroke;
    ctx.fillStyle = style.stroke;
    ctx.lineCap = 'round';
    ctx.lineJoin = 'round';
    ctx.beginPath();
    ctx.moveTo(x1, y1);
    ctx.lineTo(x2 - ux * headLen * 0.5, y2 - uy * headLen * 0.5);
    ctx.stroke();
    ctx.beginPath();
    ctx.moveTo(x2, y2);
    ctx.lineTo(baseX - uy * headWidth * 0.5, baseY + ux * headWidth * 0.5);
    ctx.lineTo(baseX + uy * headWidth * 0.5, baseY - ux * headWidth * 0.5);
    ctx.closePath();
    ctx.fill();
    ctx.restore();
  }
}

export function shapeFromData(data: ShapeData): Shape {
  switch (data.type) {
    case 'rectangle': return new RectangleShape(data);
    case 'ellipse': return new EllipseShape(data);
    case 'line': return new LineShape(data);
    case 'arrow': return new ArrowShape(data);
    default: throw new RangeError(`Shape type not implemented: ${data.type}`);
  }
}

export function findShapeAt(shapes: readonly Shape[], point: Point): Shape | undefined {
  for (let i = shapes.length - 1; i >= 0; i--) if (shapes[i].hitTest(point)) return shapes[i];
  return undefined;
}

export function drawSelectionHandles(ctx: CanvasRenderingContext2D, b: Bounds, uiScale = 1, style?: ShapeStyle): void {
  const size = uiScale > 0 ? 8 / uiScale : 8;
  ctx.save();
  ctx.fillStyle = style?.selectionHandleFill ?? '#FFFFFF';
  ctx.strokeStyle = style?.selectionHandleStroke ?? style?.selectionStroke ?? '#3370FF';
  ctx.lineWidth = uiScale > 0 ? 1.5 / uiScale : 1.5;
  for (const [x, y] of [[b.x, b.y], [b.x + b.width, b.y], [b.x, b.y + b.height], [b.x + b.width, b.y + b.height]]) {
    ctx.beginPath();
    ctx.rect(x - size / 2, y - size / 2, size, size);
    ctx.fill();
    ctx.stroke();
  }
  ctx.restore();
}
