export type Version = { id: string; resource_id: string; asset_id: string | null; source_version_id: string | null; origin: string; created_at: number };
export function VersionHistory({ versions, selected, onSelect }: { versions: Version[]; selected: string; onSelect: (id: string) => void }) {
  return <section aria-label="版本历史"><h3>版本历史</h3>
    {versions.length === 0 ? <p>尚无版本</p> : <ol className="version-list">{versions.map(v => <li key={v.id}>
      <button aria-pressed={v.id === selected} onClick={() => onSelect(v.id)}>{v.id === selected ? '当前 · ' : ''}{v.id.slice(0, 8)} · {v.origin === 'user' ? '手动导入' : '生成'}</button>
      {v.source_version_id && <small>源版本 {v.source_version_id.slice(0, 8)}</small>}
    </li>)}</ol>}
  </section>;
}
