import { invoke } from '@tauri-apps/api/core';

export type Connection = { port: number | null; error: string | null };
export type Health = { status: string; idle: boolean; safe_to_suspend: boolean };

export const getConnection = () => invoke<Connection>('gateway_connection');

export const newRequestID = () => crypto.randomUUID();
export type Page<T> = { items: T[]; coverage: { total: number; next_offset: number; exhausted: boolean } };
export class APIError extends Error {
  constructor(public code: string, message: string, public status: number) { super(message); }
}
export async function api<T>(projectID: string | null, path: string, init: RequestInit = {}): Promise<T> {
  const url = new URL(path, 'http://127.0.0.1:8001');
  if (projectID) url.searchParams.set('project_id', projectID);
  const headers = new Headers(init.headers);
  if (typeof init.body === 'string') headers.set('Content-Type', 'application/json');
  const response = await fetch(url, { ...init, headers });
  const data = await response.json();
  if (!response.ok) throw new APIError(data.error?.code ?? 'upstream', data.error?.message ?? `请求失败（${response.status}）`, response.status);
  return data as T;
}
export async function allPages<T>(projectID: string, path: string, signal: AbortSignal): Promise<T[]> {
  // ponytail: load the complete local project in memory; use paged views when project size requires it.
  const items: T[] = [];
  let offset = 0;
  for (;;) {
    const page = await api<Page<T>>(projectID, `${path}?offset=${offset}&limit=200`, { signal });
    items.push(...page.items);
    if (page.coverage.exhausted) return items;
    if (page.coverage.next_offset <= offset) throw new Error('分页进度无效');
    offset = page.coverage.next_offset;
  }
}

export async function getHealth(port: number, signal?: AbortSignal): Promise<Health> {
  // Only native startup metadata crosses IPC. All business requests use the gateway.
  if (port !== 8001) throw new Error('本地服务端口与桌面配置不一致');
  const response = await fetch(`http://127.0.0.1:${port}/api/health`, { signal });
  if (!response.ok) throw new Error(`本地服务请求失败（${response.status}）`);
  const data: unknown = await response.json();
  if (!data || typeof data !== 'object' || !('status' in data) || typeof data.status !== 'string'
    || !('idle' in data) || typeof data.idle !== 'boolean'
    || !('safe_to_suspend' in data) || typeof data.safe_to_suspend !== 'boolean') {
    throw new Error('本地服务返回了无效的健康状态');
  }
  return data as Health;
}
