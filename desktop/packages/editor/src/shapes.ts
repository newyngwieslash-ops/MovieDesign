import { distanceToSegment, normalizeBounds, pointInBounds, pointInEllipse } from './geometry.ts';
import { createCanvas } from './mosaic.ts';
import type { ArrowData, Bounds, BrushData, DrawHelpers, EllipseData, LineData, MosaicData, Point, RectangleData, ShapeData, ShapeStyle, TagData, TextData } from './types.ts';

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
  abstract draw(ctx: CanvasRenderingContext2D, helpers?: DrawHelpers): void;
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

function pointsBounds(points: readonly Point[]): Bounds {
  if (!points.length) return { x: 0, y: 0, width: 0, height: 0 };
  let minX = points[0].x, maxX = minX, minY = points[0].y, maxY = minY;
  for (const p of points) { minX = Math.min(minX, p.x); maxX = Math.max(maxX, p.x); minY = Math.min(minY, p.y); maxY = Math.max(maxY, p.y); }
  return { x: minX, y: minY, width: maxX - minX, height: maxY - minY };
}

function smoothPath(ctx: CanvasRenderingContext2D, points: readonly Point[], width: number, x = 0, y = 0): void {
  ctx.beginPath();
  if (points.length === 1) {
    ctx.arc(points[0].x - x, points[0].y - y, width / 2, 0, Math.PI * 2); ctx.fill();
  } else {
    ctx.moveTo(points[0].x - x, points[0].y - y);
    for (let i = 1; i < points.length - 1; i++) {
      const p = points[i], next = points[i + 1];
      ctx.quadraticCurveTo(p.x - x, p.y - y, (p.x + next.x) / 2 - x, (p.y + next.y) / 2 - y);
    }
    const last = points[points.length - 1]; ctx.lineTo(last.x - x, last.y - y); ctx.stroke();
  }
}

function hitPath(points: readonly Point[], p: Point, padding: number): boolean {
  if (!points.length) return false;
  if (points.length === 1) return Math.hypot(p.x - points[0].x, p.y - points[0].y) <= padding;
  for (let i = 1; i < points.length; i++) if (distanceToSegment(p, points[i - 1], points[i]) <= padding) return true;
  return false;
}

class BrushShape extends Shape<BrushData> {
  draw(ctx: CanvasRenderingContext2D): void {
    if (!this.data.points.length) return;
    ctx.save(); ctx.lineWidth = this.style.strokeWidth; ctx.strokeStyle = ctx.fillStyle = this.style.stroke;
    ctx.lineCap = ctx.lineJoin = 'round'; smoothPath(ctx, this.data.points, this.style.strokeWidth); ctx.restore();
  }
  hitTest(p: Point): boolean { return hitPath(this.data.points, p, Math.max(8, this.style.strokeWidth)); }
  getBounds(): Bounds { return pointsBounds(this.data.points); }
  move(dx: number, dy: number): void { for (const p of this.data.points) { p.x += dx; p.y += dy; } }
  resize(b: Bounds): void {
    const cur = this.getBounds(); if (!cur.width || !cur.height) return;
    for (const p of this.data.points) { p.x = b.x + (p.x - cur.x) * b.width / cur.width; p.y = b.y + (p.y - cur.y) * b.height / cur.height; }
  }
  addPoint(p: Point): void { this.data.points.push({ ...p }); }
}

