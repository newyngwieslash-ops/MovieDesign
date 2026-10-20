// Controlled async regression against the real TSX source; only installed TypeScript and Node built-ins.
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';
import { createRequire } from 'node:module';
import { setImmediate as tick } from 'node:timers/promises';
const require = createRequire(import.meta.url);
const ts = require('../desktop/node_modules/typescript');
const source = file => readFileSync(new URL(`../desktop/src/${file}`, import.meta.url), 'utf8');
function find(tree, predicate) {
  if (Array.isArray(tree)) { for (const child of tree) { const match = find(child, predicate); if (match) return match; } return; }
  if (!tree || typeof tree !== 'object') return;
  if (predicate(tree)) return tree;
  return find(tree.props?.children, predicate);
}
async function until(predicate) { const deadline=Date.now()+3000; while(!predicate()) { if(Date.now()>=deadline)throw Error('controlled response did not arrive'); await tick(); } }
function harness(storageFailure = '') {
  const storage = new Map(), requests = [];
  const localStorage = {
    getItem(key) { if (storageFailure === 'read') throw Error('storage denied'); return storage.get(key) ?? null; },
    setItem(key, value) { if (storageFailure) throw Error('storage quota denied'); storage.set(key, value); },
    removeItem(key) { storage.delete(key); },
  };
  let hooks, cursor;
  const react = {
    useState(init) { const i = cursor++; if (!(i in hooks)) hooks[i] = typeof init === 'function' ? init() : init; const owner = hooks; return [hooks[i], value => { owner[i] = typeof value === 'function' ? value(owner[i]) : value; }]; },
    useRef(init) { const i = cursor++; return hooks[i] ??= { current: init }; }, useEffect() {},
  };
  const jsx = (type, props) => ({ type, props });
  const gateway = { newRequestID: () => crypto.randomUUID(), allPages() {}, getConnection() {}, getHealth() {},
    api(project, url, init) {
      return new Promise(resolve => {
        const stage = url.endsWith('/commit') ? 'commit' : url === '/api/upload' ? 'upload' : 'deliver';
        const id = stage === 'upload' ? init.body.get('requestId') : JSON.parse(init.body).requestId;
        const response = stage === 'upload' ? { path: `projects/p/staging/${id}` } : stage === 'commit' ? { relative: `projects/p/files/${id}` } : { delivery: { status: 'completed', versions: [{ versionId: 'v1' }] } };
        requests.push({ stage, body: init.body, resolve: () => resolve(response) });
      });
    },
  };
  function ProjectPicker() {} function CanvasWorkspace() {} function AssetStub() {}
  function load(file, extras = {}) {
    const mod = { exports: {} };
    const code = ts.transpileModule(source(file), { compilerOptions: { module: ts.ModuleKind.CommonJS, jsx: ts.JsxEmit.ReactJSX } }).outputText;
    vm.runInNewContext(code, { module: mod, exports: mod.exports, crypto, FormData, localStorage, Error, Uint8Array, Date,
      require: name => extras[name] ?? (name === 'react' ? react : name === 'react/jsx-runtime' ? { jsx, jsxs: jsx } : name === '@tanstack/react-query' ? {
        useQuery: ({ queryKey }) => queryKey[0] === 'gateway-connection' ? { data: { port: 8001 } } : queryKey[0] === 'gateway-health' ? { data: { status: 'ok' } } : { data: [] },
        useQueryClient: () => ({ invalidateQueries: async () => {}, cancelQueries: async () => {} }),
      } : name.endsWith('gateway') ? gateway : name === './VersionHistory' ? { VersionHistory() {} } : require(name)),
    });
    return mod.exports;
  }
  const recovery = load('features/assets/recovery.ts');
  const exportBytes = load('features/assets/exportBytes.ts', { './recovery': recovery });
  const { AssetLibrary } = load('features/assets/AssetLibrary.tsx', { './recovery': recovery, './exportBytes': { ...exportBytes, removeExport: async () => {} }, '../editor/ImageEditor': { ImageEditor() {} } });
  const { App } = load('App.tsx', { './features/chat/ChatPanel':{ChatPanel(){}}, './features/tasks/TaskCenter':{TaskCenter(){}},'./features/questions/ConfirmationCard':{ConfirmationCards(){}},'./features/providers/ProviderSettings':{ProviderSettings(){}},'./features/settings/UpdateSettings':{UpdateSettings(){}}, './features/projects/ProjectPicker': { ProjectPicker }, './features/canvas/CanvasWorkspace': { CanvasWorkspace }, './features/assets/AssetLibrary': { AssetLibrary: AssetStub } });
  const render = (component, state) => { hooks = state; cursor = 0; return component === App ? App() : AssetLibrary({ projectID: 'p' }); };
  const drop = state => render(AssetLibrary, state).props.onDrop({ preventDefault() {}, dataTransfer: { files: [new File(['same bytes'], 'file.png')] } });
  const key = recovery.storageKey('p');
  return { storage, requests, render, drop, key, App, AssetLibrary, ProjectPicker, AssetStub };
}
async function stageRequest(h, index, stage) { await until(() => h.requests[index]?.stage === stage); return h.requests[index]; }
async function pauseFirst(h, stage, first) {
  h.drop(first); let index = 0;
  for (const current of ['upload', 'commit', 'deliver']) { const request = await stageRequest(h, index, current); if (current === stage) return { request, next: index + 1 }; request.resolve(); index++; }
}
async function retry(h, state, stage) {
  const tree = h.render(h.AssetLibrary, state);
  if (stage === 'upload') h.drop(state);
  else find(tree, node => node.type === 'button' && node.props.children === '重新查询/续投递').props.onClick();
}
for (const stage of ['upload', 'commit', 'deliver']) test(`late ${stage} from unmounted A preserves newer pending B`, async () => {
  const h = harness(), first = [], second = [];
  const { request: late, next } = await pauseFirst(h, stage, first);
  const intentA = JSON.parse(h.storage.get(h.key));
  await retry(h, second, stage);
  let index = next;
  for (const current of ['upload', 'commit', 'deliver'].slice(['upload', 'commit', 'deliver'].indexOf(stage))) { (await stageRequest(h, index++, current)).resolve(); }
  await until(() => !h.storage.has(h.key));
  h.drop(second); await stageRequest(h, index, 'upload');
  const bodyB = h.storage.get(h.key), count = h.requests.length;
  assert.notEqual(JSON.parse(bodyB).deliverID, intentA.deliverID);
  late.resolve(); await tick();
  assert.equal(h.storage.get(h.key), bodyB, 'obsolete A changed B recovery record');
  assert.equal(h.requests.length, count, 'obsolete A continued its send chain');
});
for (const stage of ['upload', 'commit']) test(`late ${stage} cannot regress the same A intent to an earlier stage`, async () => {
  const h = harness(), first = [], second = [];
  const { request: late, next } = await pauseFirst(h, stage, first);
  await retry(h, second, stage);
  (await stageRequest(h, next, stage)).resolve();
  await stageRequest(h, next + 1, stage === 'upload' ? 'commit' : 'deliver');
  if (stage === 'upload') { h.requests[next + 1].resolve(); await stageRequest(h, next + 2, 'deliver'); }
  const newer = h.storage.get(h.key), count = h.requests.length;
  late.resolve(); await tick();
  assert.equal(h.storage.get(h.key), newer, 'same intent stage regressed');
  assert.equal(h.requests.length, count, 'obsolete stage continued its send chain');
});
test('idle remounted view reconciles completed original and allows the next import', async () => {
  const h = harness(), first = [], second = [];
  const { request: original } = await pauseFirst(h, 'deliver', first);
  const intentA = JSON.parse(h.storage.get(h.key));
  h.render(h.AssetLibrary, second);
  original.resolve(); await until(() => !h.storage.has(h.key));
  const count = h.requests.length;
  await retry(h, second, 'deliver'); await tick();
  const tree = h.render(h.AssetLibrary, second);
  assert.equal(h.requests.length, count, 'stale continuation sent another POST');
  assert(!h.storage.has(h.key));
  assert(!find(tree, node => node.props?.className === 'pending-import'));
  assert.equal(find(tree, node => node.type === 'input' && node.props['aria-label'] === '导入素材').props.disabled, false);
  assert(find(tree, node => node.props?.role === 'status'), 'reconciliation needs useful feedback');
  h.drop(second); await stageRequest(h, count, 'upload');
  assert.notEqual(JSON.parse(h.storage.get(h.key)).deliverID, intentA.deliverID);
});
for (const stage of ['upload', 'commit']) test(`idle remounted view resyncs advanced ${stage} without POST, then resumes the current body`, async () => {
  const h = harness(), first = [], second = [];
  const { request: original, next } = await pauseFirst(h, stage, first);
  h.render(h.AssetLibrary, second);
  original.resolve(); await stageRequest(h, next, stage === 'upload' ? 'commit' : 'deliver');
  const advanced = h.storage.get(h.key), current = JSON.parse(advanced), count = h.requests.length;
  await retry(h, second, 'deliver'); await tick();
  assert.equal(h.requests.length, count, 'resync must not send the old stage');
  assert.equal(h.storage.get(h.key), advanced, 'resync must not mutate current storage');
  const viewOperation = second.find(value => value?.schema === 1);
  assert.equal(JSON.stringify(viewOperation), advanced, 'live view did not adopt validated current stage');
  await retry(h, second, 'deliver');
  const resumed = await stageRequest(h, count, current.stage);
  assert.equal(resumed.body, current.stage === 'commit' ? current.commitBody : current.deliveryBody);
});
test('a newly occupied slot resyncs the idle view instead of silently discarding its file action', async () => {
  const h = harness(), first = [], second = [];
  h.render(h.AssetLibrary, second);
  h.drop(first); await stageRequest(h, 0, 'upload');
  const current = h.storage.get(h.key);
  h.drop(second);
  await until(() => second.some(value => value?.schema === 1));
  assert.equal(h.requests.length, 1);
  assert.equal(h.storage.get(h.key), current);
  assert.equal(JSON.stringify(second.find(value => value?.schema === 1)), current);
});
for (const failure of ['read', 'write']) test(`selection works in memory when storage ${failure} fails`, () => {
  const h = harness(failure), state = [];
  let tree = h.render(h.App, state);
  const picker = find(tree, node => node.type === h.ProjectPicker);
  assert(picker, 'workbench did not mount');
  picker.props.onSelect('p');
  tree = h.render(h.App, state);
  assert.equal(find(tree, node => node.type === h.ProjectPicker).props.selected, 'p');
  assert(find(tree, node => node.type === h.AssetStub), 'selected library did not mount');
  assert(find(tree, node => node.props?.role === 'alert'), 'storage failure is not visible');
});
test('a new import cannot POST if critical intent persistence fails', async () => {
  const h = harness('write'), state = []; h.drop(state);
  await until(() => find(h.render(h.AssetLibrary, state), node => node.props?.role === 'alert' && node.props.children === 'storage quota denied'));
  assert.equal(h.requests.length, 0);
});

