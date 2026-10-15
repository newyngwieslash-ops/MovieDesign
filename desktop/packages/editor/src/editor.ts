import { HistoryManager } from './history.ts';
import { Renderer } from './renderer.ts';
import { findShapeAt, shapeFromData } from './shapes.ts';
import type { Shape } from './shapes.ts';
import { Tools } from './tools.ts';
import type { ToolName } from './tools.ts';
import type { Point, ShapeData, ShapeStyle, TagData } from './types.ts';

export interface EditorOptions {
  width: number; height: number; shapes?: ShapeData[]; style?: Partial<ShapeStyle>; tool?: ToolName;
  onShapeCreate?: (shape: ShapeData) => boolean;
  expandDeletion?: (ids: readonly string[]) => Iterable<string>;
  beforeCommit?: (previous: ShapeData[]) => void;
}
export function shouldHandleShortcut(event: KeyboardEvent): boolean {
  const target = event.target;
  return !event.isComposing && !(typeof HTMLElement !== 'undefined' && target instanceof HTMLElement &&
    (target.isContentEditable || target.closest('input,textarea,select,[contenteditable="true"]')));
}
export interface EditorState { shapes: ShapeData[]; selectedId: string | null; tool: ToolName; style: ShapeStyle; canUndo: boolean; canRedo: boolean }
export interface TextEditRequest { kind: 'text' | 'tag'; point: Point; data?: TagData; existingId?: string }
export interface EditorEvents { change: EditorState; selection: string | null; 'text-edit': TextEditRequest }
type Handler<K extends keyof EditorEvents> = (payload: EditorEvents[K]) => void;
const snapshotJSON = (snapshot: ShapeData[]) => JSON.stringify(snapshot, (_key, value: unknown) =>
  value && typeof value === 'object' && !Array.isArray(value)
    ? Object.fromEntries(Object.entries(value).sort(([a], [b]) => a.localeCompare(b))) : value);

export class Editor {
  readonly renderer: Renderer;
  private history = new HistoryManager<ShapeData[]>();
  private shapes: Shape[] = [];
  private shapeMap = new Map<string, Shape>();
  private selectedId: string | null = null;
  private style: ShapeStyle;
  private tool: Tools;
  private toolName: ToolName;
  private interactionSnapshot: ShapeData[] | null = null;
  private handlers = new Map<keyof EditorEvents, Set<(payload: never) => void>>();
  private pointerId: number | null = null;
  private imageRevision = 0;
  private pendingImages = new Map<HTMLImageElement, () => void>();
  private width: number;
  private height: number;
  private destroyed = false;
  private options: EditorOptions;

