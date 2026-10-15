import type { Editor } from '../../../packages/editor/src/editor.ts';
import type { Bounds, ShapeData } from '../../../packages/editor/src/types.ts';
export interface Annotation { id: string; assetId: string; rectangleId: string; tagId: string; comment: string; bounds: Bounds }
export type AnnotationPair = Pick<Annotation, 'id' | 'assetId' | 'rectangleId' | 'tagId'>;
export function annotationShapeIds(annotations: readonly AnnotationPair[], selectedIds: readonly string[]): Set<string> {
  const ids = new Set(selectedIds);
  for (const a of annotations) if (ids.has(a.rectangleId) || ids.has(a.tagId)) { ids.add(a.rectangleId); ids.add(a.tagId); }
  return ids;
}
// ponytail: retain pair identities until editor close; prune against retained history IDs if long sessions need it.
// Current shapes (including undo) own all mutable metadata.
export function collectAnnotations(records: readonly AnnotationPair[], shapes: readonly ShapeData[], logical: { width: number; height: number }, source: { width: number; height: number }): Annotation[] {
  const byId = new Map(shapes.map(s => [s.id, s]));
  return records.flatMap(a => {
    const rectangle = byId.get(a.rectangleId), tag = byId.get(a.tagId);
    if (rectangle?.type !== 'rectangle' || tag?.type !== 'tag') return [];
    const sx = source.width / logical.width, sy = source.height / logical.height;
    return [{ ...a, comment: tag.text, bounds: { x: rectangle.x * sx, y: rectangle.y * sy, width: rectangle.width * sx, height: rectangle.height * sy } }];
  });
}
export function moveAnnotationPairs(editor: Editor, pairs: readonly AnnotationPair[], previous: readonly ShapeData[]): void {
  const old = new Map(previous.map(s => [s.id, s]));
  for (const pair of pairs) {
    const rectangle = editor.getShape(pair.rectangleId), tag = editor.getShape(pair.tagId);
    const before = old.get(pair.rectangleId), oldTag = old.get(pair.tagId);
    if (rectangle?.data.type !== 'rectangle' || tag?.data.type !== 'tag' || before?.type !== 'rectangle' || oldTag?.type !== 'tag') continue;
    const dx = rectangle.data.x - before.x, dy = rectangle.data.y - before.y;
    if (dx || dy) tag.move(dx, dy);
    else if (tag.data.x !== oldTag.x || tag.data.y !== oldTag.y) {
      // Text layout can move a tag to fit the canvas; only a selected tag gesture moves its region.
      if (editor.getSelectedShape()?.id === tag.id && tag.data.text === oldTag.text) rectangle.move(tag.data.x - oldTag.x, tag.data.y - oldTag.y);
    }
  }
}
