import { useEffect, useState } from 'react';
import { APIError, api, newRequestID } from '../../gateway';

type Capability = { max_prompt_bytes: number; max_count: number; max_references: number; sizes: string[]; edit: boolean; duration_seconds: number[]; audio: boolean };
type Pricing = { currency: string; unit: string; unit_amount: number; minor_unit_scale: number; credit_numerator: number; credit_denominator: number; source: string; version: string; effective_at: number };
type Config = { id: string; name: string; kind: 'image' | 'video'; protocol: string; base_url: string; model_id: string; enabled: boolean; headers: { 'OpenAI-Organization'?: string; 'OpenAI-Project'?: string }; capability: Capability; pricing: Pricing | null };
type View = { config: Config; has_key: boolean; default_for: string };
type Receipt = { provider?: View; deleted?: boolean; probe?: { local_valid: boolean; remote_tested: boolean; models_listed: boolean; model_found: boolean; message: string } };
const blank = (): Config => ({ id: newRequestID(), name: '', kind: 'image', protocol: 'openai-images', base_url: 'http://127.0.0.1:11434/v1', model_id: '', enabled: true, headers: {}, capability: { max_prompt_bytes: 65536, max_count: 1, max_references: 1, sizes: ['1024x1024'], edit: false, duration_seconds: [], audio: false }, pricing: null });
const price = (kind: string): Pricing => ({ currency: 'USD', unit: kind === 'image' ? 'image' : 'second', unit_amount: 0, minor_unit_scale: 2, credit_numerator: 0, credit_denominator: 1, source: '', version: '', effective_at: 0 });
export function ProviderSettings({ ready }: { ready: boolean }) {
  const [items, setItems] = useState<View[]>([]), [config, setConfig] = useState<Config>(blank), [editing, setEditing] = useState(false);
  const [key, setKey] = useState(''), [removeKey, setRemoveKey] = useState(false), [message, setMessage] = useState('');
  const [busy, setBusy] = useState(false), [pending, setPending] = useState<string | null>(null);
  const locked = busy || pending !== null || !ready;
  async function refresh() { const result = await api<{ items: View[] }>(null, '/api/providers'); setItems(result.items); }
  useEffect(() => { if (ready) void refresh().catch(e => setMessage(String(e.message))); }, [ready]);
  function choose(v?: View) { setConfig(v ? structuredClone(v.config) : blank()); setEditing(!!v); setKey(''); setRemoveKey(false); setMessage(''); }
  async function send(body: string) {
    setBusy(true); setMessage('');
    try {
      const result = await api<Receipt>(null, '/api/providers', { method: 'POST', body });
      setPending(null); setKey(''); setRemoveKey(false);
      if (result.provider && !result.deleted) { setConfig(result.provider.config); setEditing(true); }
      if (result.deleted) { setConfig(blank()); setEditing(false); }
      setMessage(result.probe?.message ?? (result.deleted ? '已移除配置；已有任务的连接与凭据版本保留。' : '已保存。'));
      try { await refresh(); } catch { setMessage('操作已完成，但列表刷新失败，请刷新列表。'); }
    } catch (e) {
      // The exact original body stays in memory only. No automatic resubmission,
      // no key in browser storage; navigation keeps this mounted until resolved.
      if (e instanceof APIError && e.status < 500) { setPending(null); setMessage(e.message); }
      else { setPending(body); setMessage('结果待确认。请保留本窗口并使用“确认原请求”获取原回执；刷新或关闭会清除内存中的密钥和请求，请先检查配置再发起新操作。'); }
    } finally { setBusy(false); }
  }
  function act(action: string, id: string) { void send(JSON.stringify({ requestId: newRequestID(), action, id })); }
  function save() { void send(JSON.stringify({ requestId: newRequestID(), action: editing ? 'update' : 'create', config, ...(key ? { key } : {}), ...(removeKey ? { remove_key: true } : {}) })); }
  const set = <K extends keyof Config>(k: K, v: Config[K]) => setConfig({ ...config, [k]: v });
  const cap = <K extends keyof Capability>(k: K, v: Capability[K]) => set('capability', { ...config.capability, [k]: v });
  const pricing = <K extends keyof Pricing>(k: K, v: Pricing[K]) => set('pricing', { ...config.pricing!, [k]: v });
  return <section className="provider-settings" aria-label="供应商设置">
    <h1>供应商设置</h1><p>使用你自己的本地或云端 API。费用由供应商收取，本应用没有钱包。LLM 连接由 opencode 配置。</p>
    <p>已知协议模板：OpenAI Images、火山方舟视频任务。能力和价格由你声明，免费连接测试不执行生成，也不验证计费或生成权限。</p>
    {message && <p className="provider-message" role="status">{message}</p>}
    {pending && <button disabled={busy} onClick={() => void send(pending)}>确认原请求</button>}
    <div className="provider-layout"><div className="provider-list">
      <button disabled={locked} onClick={() => choose()}>新增供应商</button><button disabled={busy} onClick={() => void refresh().catch(e => setMessage(e.message))}>刷新列表</button>
      {items.map(v => <article key={v.config.id}><h2>{v.config.name}</h2><p>{v.config.model_id} · {v.config.enabled ? '已启用' : '已停用'}{v.default_for ? ` · 默认${v.default_for === 'image' ? '图像' : '视频'}` : ''}</p><p>{v.config.base_url}</p><p>{v.has_key ? '已保存凭据' : '未保存凭据'}</p>
        <button disabled={locked} onClick={() => choose(v)}>编辑 {v.config.name}</button>
        <button disabled={locked || !v.config.enabled} onClick={() => act('select', v.config.id)}>设为默认 {v.config.name}</button>
        <button disabled={locked || !v.config.enabled} onClick={() => act('probe', v.config.id)}>免费测试 {v.config.name}</button>
        <button disabled={locked || !v.config.enabled} onClick={() => act('disable', v.config.id)}>停用 {v.config.name}</button>
        <button disabled={locked} onClick={() => act('delete', v.config.id)}>移除 {v.config.name}</button>
      </article>)}
    </div><form onSubmit={e => { e.preventDefault(); save(); }}><fieldset disabled={locked}><legend>{editing ? '编辑供应商' : '新增供应商'}</legend>
      <label>名称<input aria-label="供应商名称" required maxLength={160} value={config.name} onChange={e => set('name', e.target.value)} /></label>
      <label>用途与协议<select aria-label="供应商协议" disabled={editing} value={config.kind} onChange={e => { const kind = e.target.value as 'image' | 'video'; setConfig({ ...blank(), id: config.id, name: config.name, kind, protocol: kind === 'image' ? 'openai-images' : 'ark-video', base_url: kind === 'image' ? 'http://127.0.0.1:11434/v1' : 'https://ark.cn-beijing.volces.com/api/v3', capability: { ...blank().capability, sizes: kind === 'image' ? ['1024x1024'] : [], duration_seconds: kind === 'video' ? [5, 10, 15] : [] } }); }}><option value="image">图像 · openai-images</option><option value="video">视频 · ark-video</option></select></label>
      <label>Base URL<input aria-label="供应商 Base URL" required type="url" value={config.base_url} onChange={e => set('base_url', e.target.value)} /></label>
      <label>Model ID<input aria-label="供应商模型" required maxLength={256} value={config.model_id} onChange={e => set('model_id', e.target.value)} /></label>
      <label><input type="checkbox" checked={config.enabled} onChange={e => set('enabled', e.target.checked)} />启用</label>
      <label>API 密钥（留空保留现有密钥）<input aria-label="供应商密钥" type="password" autoComplete="new-password" value={key} disabled={removeKey} onChange={e => setKey(e.target.value)} /></label>
      <label><input type="checkbox" checked={removeKey} onChange={e => { setRemoveKey(e.target.checked); if (e.target.checked) setKey(''); }} />移除当前密钥（已有任务仍保留旧版本）</label>
      {config.kind === 'image' && <><label>OpenAI-Organization<input value={config.headers['OpenAI-Organization'] ?? ''} onChange={e => set('headers', { ...config.headers, 'OpenAI-Organization': e.target.value })} /></label><label>OpenAI-Project<input value={config.headers['OpenAI-Project'] ?? ''} onChange={e => set('headers', { ...config.headers, 'OpenAI-Project': e.target.value })} /></label></>}
      <h2>声明能力</h2>
      <label>Prompt 最大 UTF-8 字节<input type="number" min={1} max={65536} required value={config.capability.max_prompt_bytes} onChange={e => cap('max_prompt_bytes', +e.target.value)} /></label>
      <label>最大输出数量<input type="number" min={1} max={config.kind === 'image' ? 10 : 1} required value={config.capability.max_count} onChange={e => cap('max_count', +e.target.value)} /></label>
      <label>最大参考图数量<input type="number" min={0} max={16} required value={config.capability.max_references} onChange={e => cap('max_references', +e.target.value)} /></label>
      <label>尺寸（逗号分隔，如 1024x1024）<input value={config.capability.sizes.join(',')} onChange={e => cap('sizes', e.target.value ? e.target.value.split(',') : [])} /></label>
      {config.kind === 'image' ? <label><input type="checkbox" checked={config.capability.edit} onChange={e => cap('edit', e.target.checked)} />声明支持图像编辑</label> : <><label>时长秒数（逗号分隔）<input value={config.capability.duration_seconds.join(',')} onChange={e => cap('duration_seconds', e.target.value ? e.target.value.split(',').map(Number) : [])} /></label><label><input type="checkbox" checked={config.capability.audio} onChange={e => cap('audio', e.target.checked)} />声明支持音频</label></>}
      <h2>声明价格</h2><label><input aria-label="配置报价" type="checkbox" checked={config.pricing !== null} onChange={e => set('pricing', e.target.checked ? price(config.kind) : null)} />配置报价（未配置表示未知，不表示免费）</label>
      {config.pricing && <>
        <label>币种<input required pattern="[A-Z]{3}" value={config.pricing.currency} onChange={e => pricing('currency', e.target.value)} /></label>
        <label>每{config.kind === 'image' ? '张' : '秒'}价格（整数最小货币单位，免费填 0）<input type="number" min={0} max={1e12} step={1} required value={config.pricing.unit_amount} onChange={e => pricing('unit_amount', +e.target.value)} /></label>
        <label>小数位数（USD 为 2）<input type="number" min={0} max={6} required value={config.pricing.minor_unit_scale} onChange={e => pricing('minor_unit_scale', +e.target.value)} /></label>
        <label>显示积分倍率分子<input type="number" min={0} max={1e9} required value={config.pricing.credit_numerator} onChange={e => pricing('credit_numerator', +e.target.value)} /></label>
        <label>显示积分倍率分母<input type="number" min={1} max={1e9} required value={config.pricing.credit_denominator} onChange={e => pricing('credit_denominator', +e.target.value)} /></label>
        <label>报价来源<input required maxLength={200} value={config.pricing.source} onChange={e => pricing('source', e.target.value)} /></label>
        <label>报价版本<input required maxLength={100} value={config.pricing.version} onChange={e => pricing('version', e.target.value)} /></label>
        <label>生效时间（Unix 秒）<input type="number" min={0} required value={config.pricing.effective_at} onChange={e => pricing('effective_at', +e.target.value)} /></label>
      </>}
      <button type="submit">保存供应商</button>
    </fieldset></form></div>
  </section>;
}
