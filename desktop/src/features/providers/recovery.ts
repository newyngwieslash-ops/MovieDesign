export const pendingStorageKey = 'moviedesign.providers.pending.v1';
export type PendingProvider = { body: string; keyHash: string | null };
const hash = async (key: string) => Array.from(new Uint8Array(await crypto.subtle.digest('SHA-256', new TextEncoder().encode(key))), n => n.toString(16).padStart(2, '0')).join('');
export async function preparePending(body: string): Promise<PendingProvider> {
  const value = JSON.parse(body), key = value.key;
  delete value.key;
  return { body: JSON.stringify(value), keyHash: typeof key === 'string' ? await hash(key) : null };
}
export async function replayPending(pending: PendingProvider, key: string): Promise<string> {
  if (pending.keyHash === null) return pending.body;
  if (await hash(key) !== pending.keyHash) throw new Error('请输入这次原请求的相同密钥；不创建新的 requestId。');
  return JSON.stringify({ ...JSON.parse(pending.body), key });
}
export function readPending(raw: string | null): PendingProvider | null {
  if (raw === null) return null;
  const p: unknown = JSON.parse(raw);
  if (!p || typeof p !== 'object' || !('body' in p) || typeof p.body !== 'string' || !('keyHash' in p) || p.keyHash !== null && (typeof p.keyHash !== 'string' || !/^[a-f0-9]{64}$/.test(p.keyHash))) throw new Error('待确认记录无效；保留记录并停止新写入。');
  const body = JSON.parse(p.body);
  if (!body || typeof body !== 'object' || typeof body.requestId !== 'string' || !/^[A-Za-z0-9_-]{1,128}$/.test(body.requestId) || typeof body.action !== 'string' || 'key' in body) throw new Error('待确认记录无效；保留记录并停止新写入。');
  return p as PendingProvider;
}
