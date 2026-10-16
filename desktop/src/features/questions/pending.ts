export type Intent = {path: string; body: string};
export function readIntent(storage: Pick<Storage,'getItem'>, key: string, question: {id: string;session_id: string;task_id?: string}) {
 const raw=storage.getItem(key);if(raw===null)return null;
 const value: unknown=JSON.parse(raw);
 if(!value||typeof value!=='object'||!('path' in value)||!('body' in value)||typeof value.path!=='string'||typeof value.body!=='string')throw new Error('确认记录损坏，请保留本地数据并恢复记录。');
 const body=JSON.parse(value.body);
 const reply=`/api/question/${question.id}/reply?session_id=${encodeURIComponent(question.session_id)}`, reject=reply.replace('/reply?','/reject?');
 const validBody=body&&typeof body.requestId==='string'&&/^[A-Za-z0-9_-]{1,128}$/.test(body.requestId);
 const fields=Object.keys(body??{}).sort().join(',');
 const validReply=(value.path===reply||value.path===reject)&&fields==='answers,expected_revision,requestId'&&Number.isSafeInteger(body.expected_revision)&&body.expected_revision>0&&Array.isArray(body.answers)&&body.answers.every((a:unknown)=>Array.isArray(a)&&a.every(v=>typeof v==='string'));
 const validRenew=value.path==='/api/question'&&fields==='kind,requestId,session_id,task_id,task_revision'&&body.kind==='billing'&&body.task_id===question.task_id&&body.session_id===question.session_id&&Number.isSafeInteger(body.task_revision)&&body.task_revision>0;
 if(!validBody||!validReply&&!validRenew)throw new Error('确认记录与当前卡片不匹配，请保留记录并检查本地数据。');
 return value as Intent;
}
export function clearIntent(storage: Pick<Storage,'getItem'|'removeItem'>,key:string,intent:Intent) {
 if(storage.getItem(key)===JSON.stringify(intent))storage.removeItem(key);
}
