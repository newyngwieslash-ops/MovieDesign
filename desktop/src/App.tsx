import { ProviderSettings } from './features/providers/ProviderSettings';
import { useQuery } from '@tanstack/react-query';
import { getConnection, getHealth } from './gateway';
import { CanvasWorkspace } from './features/canvas/CanvasWorkspace';
import { useState } from 'react';
import { ProjectPicker } from './features/projects/ProjectPicker';
import { AssetLibrary } from './features/assets/AssetLibrary';

export function App() {
  const [page, setPage] = useState<'canvas' | 'providers'>('canvas');
  const [selection, setSelection] = useState(() => {
    try { return { id: localStorage.getItem('moviedesign.project') ?? '', warning: '' }; }
    catch { return { id: '', warning: '无法读取项目选择的本地存储，本次选择仅在当前窗口保留。' }; }
  });
  const projectID = selection.id;
  const [editRequest, setEditRequest] = useState<{id: string; nonce: number}>();
  function selectProject(id: string) {
    setEditRequest(undefined);
    let warning = '';
    try { localStorage.setItem('moviedesign.project', id); }
    catch { warning = '无法保存项目选择到本地存储，本次选择仅在当前窗口保留。'; }
    setSelection({ id, warning });
  }
  const connection = useQuery({ queryKey: ['gateway-connection'], queryFn: getConnection, refetchInterval: 2000 });
  const port = connection.data?.port;
  const health = useQuery({
    queryKey: ['gateway-health', port],
    queryFn: ({ signal }) => getHealth(port!, signal),
    enabled: port === 8001 && !connection.data?.error,
    refetchInterval: 5000,
  });
  const error = connection.data?.error ?? connection.error?.message ?? health.error?.message;
  const ready = !error && health.data?.status === 'ok';
  const status = error ? '本地服务未就绪' : ready ? '本地服务已连接' : '正在连接本地服务';

  return <div className="workbench">
    <header className="topbar">
      <a className="brand" href="/" aria-label="MovieDesign 首页"><span className="brand-icon">M</span>MovieDesign</a>
      <span className="workspace-label">本地创作空间</span>
      <span className={`connection ${ready ? 'ready' : ''}`} role="status"><i aria-hidden="true" />{status}</span>
    </header>
    <aside className="sidebar" aria-label="工作区">
      <div className="section-label">工作区</div>
      <button className="selected-item" onClick={() => setPage('canvas')} aria-current={page === 'canvas' ? 'page' : undefined}>创作画布</button><button className="selected-item" onClick={() => setPage('providers')} aria-current={page === 'providers' ? 'page' : undefined}>供应商设置</button>
      <ProjectPicker selected={projectID} ready={ready} onSelect={selectProject} />
      <div className="sidebar-note">从一个想法开始，<br />让作品在这里生长。</div>
      <div className="milestone">素材工作台</div>
    </aside>
    <main>
      <div hidden={page !== 'providers'}><ProviderSettings ready={!!ready} /></div>
      <div hidden={page !== 'canvas'}>
      <div className="canvas-header"><div><h1>创作画布</h1><p>你的图像、视频与创作依据，将在这里汇合。</p></div><span className="local-badge">LOCAL</span></div>
      {error && <div className="error-banner" role="alert"><strong>无法连接本地服务</strong><p>{error}</p><p>请检查服务日志或端口占用，然后重新启动应用。</p></div>}
      {selection.warning && <p role="alert">{selection.warning}</p>}
      {ready && projectID ? <><CanvasWorkspace key={projectID} projectID={projectID} onEdit={id => { setEditRequest({id, nonce: Date.now()}); requestAnimationFrame(() => document.querySelector('.asset-library')?.scrollIntoView()); }} /><AssetLibrary key={projectID} projectID={projectID} editRequest={editRequest} /></> : <CanvasWorkspace />}
      </div>
      <footer><span>所有创作，从这里开始</span><span>MovieDesign · 本地优先</span></footer>
    </main>
  </div>;
}
