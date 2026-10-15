import { createCanvas, MosaicCache } from './mosaic.ts';
import { drawSelectionHandles } from './shapes.ts';
import type { Shape } from './shapes.ts';
import type { DrawHelpers } from './types.ts';

export class Renderer {
  readonly main = createCanvas(1, 1);
  readonly bgLayer = createCanvas(1, 1);
  readonly shapeLayer = createCanvas(1, 1);
  readonly previewLayer = createCanvas(1, 1);
  private bgSource = createCanvas(1, 1);
  private cache: MosaicCache | undefined = new MosaicCache();
  private background?: HTMLImageElement;
  private shapes: readonly Shape[] = [];
  private preview: Shape | null = null;
  private active: Shape | null = null;
  private selected: Shape | null = null;
  private width = 1;
  private height = 1;
  private dpr = window.devicePixelRatio || 1;
  private uiScale = 1;
  private sourceRevision = 0;
  private frame: number | null = null;
  private dirtyShapes = true;
  private dirtyPreview = true;
  private destroyed = false;
  private media?: MediaQueryList;
  private helpers: DrawHelpers = { getProcessedSource: (mode, strength) => this.cache?.getProcessedSource(mode, strength) };

  constructor(width: number, height: number) {
    this.main.style.display = 'block'; this.main.style.touchAction = 'none'; this.main.tabIndex = 0;
    this.setSize(width, height); this.watchDpr(); window.addEventListener('resize', this.onDpr);
  }
  get pixelScale(): number { return Math.max(this.dpr * Math.max(1, Math.ceil(this.uiScale)), (this.background?.naturalWidth ?? 0) / this.width); }
  getUiScale(): number { return this.uiScale; }
  setUiScale(scale: number): void {
    if (!Number.isFinite(scale) || scale <= 0) throw new RangeError('Invalid UI scale');
    const tier = Math.ceil(this.uiScale); this.uiScale = scale;
    if (Math.ceil(scale) !== tier) this.applyDimensions(); else this.invalidate();
  }
  setSize(width: number, height: number): void {
    if (![width, height].every(Number.isFinite) || width <= 0 || height <= 0) throw new RangeError('Invalid editor dimensions');
    this.width = width; this.height = height; this.applyDimensions();
  }
  private applyDimensions(): void {
    if (this.destroyed) return;
    const scale = this.pixelScale;
    for (const canvas of [this.main, this.bgLayer, this.shapeLayer, this.previewLayer]) {
      canvas.width = Math.max(1, Math.round(this.width * scale)); canvas.height = Math.max(1, Math.round(this.height * scale));
      canvas.getContext('2d')!.setTransform(scale, 0, 0, scale, 0, 0);
    }
    this.main.style.width = `${this.width}px`; this.main.style.height = `${this.height}px`;
    this.bgSource.width = Math.max(1, Math.round(this.width)); this.bgSource.height = Math.max(1, Math.round(this.height));
    this.helpers.canvasWidth = this.width; this.helpers.canvasHeight = this.height;
    this.cache?.setSize(this.width, this.height, this.dpr); this.repaintBackground();
    this.dirtyShapes = this.dirtyPreview = true; this.invalidate();
  }
  private watchDpr = (): void => {
    this.media?.removeEventListener('change', this.onDpr);
    this.media = window.matchMedia(`(resolution: ${this.dpr}dppx)`);
    this.media.addEventListener('change', this.onDpr);
  };
  private onDpr = (): void => { this.invalidate(); };
  drawBackground(image: HTMLImageElement): void { this.background = image; this.applyDimensions(); }
  private repaintBackground(): void {
    const bg = this.bgLayer.getContext('2d')!, source = this.bgSource.getContext('2d')!;
    bg.clearRect(0, 0, this.width, this.height); source.clearRect(0, 0, this.bgSource.width, this.bgSource.height);
    if (this.background) {
      bg.drawImage(this.background, 0, 0, this.width, this.height); source.drawImage(this.background, 0, 0, this.bgSource.width, this.bgSource.height);
    }
    this.cache?.setSource(this.bgSource, ++this.sourceRevision);
  }
  drawShapes(shapes: readonly Shape[]): void { this.shapes = shapes; this.dirtyShapes = true; this.invalidate(); }
  setActiveShape(shape: Shape | null): void {
    if (shape === this.active) return;
    this.active = shape; this.dirtyShapes = this.dirtyPreview = true; this.invalidate();
  }
  drawPreview(shape: Shape | null): void { this.preview = shape; this.dirtyPreview = true; this.invalidate(); }
  setSelected(shape: Shape | null): void { this.selected = shape; this.invalidate(); }
  invalidate(): void {
    if (this.destroyed || this.frame !== null) return;
    this.frame = requestAnimationFrame(() => { this.frame = null; this.composite(); });
  }
  private composite(): void {
    if (this.destroyed) return;
    // WebView can deliver resize before devicePixelRatio updates; sample at paint.
    const dpr = window.devicePixelRatio || 1;
    if (dpr !== this.dpr) { this.dpr = dpr; this.applyDimensions(); this.watchDpr(); }
    if (this.dirtyShapes) {
      const ctx = this.shapeLayer.getContext('2d')!; ctx.clearRect(0, 0, this.width, this.height);
      for (const shape of this.shapes) if (shape !== this.active) shape.draw(ctx, this.helpers);
      this.dirtyShapes = false;
    }
    if (this.dirtyPreview) {
      const ctx = this.previewLayer.getContext('2d')!; ctx.clearRect(0, 0, this.width, this.height);
      (this.preview ?? this.active)?.draw(ctx, this.helpers); this.dirtyPreview = false;
    }
    const ctx = this.main.getContext('2d')!; ctx.clearRect(0, 0, this.width, this.height);
    for (const layer of [this.bgLayer, this.shapeLayer, this.previewLayer]) ctx.drawImage(layer, 0, 0, this.width, this.height);
    if (this.selected) {
      this.selected.drawSelection(ctx, this.uiScale);
      drawSelectionHandles(ctx, this.selected.getBounds(), this.uiScale, this.selected.style);
    }
  }
  exportComposed(): HTMLCanvasElement {
    if (this.destroyed) throw new Error('Editor is destroyed');
    const width = this.background?.naturalWidth || Math.round(this.width), height = this.background?.naturalHeight || Math.round(this.height);
    const canvas = createCanvas(width, height), ctx = canvas.getContext('2d')!;
    if (this.background) ctx.drawImage(this.background, 0, 0, width, height);
    ctx.scale(width / this.width, height / this.height);
    for (const shape of this.shapes) shape.draw(ctx, this.helpers);
    return canvas;
  }
  destroy(): void {
    if (this.destroyed) return;
    this.destroyed = true;
    if (this.frame !== null) cancelAnimationFrame(this.frame);
    this.frame = null; this.media?.removeEventListener('change', this.onDpr); this.media = undefined;
    window.removeEventListener('resize', this.onDpr);
    this.cache?.clear(); this.cache = undefined; this.background = undefined; this.shapes = [];
    this.active = this.preview = this.selected = null;
    for (const canvas of [this.main, this.bgLayer, this.shapeLayer, this.previewLayer, this.bgSource]) canvas.width = canvas.height = 0;
    this.main.remove();
  }
}
