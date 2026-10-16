export type GatewayEvent = { id: number; project_id: string; session_id: string; type: string; payload: unknown };
export async function readEvents(response: Response, deliver: (event: GatewayEvent) => void): Promise<void> {
 if (!response.body) throw new Error('事件流不可用');
 const reader = response.body.getReader(), decoder = new TextDecoder();
 let buffer = '';
 try {
  for (;;) {
   const {value, done} = await reader.read();
   if (done) return;
   buffer += decoder.decode(value, {stream: true});
   for (;;) {
    const boundary = /\r?\n\r?\n/.exec(buffer);
    if (!boundary) break;
    const block = buffer.slice(0, boundary.index); buffer = buffer.slice(boundary.index + boundary[0].length);
    const data = block.split(/\r?\n/).filter(line => line.startsWith('data:')).map(line => line.slice(5).trimStart()).join('\n');
    if (!data) continue;
    const event: GatewayEvent = JSON.parse(data);
    const id = block.split(/\r?\n/).find(line => line.startsWith('id:'))?.slice(3).trim();
    if (!Number.isSafeInteger(event.id) || event.id < 1 || id !== String(event.id) || typeof event.project_id !== 'string' || typeof event.session_id !== 'string' || typeof event.type !== 'string') throw new Error('事件格式无效');
    deliver(event);
   }
   if (buffer.length > 2_000_000) throw new Error('事件过大');
  }
 } finally { try { await reader.cancel(); } finally { reader.releaseLock(); } }
}

// One project transport per app; session listeners filter only after advancing
// the durable project cursor. Switching projects aborts the previous connection.
export function createEvents(storage: Pick<Storage,'getItem'|'setItem'>, transport: typeof fetch = fetch) {
 const listeners = new Set<{session?: string; event: (e: GatewayEvent) => void; reconnect: () => void}>();
 let project = '', controller: AbortController | undefined, retry: ReturnType<typeof setTimeout> | undefined, cursor = 0, failures = 0;
 const key = () => `moviedesign.events.v1.${project}`;
 const accept = (event: GatewayEvent) => {
  if (event.project_id !== project || event.id <= cursor) return;
  // Persistence failure leaves the connection failed rather than acknowledging lost state.
  storage.setItem(key(), String(event.id)); cursor = event.id;
  for (const listener of listeners) if (listener.session === undefined || event.session_id === listener.session) listener.event(event);
 };
 const connect = async () => {
  const owned = controller = new AbortController(); const selected = project, selectedKey = key(), requestedCursor = cursor;
  try {
   const response = await transport(`http://127.0.0.1:8001/api/events?project_id=${encodeURIComponent(selected)}`, {headers: requestedCursor ? {'Last-Event-ID': String(requestedCursor)} : {}, signal: owned.signal});
   if (owned.signal.aborted || project !== selected) { await response.body?.cancel(); return; }
   if (!response.ok) {
    if (response.status === 400 && requestedCursor) { storage.setItem(selectedKey, '0'); cursor = 0; }
    await response.body?.cancel();
    throw new Error('事件连接中断');
   }
   failures = 0;
   if (owned.signal.aborted || project !== selected) return;
   for (const listener of listeners) listener.reconnect();
   await readEvents(response, event => { if (!owned.signal.aborted && project === selected) accept(event); });
  } catch { /* Read transport only: recovering never repeats a control write. */ }
  finally {
   if (!owned.signal.aborted && project === selected && listeners.size) retry = setTimeout(connect, Math.min(30_000, 500 * 2 ** Math.min(failures++, 6)));
  }
 };
 return {
  subscribe(projectID: string, event: (event: GatewayEvent) => void, reconnect: () => void, session?: string) {
   if (project && project !== projectID && listeners.size) throw new Error('同时订阅多个项目不受支持');
   const saved = !project ? Number(storage.getItem(`moviedesign.events.v1.${projectID}`) ?? 0) : cursor;
   const listener = {session,event,reconnect}; listeners.add(listener);
   if (!project) {
    project = projectID;
    cursor = Number.isSafeInteger(saved) && saved >= 0 ? saved : 0;
    void connect();
   }
   return () => { listeners.delete(listener); if (!listeners.size) { controller?.abort(); clearTimeout(retry); project = ''; cursor = 0; failures = 0; } };
  },
 };
}
let shared: ReturnType<typeof createEvents> | undefined;
export function subscribeEvents(project: string, event: (event: GatewayEvent) => void, reconnect: () => void, session?: string) {
 shared ??= createEvents(localStorage);
 return shared.subscribe(project,event,reconnect,session);
}