function selectVersion(h, state, id) {
  find(h.render(h.AssetLibrary, state), node => node.type?.name === 'VersionHistory').props.onSelect(id);
  return h.render(h.AssetLibrary, state);
}
const selectedVersion = (h, state) => find(h.render(h.AssetLibrary, state), node => node.type?.name === 'VersionHistory').props.selected;
for (const change of ['before-hash', 'during-delivery', 'away-and-back']) test(`ordinary import preserves newer selection ${change}`, async () => {
  const h = harness(), state = []; selectVersion(h, state, 'original');
  h.drop(state);
  if (change === 'before-hash') selectVersion(h, state, 'other');
  (await stageRequest(h, 0, 'upload')).resolve(); (await stageRequest(h, 1, 'commit')).resolve();
  const delivery = await stageRequest(h, 2, 'deliver');
  if (change !== 'before-hash') selectVersion(h, state, 'other');
  if (change === 'away-and-back') selectVersion(h, state, 'original');
  delivery.resolve(); await until(() => !h.storage.has(h.key)); await tick();
  assert.equal(selectedVersion(h, state), change === 'away-and-back' ? 'original' : 'other');
});
for (const generatedPNG of [false, true]) for (const changeSelection of [false, true]) test(`explicit recovery generated=${generatedPNG} preserves invocation selection ownership changed=${changeSelection}`, async () => {
  const h = harness(), initial = [], resumed = [];
  await pauseFirst(h, 'deliver', initial);
  const op = JSON.parse(h.storage.get(h.key));
  if (generatedPNG) { op.generatedPNG = true; op.target.baseVersionID = 'base'; h.storage.set(h.key, JSON.stringify(op)); }
  await retry(h, resumed, 'deliver'); const delivery = await stageRequest(h, 3, 'deliver');
  if (changeSelection) selectVersion(h, resumed, 'other');
  delivery.resolve(); await until(() => !h.storage.has(h.key)); await tick();
  assert.equal(selectedVersion(h, resumed), changeSelection ? 'other' : 'v1');
});
