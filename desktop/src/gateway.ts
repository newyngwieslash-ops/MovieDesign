import { invoke } from '@tauri-apps/api/core';

export type Connection = { port: number | null; error: string | null };
export type Health = { status: string; idle: boolean; safe_to_suspend: boolean };

export const getConnection = () => invoke<Connection>('gateway_connection');

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