class MosaicShape extends Shape<MosaicData> {
  private brushTmp?: HTMLCanvasElement;
  draw(ctx: CanvasRenderingContext2D, helpers?: DrawHelpers): void {
    const source = helpers?.getProcessedSource?.(this.data.mode, this.data.strength); if (!source) return;
    const b = this.getBounds(); if (b.width <= 0 || b.height <= 0) return;
    if (this.data.shape === 'rectangle') {
      ctx.save(); ctx.beginPath(); ctx.rect(b.x, b.y, b.width, b.height); ctx.clip(); ctx.drawImage(source, 0, 0); ctx.restore();
      return;
    }
    const { points, brushSize } = this.data; if (!points.length || brushSize <= 0) return;
    const width = Math.ceil(b.width), height = Math.ceil(b.height);
    if (!this.brushTmp) this.brushTmp = createCanvas(width, height);
    if (this.brushTmp.width < width || this.brushTmp.height < height) {
      this.brushTmp.width = Math.max(width, this.brushTmp.width); this.brushTmp.height = Math.max(height, this.brushTmp.height);
    }
    const mask = this.brushTmp.getContext('2d')!;
    mask.clearRect(0, 0, this.brushTmp.width, this.brushTmp.height); mask.drawImage(source, -b.x, -b.y);
    mask.save(); mask.globalCompositeOperation = 'destination-in'; mask.lineWidth = brushSize; mask.lineCap = mask.lineJoin = 'round';
    mask.strokeStyle = mask.fillStyle = '#000'; smoothPath(mask, points, brushSize, b.x, b.y); mask.restore();
    ctx.drawImage(this.brushTmp, b.x, b.y);
  }
  hitTest(p: Point): boolean {
    return this.data.shape === 'rectangle' ? pointInBounds(p, this.getBounds()) : hitPath(this.data.points, p, Math.max(this.data.brushSize / 2, 8, this.style.strokeWidth));
  }
  getBounds(): Bounds {
    if (this.data.shape === 'rectangle') return normalizeBounds(this.data);
    const b = pointsBounds(this.data.points), half = this.data.brushSize / 2;
    return { x: b.x - half, y: b.y - half, width: b.width + 2 * half, height: b.height + 2 * half };
  }
  move(dx: number, dy: number): void {
    if (this.data.shape === 'rectangle') { this.data.x += dx; this.data.y += dy; }
    else for (const p of this.data.points) { p.x += dx; p.y += dy; }
  }
  resize(b: Bounds): void {
    if (this.data.shape === 'rectangle') { Object.assign(this.data, b); return; }
    const cur = this.getBounds(), size = this.data.brushSize, half = size / 2;
    if (!cur.width || !cur.height) return;
    for (const p of this.data.points) {
      p.x = b.x + half + (p.x - cur.x - half) * Math.max(0.001, b.width - size) / Math.max(0.001, cur.width - size);
      p.y = b.y + half + (p.y - cur.y - half) * Math.max(0.001, b.height - size) / Math.max(0.001, cur.height - size);
    }
  }
  addPoint(p: Point): void { if (this.data.shape === 'brush') this.data.points.push({ ...p }); }
}

