export class LRU<T> {
  private values = new Map<string, T>();
  get(key: string): T | undefined {
    const value = this.values.get(key);
    if (value !== undefined) { this.values.delete(key); this.values.set(key, value); }
    return value;
  }
  put(key: string, value: T): void {
    this.values.delete(key); this.values.set(key, value);
    if (this.values.size > 8) this.values.delete(this.values.keys().next().value!);
  }
  clear(): void { this.values.clear(); }
}

export type MosaicSource = HTMLCanvasElement | HTMLImageElement | ImageBitmap | OffscreenCanvas;

export function createCanvas(width: number, height: number): HTMLCanvasElement {
  const canvas = document.createElement('canvas');
  canvas.width = Math.max(1, Math.ceil(width)); canvas.height = Math.max(1, Math.ceil(height));
  return canvas;
}

function generateBlur(source: HTMLCanvasElement, radius: number): HTMLCanvasElement {
  const { width, height } = source, r = Math.max(1, Math.round(radius)), pad = 2 * r;
  const tmp = createCanvas(width + 2 * pad, height + 2 * pad), ctx = tmp.getContext('2d')!;
  ctx.drawImage(source, pad, pad);
  ctx.drawImage(source, 0, 0, width, 1, pad, 0, width, pad);
  ctx.drawImage(source, 0, height - 1, width, 1, pad, height + pad, width, pad);
  ctx.drawImage(source, 0, 0, 1, height, 0, pad, pad, height);
  ctx.drawImage(source, width - 1, 0, 1, height, width + pad, pad, pad, height);
  ctx.drawImage(source, 0, 0, 1, 1, 0, 0, pad, pad);
  ctx.drawImage(source, width - 1, 0, 1, 1, width + pad, 0, pad, pad);
  ctx.drawImage(source, 0, height - 1, 1, 1, 0, height + pad, pad, pad);
  ctx.drawImage(source, width - 1, height - 1, 1, 1, width + pad, height + pad, pad, pad);
  const out = createCanvas(width, height), outCtx = out.getContext('2d')!;
  outCtx.filter = `blur(${r}px)`; outCtx.drawImage(tmp, -pad, -pad); outCtx.filter = 'none';
  return out;
}

function generateMosaic(source: HTMLCanvasElement, strength: number): HTMLCanvasElement {
  const { width, height } = source, block = Math.max(2, Math.round(strength));
  const small = createCanvas(Math.ceil(width / block), Math.ceil(height / block)), ctx = small.getContext('2d')!;
  ctx.imageSmoothingEnabled = true; ctx.imageSmoothingQuality = 'high';
  ctx.drawImage(source, 0, 0, small.width, small.height);
  const out = createCanvas(width, height), outCtx = out.getContext('2d')!;
  outCtx.imageSmoothingEnabled = false; outCtx.drawImage(small, 0, 0, width, height);
  return out;
}

export class MosaicCache extends LRU<HTMLCanvasElement> {
  private source?: MosaicSource;
  private revision?: string | number;
  private sourceWidth = 0;
  private sourceHeight = 0;
  private width = 0;
  private height = 0;
  private dpr = 1;
  private background?: HTMLCanvasElement;

  setSource(image: MosaicSource, revision: string | number): void {
    const width = 'naturalWidth' in image ? image.naturalWidth : image.width;
    const height = 'naturalHeight' in image ? image.naturalHeight : image.height;
    if (image !== this.source || revision !== this.revision || width !== this.sourceWidth || height !== this.sourceHeight) {
      this.clear(); this.background = undefined;
    }
    this.source = image; this.revision = revision; this.sourceWidth = width; this.sourceHeight = height;
  }
  // Processing stays in logical coordinates; the renderer owns physical DPR buffers.
  setSize(width: number, height: number, dpr = 1): void {
    if (![width, height, dpr].every(Number.isFinite) || width <= 0 || height <= 0 || dpr <= 0) throw new RangeError('Invalid mosaic viewport');
    if (width !== this.width || height !== this.height || dpr !== this.dpr) { this.clear(); this.background = undefined; }
    this.width = width; this.height = height; this.dpr = dpr;
  }
  getProcessedSource(mode: 'mosaic' | 'blur', strength: number): HTMLCanvasElement | undefined {
    if (!Number.isFinite(strength)) throw new RangeError('Invalid mosaic strength');
    if (!this.source || this.sourceWidth <= 0 || this.sourceHeight <= 0) return undefined;
    const key = `${mode}-${strength}`, cached = this.get(key);
    if (cached) return cached;
    if (!this.background) {
      this.background = createCanvas(this.width || this.sourceWidth, this.height || this.sourceHeight);
      this.background.getContext('2d')!.drawImage(this.source, 0, 0, this.background.width, this.background.height);
    }
    const processed = mode === 'blur' ? generateBlur(this.background, strength) : generateMosaic(this.background, strength);
    this.put(key, processed); return processed;
  }
}
