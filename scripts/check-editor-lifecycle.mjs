// Real ImageEditor source; controlled React lifecycle and async Editor/DOM boundaries, no browser claims.
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import vm from 'node:vm';
import { setImmediate as tick } from 'node:timers/promises';
const require = createRequire(import.meta.url), ts = require('../desktop/node_modules/typescript');
const deferred = () => { let resolve; const promise = new Promise(r => { resolve = r; }); return { promise, resolve }; };
function harness() {
  const hooks = [], effects = [], editors = [], images = [];
  let cursor = 0;
  const react = {
    useState(initial) { const i = cursor++; if (!(i in hooks)) hooks[i] = initial; return [hooks[i], value => { hooks[i] = value; }]; },
    useRef(initial) { return hooks[cursor++] ??= { current: initial }; },
    useImperativeHandle() {},
    useEffect(callback, deps) {
      const i = cursor++, old = hooks[i];
      if (!old || deps.some((v, n) => v !== old.deps[n])) effects.push(() => { old?.cleanup?.(); hooks[i] = { deps, cleanup: callback() }; });
    },
  };
  class Editor {
    renderer = { main: { setAttribute() {} } }; blob = deferred(); destroyed = false;
    constructor() { editors.push(this); }
    on() {} setUiScale() {} loadImage() { return Promise.resolve(); }
    getState() { return { shapes: [], tool: 'select', canUndo: false, canRedo: false }; }
    getSize() { return { width: 100, height: 100 }; }
    setTool() {} toBlob() { return this.blob.promise; } destroy() { this.destroyed = true; }
  }
  class Image { naturalWidth = 100; naturalHeight = 100; constructor() { images.push(this); } }
  function EditorToolbar() {} function AnnotationEditor() {}
  const jsx = (type, props) => {
    if (type === 'div' && props.ref) props.ref.current ??= { style: {}, clientWidth: 100 };
    return { type, props };
  };
  const module = { exports: {} };
  vm.runInNewContext(ts.transpileModule(readFileSync(new URL('../desktop/src/features/editor/ImageEditor.tsx', import.meta.url), 'utf8'), {
    compilerOptions: { module: ts.ModuleKind.CommonJS, jsx: ts.JsxEmit.ReactJSX },
  }).outputText, { module, exports: module.exports, Image, ResizeObserver: class { observe() {} disconnect() {} },
    require: name => name === 'react' ? react : name === 'react/jsx-runtime' ? { jsx, jsxs: jsx }
      : name.endsWith('/editor') ? { Editor } : name.endsWith('/shapes') ? {}
        : name === './annotations' ? { collectAnnotations: () => [] }
          : name === './EditorToolbar' ? { EditorToolbar } : { AnnotationEditor },
  });
  const render = props => { cursor = 0; const tree = module.exports.ImageEditor(props); effects.splice(0).forEach(run => run()); return tree; };
  const toolbar = tree => tree.props.children.find(child => child?.type === EditorToolbar).props;
  const viewport = tree => tree.props.children.find(child => child?.props?.className === 'editor-viewport').props;
  return { render, toolbar, viewport, editors, images };
}
for (const pending of ['toBlob', 'onExport']) test(`replacement source clears saving while old ${pending} cannot unlock a newer save`, async () => {
  const h = harness(), exporting = deferred(), sent = [];
  const props = { assetId: 'asset', source: 'A', onExport: blob => { sent.push(blob); return exporting.promise; } };
  h.render(props); h.images[0].onload(); await tick();
  h.toolbar(h.render(props)).onAnnotation(); h.toolbar(h.render(props)).onSave();
  assert.equal(h.toolbar(h.render(props)).disabled, true);
  if (pending === 'onExport') { h.editors[0].blob.resolve('old'); await tick(); }
  const replacement = { ...props, source: 'B' };
  h.render(replacement); h.images[1].onload(); await tick();
  const tree = h.render(replacement);
  assert.equal(h.toolbar(tree).disabled, false, 'replacement toolbar remains disabled');
  assert.equal(h.toolbar(tree).annotation, false, 'source annotation mode leaked');
  assert.equal(h.viewport(tree).style.pointerEvents, undefined);
  assert(h.editors[0].destroyed);
  h.toolbar(tree).onSave(); assert.equal(h.toolbar(h.render(replacement)).disabled, true);
  if (pending === 'toBlob') h.editors[0].blob.resolve('old'); else exporting.resolve();
  await tick(); assert.equal(h.toolbar(h.render(replacement)).disabled, true, 'old completion unlocked newer save');
  assert.equal(sent.length, pending === 'toBlob' ? 0 : 1);
  h.editors[1].blob.resolve('new'); exporting.resolve(); await tick();
  assert.equal(h.toolbar(h.render(replacement)).disabled, false);
});
