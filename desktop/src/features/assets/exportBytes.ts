import { matchesFile, type ImportOperation } from './recovery';
const database = 'moviedesign-editor-exports';
export async function fileDigest(file: Blob): Promise<string> {
  // ponytail: WebCrypto buffers the capped 256 MiB file; stream hashing if the upload ceiling grows.
  const digest = await crypto.subtle.digest('SHA-256', await file.arrayBuffer());
  return Array.from(new Uint8Array(digest), b => b.toString(16).padStart(2, '0')).join('');
}
async function store<T>(mode: IDBTransactionMode, action: (store: IDBObjectStore) => IDBRequest<T>): Promise<T> {
  const db = await new Promise<IDBDatabase>((resolve, reject) => {
    const request = indexedDB.open(database, 1);
    request.onupgradeneeded = () => request.result.createObjectStore('png');
    let blocked = false;
    request.onsuccess = () => { if (blocked) request.result.close(); else resolve(request.result); }; request.onerror = () => reject(request.error);
    request.onblocked = () => { blocked = true; reject(new Error('图像恢复存储正被其他窗口占用，请关闭后继续保存。')); };
  });
  try {
    return await new Promise<T>((resolve, reject) => {
      const transaction = db.transaction('png', mode), request = action(transaction.objectStore('png'));
      transaction.oncomplete = () => resolve(request.result);
      transaction.onabort = transaction.onerror = () => reject(transaction.error ?? request.error ?? new Error('无法保存图像恢复数据'));
    });
  } finally { db.close(); }
}
const key = (op: ImportOperation) => `${op.projectID}/${op.uploadID}`;
export async function persistExport(op: ImportOperation, file: File): Promise<void> {
  if (!matchesFile(op, file, await fileDigest(file))) throw new Error('图像内容与已冻结的保存记录不一致');
  // add never replaces another frozen intent, including a retry whose bytes were already durable.
  const existing = await store('readonly', s => s.get(key(op)));
  if (existing) { await recoverExport(op); return; }
  await store('readwrite', s => s.add({ file: op.file, blob: file }, key(op)));
}
export async function recoverExport(op: ImportOperation): Promise<File> {
  const record = await store('readonly', s => s.get(key(op)));
  if (!record || !(record.blob instanceof Blob) || typeof record.file?.name !== 'string' || typeof record.file?.size !== 'number' || typeof record.file?.sha256 !== 'string') throw new Error('原始导出图像丢失或恢复数据损坏，请保留恢复记录；不能重新绘制替代原图。');
  const file = new File([record.blob], record.file.name, { type: 'image/png' });
  if (!matchesFile(op, file, await fileDigest(file)) || JSON.stringify(record.file) !== JSON.stringify(op.file)) throw new Error('保存的图像校验失败，请保留恢复记录。');
  return file;
}
export async function removeExport(op: ImportOperation): Promise<void> { await store('readwrite', s => s.delete(key(op))); }
