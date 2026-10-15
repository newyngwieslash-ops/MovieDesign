import { useState } from 'react';
import { useInfiniteQuery, useQuery, useQueryClient } from '@tanstack/react-query';
import { api, APIError, newRequestID, type Page } from '../../gateway';

export type Project = { id: string; name: string };
const createKey = 'moviedesign.project-create.v1';
function savedCreate() {
  try {
    const body = localStorage.getItem(createKey);
    if (!body) return { body: null, name: '', error: '' };
    const input = JSON.parse(body);
    if (typeof input.requestId !== 'string' || !input.requestId || input.action !== 'create' || typeof input.value?.name !== 'string') throw new Error('保存的项目创建记录无效，请保留记录并检查本地数据');
    return { body, name: input.value.name as string, error: '' };
  } catch (e) { return { body: null, name: '', error: (e as Error).message }; }
}
export function ProjectPicker({ selected, onSelect, ready }: { selected: string; onSelect: (id: string) => void; ready: boolean }) {
  const client = useQueryClient();
  const [saved] = useState(savedCreate);
  const [name, setName] = useState(saved.name);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const [pending, setPending] = useState(saved.body);
  const projects = useInfiniteQuery({ queryKey: ['projects', null, 50], initialPageParam: 0,
    queryFn: ({ signal, pageParam }) => api<Page<Project>>(null, `/api/projects?offset=${pageParam}&limit=50`, { signal }),
    getNextPageParam: page => page.coverage.exhausted ? undefined : page.coverage.next_offset, enabled: ready });
  const selectedProject = useQuery({ queryKey: ['project', selected, 'detail'], enabled: ready && !!selected,
    queryFn: ({ signal }) => api<Project>(null, `/api/projects?id=${encodeURIComponent(selected)}`, { signal }) });
  async function create() {
    if (busy || saved.error) return;
    setBusy(true); setError('');
    try {
      const body = pending ?? JSON.stringify({ requestId: newRequestID(), action: 'create', value: { name } });
      localStorage.setItem(createKey, body); setPending(body);
      const project = await api<Project>(null, '/api/projects', { method: 'POST', body });
      localStorage.removeItem(createKey); setPending(null); setName(''); await client.invalidateQueries({ queryKey: ['projects'] }); onSelect(project.id);
    } catch (e) {
      if (e instanceof APIError && e.status === 400) { localStorage.removeItem(createKey); setPending(null); }
      setError((e as Error).message);
    } finally { setBusy(false); }
  }
  return <section className="project-picker" aria-label="项目管理">
    <label htmlFor="project">当前项目</label>
    <select id="project" value={selected} onChange={e => onSelect(e.target.value)} disabled={!ready}>
      <option value="">选择项目</option>
      {selectedProject.data && !projects.data?.pages.some(p => p.items.some(item => item.id === selected)) && <option value={selected}>{selectedProject.data.name}</option>}
      {projects.data?.pages.flatMap(p => p.items).map(p => <option key={p.id} value={p.id}>{p.name}</option>)}
    </select>
    {projects.hasNextPage && <button onClick={() => void projects.fetchNextPage()} disabled={projects.isFetchingNextPage}>加载更多项目</button>}
    <form onSubmit={e => { e.preventDefault(); void create(); }}>
      <label htmlFor="project-name">新项目名称</label>
      <input id="project-name" required maxLength={256} value={name} disabled={!!pending || !!saved.error} onChange={e => setName(e.target.value)} />
      <button disabled={!ready || busy || !!saved.error}>{busy ? '创建中…' : pending ? '续建项目' : '创建项目'}</button>
    </form>
    {pending && <p role="status">项目创建结果待确认，请沿用原名称续建。</p>}
    {(saved.error || error || projects.error) && <p role="alert">{saved.error || error || projects.error?.message}</p>}
  </section>;
}