const fontFamily = 'system-ui, -apple-system, "PingFang SC", "Microsoft YaHei", sans-serif';
const filledPadding = (size: number) => ({ x: Math.max(4, size * 0.45), y: Math.max(2, size * 0.18) });
const baselineCache = new Map<string, number>();
function firstLineBaseline(font: string): number {
  const cached = baselineCache.get(font); if (cached !== undefined) return cached;
  const wrap = document.createElement('div'), probe = document.createElement('span');
  wrap.style.cssText = `position:absolute;visibility:hidden;left:-9999px;top:0;padding:0;margin:0;border:0;white-space:pre;font:${font};line-height:1.4`;
  probe.style.cssText = 'display:inline-block;width:0;height:0;vertical-align:baseline';
  wrap.append(probe, document.createTextNode('Mg')); document.body.append(wrap);
  const baseline = probe.getBoundingClientRect().bottom - wrap.getBoundingClientRect().top;
  wrap.remove(); baselineCache.set(font, baseline); return baseline;
}
function contrastColor(color: string): string {
  let hex = color.trim().replace(/^#/, ''); if (hex.length === 3) hex = hex.replace(/./g, '$&$&');
  if (!/^[\da-f]{6}$/i.test(hex)) return '#FFFFFF';
  const [r, g, b] = [0, 2, 4].map(i => parseInt(hex.slice(i, i + 2), 16));
  return (r * 299 + g * 587 + b * 114) / 1000 > 165 ? '#1C1C1E' : '#FFFFFF';
}

class TextShape extends Shape<TextData> {
  draw(ctx: CanvasRenderingContext2D): void {
    const { text, x, y, style } = this.data; if (!text) return;
    const size = Math.max(8, style.fontSize ?? 18), font = `${style.fontWeight ?? 400} ${size}px ${style.fontFamily || fontFamily}`;
    const variant = style.textVariant ?? 'plain', lines = text.split('\n'), lineHeight = size * 1.4;
    ctx.save(); ctx.font = font; ctx.textBaseline = 'alphabetic'; ctx.textAlign = 'left'; ctx.lineJoin = 'round'; ctx.miterLimit = 2;
    const width = Math.max(...lines.map(line => ctx.measureText(line).width)), height = lines.length * lineHeight;
    const baseline = firstLineBaseline(font), inverse = style.textOutlineColor ?? contrastColor(style.stroke);
    if (variant === 'filled') {
      const pad = filledPadding(size); this.data.width = Math.max(size, width) + pad.x * 2; this.data.height = height + pad.y * 2;
      ctx.fillStyle = style.stroke; ctx.fillRect(x - pad.x, y - pad.y, this.data.width, this.data.height); ctx.fillStyle = inverse;
    } else {
      this.data.width = width + (variant === 'outlined' ? 0.5 : 0); this.data.height = height + (variant === 'outlined' ? 0.5 : 0);
      ctx.fillStyle = style.stroke;
    }
    if (variant === 'outlined') { ctx.lineWidth = 0.5; ctx.strokeStyle = style.stroke; }
    lines.forEach((line, i) => variant === 'outlined' ? ctx.strokeText(line, x, y + baseline + i * lineHeight) : ctx.fillText(line, x, y + baseline + i * lineHeight));
    ctx.restore();
  }
  hitTest(p: Point): boolean { return pointInBounds(p, this.getBounds(), Math.max(8, this.style.strokeWidth)); }
  getBounds(): Bounds {
    const { x, y, width, height, style } = this.data, size = Math.max(8, style.fontSize ?? 18);
    const pad = style.textVariant === 'filled' ? filledPadding(size) : { x: 0, y: 0 };
    return { x: x - pad.x, y: y - pad.y, width: Math.max(width, size + pad.x * 2), height: Math.max(height, size + pad.y * 2) };
  }
  move(dx: number, dy: number): void { this.data.x += dx; this.data.y += dy; }
  resize(b: Bounds): void {
    const cur = this.getBounds(), size = Math.max(8, Math.round((this.style.fontSize ?? 18) * b.height / cur.height));
    this.data.style.fontSize = size;
    const pad = this.style.textVariant === 'filled' ? filledPadding(size) : { x: 0, y: 0 };
    this.data.x = b.x + pad.x; this.data.y = b.y + pad.y;
  }
}

type TagSide = 'left' | 'right' | 'top' | 'bottom';
const tagOffset = 7.5;
function inferTagSide(b: Bounds, x: number, y: number): TagSide {
  const dx = x - b.x - b.width / 2, dy = y - b.y - b.height / 2;
  return Math.abs(dx) >= Math.abs(dy) ? dx < 0 ? 'left' : 'right' : dy < 0 ? 'top' : 'bottom';
}
export function wrapTagLines(text: string, ctx: { measureText(text: string): { width: number } }, maxWidth: number): string[] {
  if (!Number.isFinite(maxWidth)) return text.split('\n');
  const result: string[] = [];
  for (const source of text.split('\n')) {
    let line = '';
    for (const char of source || ' ') { const next = line + char; if (line && ctx.measureText(next).width > maxWidth) { result.push(line); line = char; } else line = next; }
    result.push(line || ' ');
  }
  return result;
}
export function keepTagInCanvas(data: TagData, width: number, height: number): boolean {
  if (width <= 0 || height <= 0) return false;
  const w = Math.max(1, data.width), h = Math.max(1, data.height), side = inferTagSide({ ...data, width: w, height: h }, data.anchorX, data.anchorY);
  const opposite = { left: 'right', right: 'left', top: 'bottom', bottom: 'top' } as const;
  const place = (s: TagSide): Point => s === 'left' ? { x: data.anchorX - tagOffset - w, y: data.anchorY - h / 2 }
    : s === 'right' ? { x: data.anchorX + tagOffset, y: data.anchorY - h / 2 }
      : s === 'top' ? { x: data.anchorX - w / 2, y: data.anchorY - tagOffset - h } : { x: data.anchorX - w / 2, y: data.anchorY + tagOffset };
  const fits = (p: Point) => p.x >= 4 && p.y >= 4 && p.x + w <= width - 4 && p.y + h <= height - 4;
  let next = place(opposite[side]);
  if (!fits(next)) { const alternate = place(side); next = fits(alternate) ? alternate : { x: Math.max(4, Math.min(next.x, width - w - 4)), y: Math.max(4, Math.min(next.y, height - h - 4)) }; }
  const changed = data.x !== next.x || data.y !== next.y; data.x = next.x; data.y = next.y; return changed;
}

class TagShape extends Shape<TagData> {
  draw(ctx: CanvasRenderingContext2D, helpers?: DrawHelpers): void {
    const { style } = this.data, size = Math.max(8, style.fontSize ?? 13), lineHeight = size * 1.4, radius = Math.max(1, style.tagAnchorRadius ?? 2.5);
    ctx.save(); ctx.font = `${style.fontWeight ?? 500} ${size}px ${style.fontFamily ?? fontFamily}`; ctx.textBaseline = 'middle'; ctx.textAlign = 'center';
    const lines = wrapTagLines(this.data.text || ' ', ctx, this.data.maxWidth ? Math.max(size, this.data.maxWidth - 12) : Infinity);
    this.data.width = Math.max(size, ...lines.map(line => ctx.measureText(line || ' ').width)) + 12; this.data.height = lineHeight * lines.length + 4;
    if (helpers?.canvasWidth !== undefined && helpers.canvasHeight !== undefined) keepTagInCanvas(this.data, helpers.canvasWidth, helpers.canvasHeight);
    const { x, y, width, height } = this.data, side = inferTagSide(this.data, this.data.anchorX, this.data.anchorY), offset = Math.max(tagOffset, radius + 1);
    const cx = x + width / 2, cy = y + height / 2;
    this.data.anchorX = side === 'left' ? x - offset : side === 'right' ? x + width + offset : cx;
    this.data.anchorY = side === 'top' ? y - offset : side === 'bottom' ? y + height + offset : cy;
    const r = Math.min(6, width / 2, height / 2);
    ctx.beginPath(); ctx.moveTo(x + r, y); ctx.lineTo(x + width - r, y); ctx.quadraticCurveTo(x + width, y, x + width, y + r);
    ctx.lineTo(x + width, y + height - r); ctx.quadraticCurveTo(x + width, y + height, x + width - r, y + height);
    ctx.lineTo(x + r, y + height); ctx.quadraticCurveTo(x, y + height, x, y + height - r);
    ctx.lineTo(x, y + r); ctx.quadraticCurveTo(x, y, x + r, y); ctx.closePath();
    if (side === 'left' || side === 'right') {
      const bx = side === 'left' ? x : x + width, tip = side === 'left' ? bx - 4 : bx + 4;
      ctx.moveTo(bx, cy - 4); ctx.lineTo(tip, cy); ctx.lineTo(bx, cy + 4);
    } else {
      const by = side === 'top' ? y : y + height, tip = side === 'top' ? by - 4 : by + 4;
      ctx.moveTo(cx - 4, by); ctx.lineTo(cx, tip); ctx.lineTo(cx + 4, by);
    }
    ctx.closePath(); ctx.fillStyle = style.tagBackground ?? 'rgba(0, 0, 0, 0.6)'; ctx.fill();
    ctx.fillStyle = style.tagTextColor ?? '#FFFFFF'; lines.forEach((line, i) => ctx.fillText(line, cx, y + 2 + lineHeight * (i + 0.5)));
    ctx.beginPath(); ctx.arc(this.data.anchorX, this.data.anchorY, radius, 0, Math.PI * 2); ctx.fillStyle = style.stroke; ctx.fill(); ctx.restore();
  }
  hitTest(p: Point): boolean {
    const padding = Math.max(8, this.style.strokeWidth);
    return pointInBounds(p, this.getBounds(), padding) || Math.hypot(p.x - this.data.anchorX, p.y - this.data.anchorY) <= Math.max(1, this.style.tagAnchorRadius ?? 2.5) + padding;
  }
  getBounds(): Bounds {
    const { x, y, width, height, anchorX, anchorY, style } = this.data, radius = Math.max(1, style.tagAnchorRadius ?? 2.5);
    const minX = Math.min(x, anchorX - radius), minY = Math.min(y, anchorY - radius);
    return { x: minX, y: minY, width: Math.max(x + width, anchorX + radius) - minX, height: Math.max(y + height, anchorY + radius) - minY };
  }
  move(dx: number, dy: number): void { this.data.x += dx; this.data.y += dy; this.data.anchorX += dx; this.data.anchorY += dy; }
  resize(b: Bounds): void { const cur = this.getBounds(); this.move(b.x - cur.x, b.y - cur.y); }
}

export function shapeFromData(data: ShapeData): Shape {
  switch (data.type) {
    case 'rectangle': return new RectangleShape(data);
    case 'ellipse': return new EllipseShape(data);
    case 'line': return new LineShape(data);
    case 'arrow': return new ArrowShape(data);
    case 'brush': return new BrushShape(data);
    case 'text': return new TextShape(data);
    case 'tag': return new TagShape(data);
    case 'mosaic': return new MosaicShape(data);
    default: throw new RangeError('Unknown shape type');
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
