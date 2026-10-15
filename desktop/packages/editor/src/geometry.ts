import type { Bounds, Point, ResizeCorner } from './types.ts';

export function normalizeBounds(b: Bounds): Bounds {
  return { x: Math.min(b.x, b.x + b.width), y: Math.min(b.y, b.y + b.height), width: Math.abs(b.width), height: Math.abs(b.height) };
}

export function distanceToSegment(p: Point, a: Point, b: Point): number {
  const dx = b.x - a.x, dy = b.y - a.y, length = dx * dx + dy * dy;
  if (length === 0) return Math.hypot(p.x - a.x, p.y - a.y);
  const t = Math.max(0, Math.min(1, ((p.x - a.x) * dx + (p.y - a.y) * dy) / length));
  return Math.hypot(p.x - a.x - t * dx, p.y - a.y - t * dy);
}

export function pointInBounds(p: Point, b: Bounds, padding = 0): boolean {
  return p.x >= b.x - padding && p.x <= b.x + b.width + padding && p.y >= b.y - padding && p.y <= b.y + b.height + padding;
}

export function pointInEllipse(p: Point, b: Bounds): boolean {
  if (b.width <= 0 || b.height <= 0) return false;
  const nx = (p.x - b.x - b.width / 2) / (b.width / 2);
  const ny = (p.y - b.y - b.height / 2) / (b.height / 2);
  return nx * nx + ny * ny <= 1;
}

export function computeResize(orig: Bounds, corner: ResizeCorner, p: Point): Bounds {
  const left = corner === 'nw' || corner === 'sw' ? p.x : orig.x;
  const top = corner === 'nw' || corner === 'ne' ? p.y : orig.y;
  const right = corner === 'ne' || corner === 'se' ? p.x : orig.x + orig.width;
  const bottom = corner === 'sw' || corner === 'se' ? p.y : orig.y + orig.height;
  return normalizeBounds({ x: left, y: top, width: right - left, height: bottom - top });
}

export function hitHandle(p: Point, b: Bounds, uiScale = 1): ResizeCorner | null {
  const half = uiScale > 0 ? 8 / uiScale : 8;
  for (const corner of ['nw', 'ne', 'sw', 'se'] as const) {
    const x = b.x + (corner === 'ne' || corner === 'se' ? b.width : 0);
    const y = b.y + (corner === 'sw' || corner === 'se' ? b.height : 0);
    if (Math.abs(p.x - x) <= half && Math.abs(p.y - y) <= half) return corner;
  }
  return null;
}
