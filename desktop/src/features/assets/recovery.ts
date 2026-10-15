export type ImportOperation = {
  schema: 1; projectID: string; stage: 'upload' | 'commit' | 'deliver';
  uploadID: string; commitID: string; deliverID: string;
  file: { name: string; size: number; sha256: string };
  generatedPNG?: true;
  target: { name: string; composition: { name: string; type: { id?: string; label?: string }; placements: { key: string; role: string }[] }; baseVersionID?: string };
  stagedPath?: string; committedPath?: string; commitBody?: string; deliveryBody?: string;
};
export const storageKey = (projectID: string) => `moviedesign.import.v1.${projectID}`;
export function advanceUpload(op: ImportOperation, path: string): ImportOperation {
  return { ...op, stage: 'commit', stagedPath: path, commitBody: JSON.stringify({ requestId: op.commitID, path }) };
}
export function advanceCommit(op: ImportOperation, path: string): ImportOperation {
  return { ...op, stage: 'deliver', committedPath: path, deliveryBody: JSON.stringify({
    requestId: op.deliverID, sources: [{ key: 'file', path }], output: {
      resources: [{ key: 'file', mode: op.target.baseVersionID ? 'new_version' : 'create', name: op.target.name,
        ...(op.target.baseVersionID ? { baseVersionId: op.target.baseVersionID } : {}) }], composition: op.target.composition,
    },
  }) };
}
export function readOperation(raw: string | null, projectID: string): ImportOperation | null {
  if (!raw) return null;
  const op = JSON.parse(raw) as ImportOperation;
  if (op.schema !== 1 || op.projectID !== projectID || !['upload', 'commit', 'deliver'].includes(op.stage)
    || !op.uploadID || !op.commitID || !op.deliverID || !op.file?.sha256 || !op.target?.composition
    || (op.stage === 'commit' && (!op.stagedPath || !op.commitBody)) || (op.stage === 'deliver' && !op.deliveryBody)) {
    throw new Error('保存的导入记录无效，请保留记录并检查本地数据');
  }
  return op;
}
export function matchesFile(op: ImportOperation, file: { name: string; size: number }, sha256: string) {
  return op.file.name === file.name && op.file.size === file.size && op.file.sha256 === sha256;
}
