import type { GatewayEvent } from '../../events.ts';
export type DocNode = { type?: string; text?: string; attrs?: Record<string, unknown>; content?: DocNode[] };
export type Input = {requestId: string; document: DocNode; text: string; references: string[]; annotations: unknown[]};
export type Session = {id: string; project_id: string; title: string; state: string; created_at: number};
export type Message = {id: string; session_id: string; requestId: string; role: string; content: Input & {reply_to?: string; parts?: Part[]}; state: string; error?: string; created_at: number};
export type Part = {session_id: string; message_id: string; step: number; part_id?: string; call_id?: string; type: string; part_type: string; content?: string; content_delta?: string; tool?: string; state?: string; tool_input?: unknown; tool_output?: string};
export type ChatState = {project: string; session: string; cursor: number; messages: Message[]; parts: Record<string,Part[]>};
export type Pending = {schema: 1; projectID: string; kind: 'create'|'send'|'cancel'; sessionID: string; body: string; edit?: Input};
export const pendingKey = (project: string, kind: Pending['kind']) => `moviedesign.chat.${kind}.v1.${project}`;
export function assetReferences(root: DocNode): string[] {
 const result = new Set<string>();
 function visit(node: DocNode) {
  const path = node.attrs?.path;
  if (node.type === 'mention' && typeof path === 'string' && path.startsWith('asset:') && path.length > 6) result.add(path.slice(6));
  for (const child of node.content ?? []) visit(child);
 }
 visit(root); return [...result];
}
export function messageBody(requestId: string, document: DocNode, text: string, annotations: unknown[]): string {
 return JSON.stringify({requestId,document,text,references:assetReferences(document),annotations});
}
const identifier = (v: unknown): v is string => typeof v === 'string' && /^[A-Za-z0-9_-]{1,128}$/.test(v);
function validDoc(value: unknown, depth = 0): value is DocNode {
 if (!value || typeof value !== 'object' || depth > 64) return false;
 const node = value as DocNode;
 return typeof node.type === 'string' && (node.text === undefined || typeof node.text === 'string') && (node.attrs === undefined || !!node.attrs && typeof node.attrs === 'object' && !Array.isArray(node.attrs)) && (node.content === undefined || Array.isArray(node.content) && node.content.every(c => validDoc(c,depth+1)));
}
function validInput(value: Input) {
 return value && Object.keys(value).sort().join(',') === 'annotations,document,references,requestId,text' && identifier(value.requestId) && typeof value.text === 'string' && !!value.text.trim() && new TextEncoder().encode(value.text).length <= 65536 && validDoc(value.document) && value.document.type === 'doc' && Array.isArray(value.references) && value.references.length <= 16 && value.references.every(identifier) && JSON.stringify(value.references) === JSON.stringify(assetReferences(value.document)) && Array.isArray(value.annotations) && value.annotations.every(a => !!a && typeof a === 'object' && !Array.isArray(a));
}
export function readPending(raw: string|null, project: string, kind: Pending['kind']): Pending|null {
 if (raw === null) return null;
 try {
  const p = JSON.parse(raw), b = JSON.parse(p.body);
  const keys = Object.keys(p).sort().join(',');
  if (p.schema !== 1 || p.projectID !== project || p.kind !== kind || typeof p.body !== 'string' || !['body,kind,projectID,schema,sessionID','body,edit,kind,projectID,schema,sessionID'].includes(keys) || !identifier(b.requestId)) throw Error();
  if (kind === 'create' ? p.sessionID !== '' || p.edit !== undefined || Object.keys(b).sort().join(',') !== 'requestId,title' || typeof b.title !== 'string' || !b.title.trim() : !identifier(p.sessionID)) throw Error();
  if (kind === 'send' && (!validInput(b) || p.edit !== undefined)) throw Error();
  if (kind === 'cancel' && (!['message_id,requestId','requestId'].includes(Object.keys(b).sort().join(',')) || b.message_id !== undefined && !identifier(b.message_id) || p.edit !== undefined && (!b.message_id || !validInput(p.edit)))) throw Error();
  return p;
 } catch { throw new Error('聊天恢复记录损坏或归属不匹配；请保留本地数据，停止写入并检查记录。'); }
}
export function updatePending(storage: Pick<Storage,'getItem'|'setItem'|'removeItem'>, key: string, previous: unknown, next: unknown): void {
 if (storage.getItem(key) !== (previous === null ? null : JSON.stringify(previous))) throw new Error('原请求已在其他窗口更新，请刷新聊天后恢复原记录。');
 if (next === null) storage.removeItem(key); else storage.setItem(key,JSON.stringify(next));
}
const terminal = (state: string) => ['done','error','cancelled','unknown'].includes(state);
export function historyState(project: string, session: string, messages: Message[]): ChatState {
 const owned = messages.filter(m => m.session_id === session);
 const parts: Record<string,Part[]> = {};
 for (const m of owned) if (m.role === 'assistant' && m.content.reply_to) parts[m.content.reply_to] = [...(parts[m.content.reply_to] ?? []),...(m.content.parts ?? [])];
 return {project,session,cursor:0,messages:owned.filter(m => m.role === 'user'),parts};
}
export function reduceEvent(state: ChatState, event: GatewayEvent): ChatState {
 if (event.project_id !== state.project || event.id <= state.cursor) return state;
 const next = {...state,cursor:event.id};
 const p = event.payload as Part & {status?: string; error?: string};
 if (event.session_id !== state.session || !p || p.session_id !== state.session) return next;
 const message = state.messages.find(m => m.id === p.message_id);
 if (!message || terminal(message.state)) return next;
 if (event.type === 'chat_done' && p.status && terminal(p.status)) return {...next,messages:state.messages.map(m => m.id === p.message_id ? {...m,state:p.status!,error:p.error} : m)};
 if (event.type !== 'chat_message' || !Number.isSafeInteger(p.step) || p.step < 1 || !['text','reasoning','tool'].includes(p.part_type)) return next;
 const parts = [...(state.parts[p.message_id] ?? [])], i = parts.findIndex(v => v.step === p.step && v.part_id === p.part_id && v.call_id === p.call_id);
 const old = parts[i];
 const part = {...p, ...(p.part_type === 'tool' ? {} : {content:p.type === 'content_delta' ? (old?.content ?? '') + (p.content_delta ?? '') : p.content ?? ''})};
 if (i < 0) parts.push(part); else parts[i] = part;
 return {...next,parts:{...state.parts,[p.message_id]:parts}};
}