  constructor(container: HTMLElement, options: EditorOptions) {
    this.options = options;
    const shapes = options.shapes ?? [];
    if (new Set(shapes.map(s => s.id)).size !== shapes.length) throw new Error('Duplicate shape id');
    this.width = options.width; this.height = options.height;
    this.style = structuredClone({ stroke: '#FF3B30', strokeWidth: 3, ...options.style });
    this.toolName = options.tool ?? 'select'; this.tool = new Tools(this, this.toolName);
    this.renderer = new Renderer(this.width, this.height); container.append(this.renderer.main);
    this.shapes = shapes.map(shapeFromData); this.shapeMap = new Map(this.shapes.map(s => [s.id, s]));
    this.renderer.drawShapes(this.shapes); this.setCursor(this.tool.cursor);
    const main = this.renderer.main;
    main.addEventListener('pointerdown', this.down); main.addEventListener('pointermove', this.move);
    main.addEventListener('pointerup', this.up); main.addEventListener('pointercancel', this.up);
    main.addEventListener('keydown', this.key); main.addEventListener('dblclick', this.doubleClick);
  }
  getShape(id: string): Shape | undefined { return this.shapeMap.get(id); }
  getSelectedShape(): Shape | null { return this.selectedId ? this.shapeMap.get(this.selectedId) ?? null : null; }
  findShapeAt(point: Point): Shape | undefined { return findShapeAt(this.shapes, point); }
  getStyle(): ShapeStyle { return structuredClone(this.style); }
  getSize(): { width: number; height: number } { return { width: this.width, height: this.height }; }
  getUiScale(): number { return this.renderer.getUiScale(); }
  setUiScale(scale: number): void { this.renderer.setUiScale(scale); }
  setCursor(cursor: string): void { this.renderer.main.style.cursor = cursor; }
  setTool(name: ToolName): void {
    if (this.destroyed || name === this.toolName) return;
    this.tool.deactivate(); this.releasePointer(); this.toolName = name; this.tool = new Tools(this, name); this.setCursor(this.tool.cursor);
    if (name !== 'select' && this.selectedId !== null) this.select(null);
    else this.emit('change', this.getState());
  }
  setStyle(patch: Partial<ShapeStyle>): void {
    if (this.destroyed) return;
    Object.assign(this.style, structuredClone(patch));
    const shape = this.getSelectedShape(); if (!shape) { this.emit('change', this.getState()); return; }
    this.beginInteraction(); Object.assign(shape.style, structuredClone(patch));
    if (shape.data.type === 'mosaic') {
      const data = shape.data, style = data.style;
      data.mode = style.mosaicMode ?? data.mode;
      data.strength = Math.max(1, data.mode === 'blur' ? style.blurRadius ?? data.strength : style.mosaicBlockSize ?? data.strength);
      if (data.shape === 'brush') data.brushSize = Math.max(2, style.mosaicBrushSize ?? data.brushSize);
    }
    this.commitInteraction();
  }
  addShape(data: ShapeData): void { this.addShapeInstance(shapeFromData(data)); }
  addShapeInstance(shape: Shape): void {
    if (this.destroyed) return;
    if (this.shapeMap.has(shape.id)) throw new Error('Duplicate shape id');
    if (this.options.onShapeCreate?.(shape.toData())) return;
    this.beginInteraction(); this.addShapeInstanceWithoutHistory(shape); this.commitInteraction();
  }
  addShapeInstanceWithoutHistory(shape: Shape): void {
    if (this.destroyed) return;
    if (this.shapeMap.has(shape.id)) throw new Error('Duplicate shape id');
    this.shapes.push(shape); this.shapeMap.set(shape.id, shape); this.requestRender();
  }
  deleteShapes(ids: readonly string[]): void {
    if (this.destroyed) return;
    const remove = new Set(this.options.expandDeletion?.(ids) ?? ids); if (!this.shapes.some(s => remove.has(s.id))) return;
    if (this.pointerId !== null) this.finishGesture();
    this.beginInteraction(); this.removeShapesWithoutHistory([...remove]); this.commitInteraction();
  }
  removeShapesWithoutHistory(ids: readonly string[]): void {
    if (this.destroyed) return;
    const remove = new Set(this.options.expandDeletion?.(ids) ?? ids);
    this.shapes = this.shapes.filter(s => !remove.has(s.id));
    for (const id of remove) this.shapeMap.delete(id);
    if (this.selectedId && remove.has(this.selectedId)) this.select(null);
    this.requestRender();
  }
  select(id: string | null): void {
    if (this.destroyed) return;
    const next = id && this.shapeMap.has(id) ? id : null;
    if (next === this.selectedId) return;
    this.selectedId = next; this.renderer.setSelected(this.getSelectedShape()); this.emit('selection', next);
    // A deletion emits its change once, when the transaction commits.
    if (this.interactionSnapshot === null) this.emit('change', this.getState());
  }
  beginInteraction(): void { if (!this.destroyed && this.interactionSnapshot === null) this.interactionSnapshot = this.snapshot(); }
  commitInteraction(): void {
    if (this.destroyed) return;
    if (this.interactionSnapshot) this.options.beforeCommit?.(structuredClone(this.interactionSnapshot));
    const previous = this.interactionSnapshot; this.interactionSnapshot = null;
    const changed = previous !== null && snapshotJSON(previous) !== snapshotJSON(this.snapshot());
    if (changed) this.history.push(previous);
    this.requestRender(); if (changed) this.emit('change', this.getState());
  }
  cancelInteraction(): void { this.interactionSnapshot = null; }
  rollbackInteraction(): void {
    const previous = this.interactionSnapshot; this.cancelInteraction();
    if (!this.destroyed && previous) this.applySnapshot(previous);
  }
  undo(): void {
    if (this.destroyed) return;
    this.finishGesture(); const snapshot = this.history.undo(this.snapshot()); if (snapshot !== undefined) this.applySnapshot(snapshot);
  }
  redo(): void {
    if (this.destroyed) return;
    this.finishGesture(); const snapshot = this.history.redo(this.snapshot()); if (snapshot !== undefined) this.applySnapshot(snapshot);
  }
  private snapshot(): ShapeData[] { return this.shapes.map(shape => shape.toData()); }
  private applySnapshot(snapshot: ShapeData[]): void {
    this.shapes = snapshot.map(data => {
      const existing = this.shapeMap.get(data.id);
      if (existing?.type === data.type) { existing.data = structuredClone(data); return existing; }
      return shapeFromData(data);
    });
    this.shapeMap = new Map(this.shapes.map(s => [s.id, s]));
    if (this.selectedId && !this.shapeMap.has(this.selectedId)) { this.selectedId = null; this.emit('selection', null); }
    this.requestRender(); this.emit('change', this.getState());
  }
  getState(): EditorState { return { shapes: this.snapshot(), selectedId: this.selectedId, tool: this.toolName, style: this.getStyle(), canUndo: this.history.canUndo, canRedo: this.history.canRedo }; }
  on<K extends keyof EditorEvents>(type: K, handler: Handler<K>): () => void {
    let handlers = this.handlers.get(type); if (!handlers) this.handlers.set(type, handlers = new Set());
    if (!this.destroyed) handlers.add(handler as (payload: never) => void);
    return () => this.off(type, handler);
  }
  off<K extends keyof EditorEvents>(type: K, handler: Handler<K>): void { this.handlers.get(type)?.delete(handler as (payload: never) => void); }
  private emit<K extends keyof EditorEvents>(type: K, payload: EditorEvents[K]): void {
    for (const handler of this.handlers.get(type) ?? []) {
      try { handler(structuredClone(payload) as never); } catch (error) { console.error('[Editor] listener failed', error); }
    }
  }
  requestTextEdit(request: TextEditRequest): void { this.emit('text-edit', request); }
  setPreviewShape(shape: Shape | null): void { this.renderer.drawPreview(shape); }
  requestRender(): void { this.renderer.drawShapes(this.shapes); this.renderer.setSelected(this.getSelectedShape()); }
  async loadImage(src: string | HTMLImageElement): Promise<void> {
    if (this.destroyed) return;
    const revision = ++this.imageRevision;
    try {
      const image = typeof src === 'string' ? await new Promise<HTMLImageElement | undefined>((resolve, reject) => {
        const image = new Image();
        const cleanup = () => { image.onload = image.onerror = null; this.pendingImages.delete(image); };
        this.pendingImages.set(image, () => { cleanup(); image.src = ''; resolve(undefined); });
        image.crossOrigin = 'anonymous';
        image.onload = () => { cleanup(); resolve(image); };
        image.onerror = () => { cleanup(); reject(new Error('Image load failed')); };
        image.src = src;
      }) : src;
      if (!image || this.destroyed || revision !== this.imageRevision) return;
      if (image.crossOrigin !== 'anonymous') throw new Error('Image requires anonymous CORS');
      if (!image.naturalWidth || !image.naturalHeight) throw new Error('Image has no natural dimensions');
      this.height = Math.max(1, Math.round(this.width * image.naturalHeight / image.naturalWidth));
      this.renderer.setSize(this.width, this.height); this.renderer.drawBackground(image); this.requestRender(); this.emit('change', this.getState());
    } catch (error) { if (!this.destroyed && revision === this.imageRevision) throw error; }
  }
  toBlob(type = 'image/png', quality = 0.92): Promise<Blob> {
    if (this.destroyed) return Promise.reject(new Error('Editor is destroyed'));
    const canvas = this.renderer.exportComposed();
    return new Promise((resolve, reject) => canvas.toBlob(blob => { canvas.width = canvas.height = 0; if (blob) resolve(blob); else reject(new Error('Canvas.toBlob returned null')); }, type, quality));
  }
  private point(event: MouseEvent): Point {
    const rect = this.renderer.main.getBoundingClientRect();
    return { x: (event.clientX - rect.left) * (rect.width ? this.width / rect.width : 1), y: (event.clientY - rect.top) * (rect.height ? this.height / rect.height : 1) };
  }
  private down = (event: PointerEvent): void => {
    if (this.destroyed || event.button !== 0 || this.pointerId !== null) return;
    // Focus is explicit below; suppress the later native mousedown default that would blur a newly opened textarea.
    event.preventDefault();
    this.pointerId = event.pointerId; this.renderer.main.setPointerCapture(event.pointerId); this.renderer.main.focus(); this.tool.down(this.point(event));
  };
  private move = (event: PointerEvent): void => { if (!this.destroyed && (this.pointerId === null || event.pointerId === this.pointerId)) this.tool.move(this.point(event)); };
  private up = (event: PointerEvent): void => {
    if (this.destroyed || this.pointerId !== event.pointerId) return;
    this.releasePointer(); this.tool.up(this.point(event));
  };
  private key = (event: KeyboardEvent): void => {
    if (!shouldHandleShortcut(event)) return;
    if (event.key === 'Delete' || event.key === 'Backspace') { if (this.selectedId) this.deleteShapes([this.selectedId]); event.preventDefault(); }
    else if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'z') { event.preventDefault(); if (event.shiftKey) this.redo(); else this.undo(); }
    else if (event.key === 'Escape') this.select(null);
  };
  private doubleClick = (event: MouseEvent): void => {
    const shape = this.findShapeAt(this.point(event));
    if (shape?.type === 'text' || shape?.type === 'tag') this.requestTextEdit({ kind: shape.type, point: this.point(event), existingId: shape.id });
  };
  private releasePointer(): void {
    if (this.pointerId !== null && this.renderer.main.hasPointerCapture(this.pointerId)) this.renderer.main.releasePointerCapture(this.pointerId);
    this.pointerId = null;
  }
  private finishGesture(): void { this.tool.deactivate(); this.releasePointer(); this.cancelInteraction(); }
  destroy(): void {
    if (this.destroyed) return;
    this.destroyed = true; ++this.imageRevision; this.tool.deactivate(); this.releasePointer(); this.cancelInteraction();
    for (const cancel of this.pendingImages.values()) cancel(); this.pendingImages.clear();
    const main = this.renderer.main;
    main.removeEventListener('pointerdown', this.down); main.removeEventListener('pointermove', this.move);
    main.removeEventListener('pointerup', this.up); main.removeEventListener('pointercancel', this.up);
    main.removeEventListener('keydown', this.key); main.removeEventListener('dblclick', this.doubleClick);
    this.shapes = []; this.shapeMap.clear(); this.selectedId = null; this.history.clear(); this.handlers.clear(); this.renderer.destroy();
  }
}
