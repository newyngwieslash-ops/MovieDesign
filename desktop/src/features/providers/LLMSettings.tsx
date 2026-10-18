import { useEffect, useState } from 'react';
import { APIError, api, newRequestID } from '../../gateway';
import { preparePending, readPending, replayPending, type PendingProvider } from './recovery';

type Pricing = {currency: string; unit: 'token'; unit_amount: number; minor_unit_scale: number; credit_numerator: number; credit_denominator: number; source: string; version: string; effective_at: number};
type Config = { protocol: string; base_url: string; model_id: string; context: number; output: number; pricing?: Pricing | null };
type View = { config: Config | null; has_key: boolean };
type Receipt = { settings: View; probe?: { message: string } };
type Status = { host_ready: boolean; tools_ready: boolean; active: Config | null; pending_changes: boolean; message: string };
const storage = 'moviedesign.llm.pending.v1';
const blank: Config = { protocol: 'openai-compatible', base_url: 'http://127.0.0.1:11434/v1', model_id: '', context: 32000, output: 4096 };

export function LLMSettings({ ready }: { ready: boolean }) {
  const [config, setConfig] = useState<Config>(blank), [key, setKey] = useState(''), [remove, setRemove] = useState(false);
  const [status, setStatus] = useState<Status>(), [hasKey, setHasKey] = useState(false), [message, setMessage] = useState(''), [busy, setBusy] = useState(false);
  const [pending, setPending] = useState<PendingProvider | null>(() => { try { return readPending(localStorage.getItem(storage)); } catch { return null; } });
  const [storageError, setStorageError] = useState(() => { try { readPending(localStorage.getItem(storage)); return ''; } catch { return '无法读取 LLM 待确认记录，请恢复本机存储后继续。'; } });
  async function refresh() { const [view, host] = await Promise.all([api<View>(null, '/api/agent/settings'), api<Status>(null, '/api/agent/status')]); if (view.config) setConfig(view.config); setHasKey(view.has_key); setStatus(host); }
  useEffect(() => { if (!ready) return; void refresh().catch(e => setMessage(e.message)); const timer = setInterval(() => { void api<Status>(null, '/api/agent/status').then(setStatus).catch(() => {}); }, 5000); return () => clearInterval(timer); }, [ready]);
  async function accept(receipt: Receipt) { localStorage.removeItem(storage); setPending(null); setKey(''); setRemove(false); setMessage(receipt.probe?.message ?? '已保存。重新启动应用后启用新连接；当前任务和会话继续使用原配置。'); await refresh(); }
  async function send(body: string, original?: PendingProvider) {
    setBusy(true); setMessage('');
    try { const staged = original ?? await preparePending(body); localStorage.setItem(storage, JSON.stringify(staged)); setPending(staged); }
    catch { setStorageError('无法保存不含密钥的恢复记录，本次未发送。'); setBusy(false); return; }
    try { await accept(await api<Receipt>(null, '/api/agent/settings', { method: 'POST', body })); }
    catch (e) { if (e instanceof APIError && e.status < 500 && e.status !== 409) { try { localStorage.removeItem(storage); setPending(null); } catch { setStorageError('无法清除恢复记录。'); } setMessage(e.message); } else setMessage('结果待确认。请查询原回执；未找到时使用原密钥确认原请求，不创建新请求。'); }
    finally { setKey(''); setBusy(false); }
  }
  async function reconcile() { if (!pending) return; setBusy(true); try { await accept(await api<Receipt>(null, `/api/agent/settings/receipt?request_id=${encodeURIComponent(JSON.parse(pending.body).requestId)}`)); } catch { setMessage('原回执暂不可用，请保留记录并使用原输入确认。'); } finally { setBusy(false); } }
  async function replay() { if (!pending) return; try { await send(await replayPending(pending, key), pending); } catch (e) { setMessage((e as Error).message); } }
  const locked = !ready || busy || !!pending || !!storageError;
  const set = <K extends keyof Config>(field: K, value: Config[K]) => setConfig({ ...config, [field]: value });
  return <section aria-label="LLM 连接设置"><h2>LLM 连接</h2><p>本地或云端自定义模型。保存后需重新启动应用；免费测试只读取模型列表，Anthropic 只做本地校验。</p>
    <p role="status">{status?.message ?? '正在读取 agent 状态。'} 当前启用：{status?.active?.model_id ?? '未配置'}{status?.pending_changes ? ' · 配置已变更，重启后生效' : ''}</p>
    <p>{hasKey ? '已加密保存凭据' : '未保存凭据（本地模型可留空）'}</p>
    {message && <p role="status">{message}</p>}{storageError && <p role="alert">{storageError}</p>}
    {pending && <div><p>LLM 配置结果待确认，浏览器仅保存无密钥恢复记录。</p><button disabled={busy || !ready} onClick={() => void reconcile()}>查询 LLM 原回执</button>{pending.keyHash && <label>原 LLM 密钥<input type="password" autoComplete="new-password" value={key} onChange={e => setKey(e.target.value)} /></label>}<button disabled={busy || !ready} onClick={() => void replay()}>确认 LLM 原请求</button></div>}
    <form onSubmit={e => { e.preventDefault(); void send(JSON.stringify({ requestId: newRequestID(), action: 'save', config, ...(key ? { key } : {}), ...(remove ? { remove_key: true } : {}) })); }}><fieldset disabled={locked}><legend>LLM 连接配置</legend>
      <label>LLM 协议<select value={config.protocol} onChange={e => set('protocol', e.target.value)}><option value="openai-compatible">自定义 OpenAI Chat Completions 兼容</option><option value="openai">OpenAI Responses</option><option value="anthropic">Anthropic Messages</option></select></label>
      <label>LLM Base URL<input type="url" required value={config.base_url} onChange={e => set('base_url', e.target.value)} /></label>
      <label>LLM Model ID<input required maxLength={256} value={config.model_id} onChange={e => set('model_id', e.target.value)} /></label>
      <label>上下文 token 上限<input type="number" min={1024} max={2000000} required value={config.context} onChange={e => set('context', +e.target.value)} /></label>
      <label>输出 token 上限<input type="number" min={1} max={config.context - 1} required value={config.output} onChange={e => set('output', +e.target.value)} /></label>
      <label><input type="checkbox" checked={!!config.pricing} onChange={e => set('pricing', e.target.checked ? {currency:'USD',unit:'token',unit_amount:0,minor_unit_scale:6,credit_numerator:1,credit_denominator:1,source:'用户声明',version:'1',effective_at:Math.floor(Date.now()/1000)} : null)} />声明文本生成价格（未知价格不能提交生成）</label>
      {config.pricing && <>
        <p>按上下文 token 上限保守估算。每 token 价格应覆盖输入和输出中较高的单价；免费本地模型需明确填写 0。</p>
        <label>币种<input required pattern="[A-Z]{3}" value={config.pricing.currency} onChange={e => set('pricing',{...config.pricing!,currency:e.target.value})} /></label>
        <label>单 token 最高价（整数最小货币单位）<input required type="number" min={0} max={1000000000} step={1} value={config.pricing.unit_amount} onChange={e => set('pricing',{...config.pricing!,unit_amount:+e.target.value})} /></label>
        <label>货币小数位（6 表示百万分之一）<input required type="number" min={0} max={6} value={config.pricing.minor_unit_scale} onChange={e => set('pricing',{...config.pricing!,minor_unit_scale:+e.target.value})} /></label>
        <label>报价来源<input required maxLength={200} value={config.pricing.source} onChange={e => set('pricing',{...config.pricing!,source:e.target.value})} /></label>
        <label>报价版本<input required maxLength={100} value={config.pricing.version} onChange={e => set('pricing',{...config.pricing!,version:e.target.value})} /></label>
      </>}
      <label>LLM API 密钥（留空保留）<input type="password" autoComplete="new-password" disabled={remove} value={key} onChange={e => setKey(e.target.value)} /></label>
      <label><input type="checkbox" checked={remove} onChange={e => { setRemove(e.target.checked); setKey(''); }} />移除保存的 LLM 密钥</label>
      <button type="submit">保存 LLM 连接</button><button type="button" onClick={() => void send(JSON.stringify({ requestId: newRequestID(), action: 'probe' }))}>免费测试已保存 LLM 连接</button>
    </fieldset></form></section>;
}
