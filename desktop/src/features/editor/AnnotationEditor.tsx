import { useState } from 'react';
export function AnnotationEditor({ onConfirm, onCancel }: { onConfirm: (comment: string) => void; onCancel: () => void }) {
  const [comment, setComment] = useState('');
  return <form className="annotation-comment" onSubmit={e => { e.preventDefault(); if (comment.trim()) onConfirm(comment.trim()); }}>
    <label>区域说明<textarea autoFocus aria-label="区域说明" value={comment} onChange={e => setComment(e.target.value)} onKeyDown={e => {
      if (e.nativeEvent.isComposing) return;
      if (e.key === 'Escape') { e.preventDefault(); onCancel(); }
      if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); if (comment.trim()) onConfirm(comment.trim()); }
    }} /></label>
    <button disabled={!comment.trim()} title={!comment.trim() ? '请填写区域说明' : '确认标注'}>确认标注</button><button type="button" onClick={onCancel}>取消标注</button>
  </form>;
}
