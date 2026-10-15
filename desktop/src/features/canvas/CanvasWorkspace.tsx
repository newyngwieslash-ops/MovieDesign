export function CanvasWorkspace() {
  return <section className="canvas-workspace" aria-label="空创作画布">
    <div className="canvas-empty">
      <div className="canvas-emblem" aria-hidden="true"><span /> <span /> <span /></div>
      <span className="eyebrow">A SPACE FOR YOUR NEXT IDEA</span>
      <h2>留白，也是创作的开始</h2>
      <p>工作台已准备好。<br />素材管理、标注编辑和 AI 创作将在后续里程碑接入。</p>
      <span className="stage-label">基础画布 · M1</span>
    </div>
    <span className="canvas-origin" aria-hidden="true">＋</span>
  </section>;
}
