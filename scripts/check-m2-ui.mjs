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
async function until(predicate) { for (let i = 0; i < 100; i++) { if (predicate()) return; await tick(); } throw Error('controlled response did not arrive'); }
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
  const { AssetLibrary } = load('features/assets/AssetLibrary.tsx', { './recovery': recovery });
  const { App } = load('App.tsx', { './features/projects/ProjectPicker': { ProjectPicker }, './features/canvas/CanvasWorkspace': { CanvasWorkspace }, './features/assets/AssetLibrary': { AssetLibrary: AssetStub } });
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
