import { useEffect, useRef, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { allPages, api, newRequestID } from '../../gateway';
import { advanceCommit, advanceUpload, matchesFile, readOperation, storageKey, type ImportOperation } from './recovery';
import { VersionHistory, type Version } from './VersionHistory';

type Asset = { id: string; name: string; path: string; mime_type: string | null; width: number | null; height: number | null; status: string };
type Resource = { id: string; composition_id: string; description: string | null };
type Composition = { id: string; name: string; type_id: string };
type Receipt = { delivery: { status: 'completed' | 'unknown' | 'blocked'; versions: { resourceId: string; versionId: string; assetId: string }[]; problem?: { code: string; message: string } } };

function Preview({ projectID, asset }: { projectID: string; asset: Asset }) {
  // Image elements never execute SVG scripts; crossOrigin sends the required gateway Origin.
  if (!asset.mime_type?.startsWith('image/') || asset.status !== 'active') return <span className="file-icon">{asset.mime_type ?? '文件'}</span>;
  const path = `http://127.0.0.1:8001/api/files/content?project_id=${encodeURIComponent(projectID)}&path=${encodeURIComponent(asset.path)}`;
  return <img crossOrigin="anonymous" src={path} alt={asset.name} loading="lazy" />;
}
export function AssetLibrary({ projectID }: { projectID: string }) {
  const client = useQueryClient();
  const [selected, setSelected] = useState('');
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState('');
  const [error, setError] = useState('');
  const [op, setOp] = useState<ImportOperation | null>(() => {
    try { return readOperation(localStorage.getItem(storageKey(projectID)), projectID); }
    catch { return null; }
  });
  const [storageError] = useState(() => { try { readOperation(localStorage.getItem(storageKey(projectID)), projectID); return ''; } catch (e) { return (e as Error).message; } });
  const fileRef = useRef<File | null>(null);
  const lock = useRef(false);
  const assets = useQuery({ queryKey: ['project', projectID, 'assets', 'all', 200], queryFn: ({ signal }) => allPages<Asset>(projectID, '/api/assets', signal) });
  const versions = useQuery({ queryKey: ['project', projectID, 'versions', 'all', 200], queryFn: ({ signal }) => allPages<Version>(projectID, '/api/versions', signal) });
  const resources = useQuery({ queryKey: ['project', projectID, 'resources', 'all', 200], queryFn: ({ signal }) => allPages<Resource>(projectID, '/api/resources', signal) });
  const compositions = useQuery({ queryKey: ['project', projectID, 'compositions', 'all', 200], queryFn: ({ signal }) => allPages<Composition>(projectID, '/api/compositions', signal) });
  useEffect(() => () => { void client.cancelQueries({ queryKey: ['project', projectID] }); }, [client, projectID]);
  const version = versions.data?.find(v => v.id === selected);
  const resource = resources.data?.find(r => r.id === version?.resource_id);
  const composition = compositions.data?.find(c => c.id === resource?.composition_id);
  const activeAsset = assets.data?.find(a => a.id === version?.asset_id);
  function save(value: ImportOperation) {
    localStorage.setItem(storageKey(projectID), JSON.stringify(value));
    setOp(value);
  }
  async function run(initial: ImportOperation) {
    let current = initial;
    if (current.stage === 'upload') {
      if (!fileRef.current) throw new Error('请选择原文件；将核对完整内容并沿用上传请求号');
      const form = new FormData(); form.set('requestId', current.uploadID); form.set('file', fileRef.current);
      const uploaded = await api<{ path: string }>(projectID, '/api/upload', { method: 'POST', body: form });
      current = advanceUpload(current, uploaded.path); save(current);
    }
    if (current.stage === 'commit') {
      const committed = await api<{ relative: string }>(projectID, '/api/upload/commit', { method: 'POST', body: current.commitBody });
      current = advanceCommit(current, committed.relative); save(current);
    }
    const result = await api<Receipt>(projectID, '/api/deliver', { method: 'POST', body: current.deliveryBody });
    if (result.delivery.status === 'unknown') throw new Error(result.delivery.problem?.message ?? '投递尚未确认');
    localStorage.removeItem(storageKey(projectID)); setOp(null); fileRef.current = null;
    if (result.delivery.status === 'blocked') throw new Error(`投递受阻：${result.delivery.problem?.message ?? '请检查输入后发起新导入'}`);
    const delivered = result.delivery.versions[0];
    setSelected(delivered?.versionId ?? ''); setMessage('导入完成，已选择新版本');
    await client.invalidateQueries({ queryKey: ['project', projectID] });
  }
  async function execute(current: ImportOperation) {
    if (lock.current) return;
    lock.current = true; setBusy(true); setError(''); setMessage('');
    try { await run(current); } catch (e) { setError((e as Error).message); }
    finally { lock.current = false; setBusy(false); }
  }
  async function choose(file: File, revision: boolean) {
    if (lock.current || storageError) return;
    lock.current = true; setBusy(true); setError('');
    try {
      if (file.size > 256 * 1024 * 1024) throw new Error('文件超过 256 MiB 上传上限');
      // ponytail: WebCrypto buffers at most 256 MiB per import; use streaming hashing if larger files are supported.
      const digest = await crypto.subtle.digest('SHA-256', await file.arrayBuffer());
      const sha256 = Array.from(new Uint8Array(digest), b => b.toString(16).padStart(2, '0')).join('');
      let current = op;
      if (current) {
        if (current.stage !== 'upload') throw new Error('请先续投递当前导入');
        if (!matchesFile(current, file, sha256)) throw new Error('所选文件与原导入的完整内容或名称不一致，请选择原文件');
      } else {
        if (revision && (!version || !composition)) throw new Error('请先选择对象版本');
        current = { schema: 1, projectID, stage: 'upload', uploadID: newRequestID(), commitID: newRequestID(), deliverID: newRequestID(),
          file: { name: file.name, size: file.size, sha256 }, target: { name: file.name,
            ...(revision ? { baseVersionID: version!.id } : {}),
            composition: { name: revision ? composition!.name : '素材作品', type: revision ? { id: composition!.type_id } : { label: '素材' }, placements: [{ key: 'file', role: '素材' }] },
          } };
        save(current);
      }
      fileRef.current = file; await run(current);
    } catch (e) { setError((e as Error).message); }
    finally { lock.current = false; setBusy(false); }
  }
  const queryError = assets.error ?? versions.error ?? resources.error ?? compositions.error;
  return <section className="asset-library" aria-label="项目素材库" onDragOver={e => e.preventDefault()} onDrop={e => { e.preventDefault(); if (e.dataTransfer.files[0]) void choose(e.dataTransfer.files[0], false); }}>
    <div className="library-toolbar"><div><h2>素材与对象</h2><p>选择文件或拖入此区域，保存为可追溯的对象版本。</p></div>
      <label className="file-button">{op?.stage === 'upload' ? '重新选择原文件' : '导入素材'}<input aria-label="导入素材" type="file" disabled={busy || !!storageError || (!!op && op.stage !== 'upload')} onChange={e => { const f = e.target.files?.[0]; e.target.value = ''; if (f) void choose(f, false); }} /></label>
    </div>
    {op && <div className="pending-import" role="status"><strong>结果待确认</strong><p>{op.file.name} · {op.stage === 'upload' ? '上传' : op.stage === 'commit' ? '提交文件' : '投递对象'}阶段</p>
      <button disabled={busy} onClick={() => void execute(op)}>重新查询/续投递</button>
      {op.stage === 'upload' && <p>重启后请重新选择原文件，完整内容核对通过才会继续。</p>}
    </div>}
    {(error || storageError || queryError) && <p className="inline-error" role="alert">{storageError || error || queryError?.message}</p>}
    {message && <p role="status">{message}</p>}
    <div className="library-columns"><section aria-label="素材列表"><h3>素材（{assets.data?.length ?? 0}）</h3><div className="asset-grid">
      {assets.data?.map(asset => <button className="asset-card" key={asset.id} aria-pressed={activeAsset?.id === asset.id} onClick={() => { setSelected(versions.data?.find(v => v.asset_id === asset.id)?.id ?? ''); }}>
        <Preview projectID={projectID} asset={asset} /><span>{asset.name}</span><small>{asset.width && asset.height ? `${asset.width} × ${asset.height} · ` : ''}{asset.status === 'active' ? '已归档' : '文件缺失'}</small>
      </button>)}
      {assets.data?.length === 0 && <p>还没有素材，导入你的第一个文件。</p>}
    </div></section><aside className="object-details" aria-label="对象详情" data-resource-id={resource?.id} data-version-id={version?.id}>
      <h3>当前对象</h3>{resource ? <><p>{resource.description ?? '未命名对象'}</p><p>当前版本 · {version ? new Date(version.created_at).toLocaleString('zh-CN') : ''}</p>
        <label className="file-button">导入为新版本<input aria-label="导入为新版本" type="file" disabled={busy || !!op || !!storageError} onChange={e => { const f = e.target.files?.[0]; e.target.value = ''; if (f) void choose(f, true); }} /></label>
      </> : <p>选择素材以查看对象及版本。</p>}
      <VersionHistory versions={versions.data?.filter(v => v.resource_id === resource?.id) ?? []} selected={selected} onSelect={setSelected} />
    </aside></div>
  </section>;
}
