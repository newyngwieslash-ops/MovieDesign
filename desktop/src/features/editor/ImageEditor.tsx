import { useEffect, useImperativeHandle, useRef, useState, type Ref } from 'react';
import { Editor, type EditorState, type TextEditRequest } from '../../../packages/editor/src/editor';
import { shapeFromData } from '../../../packages/editor/src/shapes';
import type { RectangleData, TagData, TextData } from '../../../packages/editor/src/types';
import { annotationShapeIds, collectAnnotations, moveAnnotationPairs, type Annotation, type AnnotationPair } from './annotations';
import { EditorToolbar } from './EditorToolbar';
import { AnnotationEditor } from './AnnotationEditor';

export interface ImageEditorHandle { collectAnnotations(): Annotation[] }
export function ImageEditor({ assetId, source, onExport, onAnnotations, ref }: {
  assetId: string; source: string; onExport: (blob: Blob) => Promise<void>; onAnnotations?: (annotations: Annotation[]) => void; ref?: Ref<ImageEditorHandle>;
}) {
  const host = useRef<HTMLDivElement>(null), viewport = useRef<HTMLDivElement>(null);
  const editorRef = useRef<Editor | null>(null), pairs = useRef<AnnotationPair[]>([]);
  const callbacks = useRef({ onExport, onAnnotations }); callbacks.current = { onExport, onAnnotations };
  const annotationMode = useRef(false), draftRef = useRef<RectangleData | null>(null);
  const sourceSize = useRef({ width: 1, height: 1 });
  const collect = () => {
    const editor = editorRef.current;
    return editor ? collectAnnotations(pairs.current, editor.getState().shapes, editor.getSize(), sourceSize.current) : [];
  };
  useImperativeHandle(ref, () => ({ collectAnnotations: collect }), []);
  const [state, setState] = useState<EditorState | null>(null), [draft, setDraft] = useState<RectangleData | null>(null);
  const [editing, setEditing] = useState(false), [saving, setSaving] = useState(false), [error, setError] = useState('');
  const [annotation, setAnnotation] = useState(false);
  const [annotations, setAnnotations] = useState<Annotation[]>([]);
  useEffect(() => {
    let disposed = false, observer: ResizeObserver | undefined, closeText: ((commit: boolean) => void) | undefined;
    const image = new Image(); image.crossOrigin = 'anonymous';
    pairs.current = []; setState(null); setError(''); setDraft(null); draftRef.current = null;
    image.onload = () => {
      if (disposed || !host.current || !viewport.current) return;
      const width = Math.min(960, image.naturalWidth), height = Math.max(1, Math.round(width * image.naturalHeight / image.naturalWidth));
      sourceSize.current = { width: image.naturalWidth, height: image.naturalHeight };
      const editor = new Editor(host.current, { width, height,
        expandDeletion: ids => annotationShapeIds(pairs.current, ids),
        beforeCommit: previous => moveAnnotationPairs(editor, pairs.current, previous),
        onShapeCreate: shape => {
          if (!annotationMode.current || shape.type !== 'rectangle') return false;
          editor.beginInteraction(); editor.addShapeInstanceWithoutHistory(shapeFromData(shape));
          draftRef.current = shape; setDraft(shape); return true;
        },
      });
      editorRef.current = editor; editor.renderer.main.setAttribute('aria-label', '图像编辑画布');
      const publish = () => {
        setState(editor.getState());
        const annotations = collect();
        setAnnotations(annotations); callbacks.current.onAnnotations?.(annotations);
      };
      editor.on('change', publish);
      editor.on('text-edit', (request: TextEditRequest) => {
        closeText?.(true);
        const existing = request.existingId ? editor.getShape(request.existingId) : null;
        if (existing && existing.type !== 'text' && existing.type !== 'tag') return;
        editor.beginInteraction();
        const size = editor.getSize();
        const data = existing?.data ?? request.data ?? { id: `text_${crypto.randomUUID()}`, type: 'text', text: '',
          x: Math.max(0, Math.min(request.point.x, size.width - 30)), y: Math.max(0, Math.min(request.point.y, size.height - 26)), width: 1, height: 26, style: editor.getStyle() };
        const shape = existing ?? shapeFromData(data);
        if (!existing) editor.addShapeInstanceWithoutHistory(shape);
        const text = shape.data as TextData | TagData;
        const textarea = document.createElement('textarea'); textarea.setAttribute('aria-label', request.kind === 'tag' ? '标签文字' : '图像文字'); textarea.className = 'canvas-text-input';
        textarea.value = text.text; textarea.spellcheck = false; textarea.wrap = 'off';
        host.current!.append(textarea); setEditing(true);
        let frame = 0, closed = false, composing = false;
        const position = () => {
          const tag = text.type === 'tag', fontSize = Math.max(8, text.style.fontSize ?? (tag ? 13 : 18));
          Object.assign(textarea.style, { left: `${text.x + (tag ? 6 : 0)}px`, top: `${text.y + (tag ? 2 : 0)}px`,
            width: `${Math.max(fontSize, text.width - (tag ? 12 : 0)) + 3}px`, height: `${Math.max(fontSize * 1.4, text.height - (tag ? 4 : 0))}px`,
            fontSize: `${fontSize}px`, lineHeight: '1.4', fontFamily: text.style.fontFamily ?? 'system-ui, -apple-system, "PingFang SC", "Microsoft YaHei", sans-serif',
            fontWeight: `${text.style.fontWeight ?? (tag ? 500 : 400)}`, textAlign: tag ? 'center' : 'left', caretColor: text.style.stroke });
          frame = requestAnimationFrame(position);
        };
        position();
        closeText = commit => {
          if (closed) return; closed = true; cancelAnimationFrame(frame); closeText = undefined;
          textarea.remove(); setEditing(false);
          if (!commit && text.type === 'text') editor.rollbackInteraction();
          else { if (!commit || !text.text.trim()) editor.removeShapesWithoutHistory([shape.id]); editor.commitInteraction(); }
          publish();
        };
        textarea.addEventListener('input', () => { text.text = textarea.value; editor.requestRender(); });
        textarea.addEventListener('compositionstart', () => { composing = true; });
        textarea.addEventListener('compositionend', () => { composing = false; });
        textarea.addEventListener('keydown', event => {
          if (event.isComposing || composing) return;
          if (event.key === 'Escape' || event.key === 'Enter' && !event.shiftKey) { event.preventDefault(); closeText?.(event.key !== 'Escape'); editor.renderer.main.focus(); }
        });
        textarea.addEventListener('blur', () => closeText?.(true));
        // pointerdown's canvas focus completes before the input receives focus.
        queueMicrotask(() => { if (!closed) { textarea.focus(); textarea.select(); } });
      });
      const resize = () => {
        const scale = Math.min(1, viewport.current!.clientWidth / width);
        host.current!.style.width = `${width}px`; host.current!.style.height = `${height}px`; host.current!.style.transform = `scale(${scale})`;
        viewport.current!.style.height = `${height * scale}px`; editor.setUiScale(scale);
      };
      observer = new ResizeObserver(resize); observer.observe(viewport.current); resize();
      void editor.loadImage(image).then(() => { if (!disposed) publish(); }).catch(e => { if (!disposed) setError(String(e)); });
    };
    image.onerror = () => { if (!disposed) setError('图像无法载入，请检查素材文件。'); }; image.src = source;
    return () => { disposed = true; image.onload = image.onerror = null; image.src = ''; observer?.disconnect(); closeText?.(false); editorRef.current?.destroy(); editorRef.current = null; };
  }, [assetId, source]);
  function finishDraft(comment?: string) {
    const editor = editorRef.current, rectangle = draftRef.current; if (!editor || !rectangle) return;
    if (comment) {
      const tagId = `tag_${crypto.randomUUID()}`;
      pairs.current.push({ id: crypto.randomUUID(), assetId, rectangleId: rectangle.id, tagId });
      const tag: TagData = { id: tagId, type: 'tag', text: comment, x: rectangle.x + rectangle.width + 8, y: rectangle.y, anchorX: rectangle.x + rectangle.width, anchorY: rectangle.y + rectangle.height / 2, width: 40, height: 22, style: { ...editor.getStyle(), fontSize: 13 } };
      editor.addShapeInstanceWithoutHistory(shapeFromData(tag)); editor.commitInteraction();
    } else editor.rollbackInteraction();
    draftRef.current = null; setDraft(null); editor.renderer.main.focus();
  }
  async function save() {
    const editor = editorRef.current; if (!editor || saving) return;
    setSaving(true); setError('');
    try { const blob = await editor.toBlob(); if (editorRef.current === editor) await callbacks.current.onExport(blob); }
    catch (e) { if (editorRef.current === editor) setError((e as Error).message); }
    finally { if (editorRef.current === editor) setSaving(false); }
  }
  return <section className="image-editor" aria-label="图像编辑器" data-asset-id={assetId}>
    <EditorToolbar editor={editorRef.current} state={state} disabled={saving || editing || !!draft} annotation={annotation} onTool={tool => { annotationMode.current = false; setAnnotation(false); editorRef.current?.setTool(tool); }} onAnnotation={() => { annotationMode.current = !annotationMode.current; setAnnotation(annotationMode.current); editorRef.current?.setTool(annotationMode.current ? 'rectangle' : 'select'); }} onSave={() => void save()} />
    <div ref={viewport} className="editor-viewport" style={{ pointerEvents: draft || saving ? 'none' : undefined }}><div ref={host} className="editor-stage" /></div>
    {draft && <AnnotationEditor onConfirm={finishDraft} onCancel={() => finishDraft()} />}
    {annotations.length > 0 && <ul aria-label="区域标注列表">{annotations.map(a => <li key={a.id}>{a.comment}</li>)}</ul>}
    {error && <p role="alert" className="inline-error">{error}</p>}
  </section>;
}
