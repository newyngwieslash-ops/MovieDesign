import type { Editor, EditorState } from '../../../packages/editor/src/editor';
import type { ToolName } from '../../../packages/editor/src/tools';
const tools: [ToolName, string][] = [['select', '选择'], ['rectangle', '矩形'], ['ellipse', '椭圆'], ['line', '直线'], ['arrow', '箭头'], ['brush', '画笔'], ['text', '文字'], ['tag', '标签'], ['mosaic', '马赛克']];
export function EditorToolbar({ editor, state, disabled, annotation, onAnnotation, onTool, onSave }: {
  editor: Editor | null; state: EditorState | null; disabled: boolean; annotation: boolean; onAnnotation: () => void; onTool: (tool: ToolName) => void; onSave: () => void;
}) {
  return <div className="editor-toolbar" role="toolbar" aria-label="图像编辑工具">
    {tools.map(([tool, name]) => <button key={tool} disabled={!editor || disabled} title={!editor ? '正在载入图像' : disabled ? '请先完成当前编辑' : name} aria-pressed={!annotation && state?.tool === tool} onClick={() => onTool(tool)}>{name}</button>)}
    <button disabled={!editor || disabled} aria-pressed={annotation} onClick={onAnnotation}>区域标注</button>
    <button disabled={disabled || !state?.canUndo} title={!state?.canUndo ? '没有可撤销的编辑' : '撤销上一步'} onClick={() => editor?.undo()}>撤销</button>
    <button disabled={disabled || !state?.canRedo} title={!state?.canRedo ? '没有可重做的编辑' : '重做上一步'} onClick={() => editor?.redo()}>重做</button>
    <button disabled={disabled || !state?.selectedId} title={!state?.selectedId ? '请先选择图形' : '删除选中图形及其配对标注'} onClick={() => state?.selectedId && editor?.deleteShapes([state.selectedId])}>删除</button>
    <button disabled={!editor || disabled} title={disabled ? '请先完成当前编辑或保存' : '保存为当前对象的新版本'} onClick={onSave}>保存新版本</button>
  </div>;
}
