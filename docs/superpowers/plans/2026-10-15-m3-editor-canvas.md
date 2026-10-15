# M3 图像编辑器与无限画布 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 实现独立 Canvas 2D 八图形编辑器、文字/标注工作流，以及基于对象版本引用的持久创作画布。

**Architecture:** 编辑器核心不依赖 React，React 只管理生命周期和外部输入；无限画布保存服务器 revision 与对象引用，不复用编辑器快照历史。

**Tech Stack:** TypeScript、Canvas 2D、Node 内置 node:test、React、现有 WebView/CDP 冒烟、Go/SQLite。

## Global Constraints

继承总计划。“命中下限 max(8, strokeWidth)”；“HistoryManager 双栈快照式，容量 50”；一次手势一个快照。参考原恢复源码，不把摘要文档的简写误当 wire schema。

## 文件分工

`desktop/packages/editor/src/` 中 types/geometry/shapes/history/renderer/tools/editor 各承担一个责任。React 封装在 features/editor，项目节点在 features/canvas；前者导出图像与 annotation，后者存对象引用和布局。

### Task M3.1：Shape 数据、基础几何和四种线框图形

**Files:**
- Create: `desktop/packages/editor/package.json`, `desktop/packages/editor/src/types.ts`, `desktop/packages/editor/src/geometry.ts`, `desktop/packages/editor/src/shapes.ts`, `desktop/packages/editor/src/index.ts`。
- Test: `desktop/packages/editor/test/shapes.test.ts`。
- Modify: `desktop/package.json`, `desktop/tsconfig.json`。

**Interfaces:** `Point{x:number,y:number}`、`Bounds{x,y,width,height}`；`ShapeData{id,type,style,geometry}` 为纯数据；`Shape` 提供 draw(ctx)、hitTest(point)、getBounds()、move(dx,dy)、resize(bounds)、toData()。类型枚举 rectangle/ellipse/line/arrow/brush/text/tag/mosaic；`shapeFromData(data):Shape` 是唯一反序列化入口。TypeScript 采用 Node 可擦除语法，测试不依赖 React。

- [ ] 创建几何测试（imports 来自 node:test、node:assert/strict 与 geometry.ts）：

```ts
test('normalizes reverse drags and zero length segments', () => {
  assert.deepEqual(normalizeBounds({x:20,y:30,width:-10,height:-20}), {x:10,y:10,width:10,height:20});
  assert.equal(distanceToSegment({x:3,y:4},{x:0,y:0},{x:0,y:0}),5);
});
test('an unfilled rectangle has a hollow hit region', () => {
  const rect = shapeFromData({id:'r',type:'rectangle',style:{stroke:'#000',strokeWidth:1,fill:null},geometry:{x:0,y:0,width:100,height:100}});
  assert.equal(rect.hitTest({x:50,y:50}),false);
  assert.equal(rect.hitTest({x:1,y:50}),true);
});
```

- [ ] `node --experimental-strip-types --test desktop/packages/editor/test/shapes.test.ts`；确认几何/空心命中失败。
- [ ] 实现最小几何，不对负向拖动做“禁止缩放”补丁：

```ts
export function distanceToSegment(p:Point,a:Point,b:Point):number {
  const dx=b.x-a.x, dy=b.y-a.y, length=dx*dx+dy*dy;
  if (length===0) return Math.hypot(p.x-a.x,p.y-a.y);
  const t=Math.max(0,Math.min(1,((p.x-a.x)*dx+(p.y-a.y)*dy)/length));
  return Math.hypot(p.x-a.x-t*dx,p.y-a.y-t*dy);
}
export function normalizeBounds(b:Bounds):Bounds {
  return {x:Math.min(b.x,b.x+b.width),y:Math.min(b.y,b.y+b.height),width:Math.abs(b.width),height:Math.abs(b.height)};
}
```

- [ ] 实现 rectangle/ellipse 的填充与描边命中分路、line 距离命中、arrow 头长 max(12,4×strokeWidth)/头宽 max(8,3×strokeWidth)。resize 对零宽高保持有限数值；选择倒序扫描；手柄尺寸使用8/uiScale，不随缩放变细。
- [ ] 测试负拖动、角点翻越、零线段、箭头 bounds 与数据深拷贝；提交 `feat(M3.1): implement core shape geometry`。

### Task M3.2：笔刷、文字、气泡与马赛克

**Files:**
- Modify: `desktop/packages/editor/src/shapes.ts`, `desktop/packages/editor/src/types.ts`。
- Create: `desktop/packages/editor/src/mosaic.ts`, `desktop/packages/editor/test/render.html`, `desktop/packages/editor/test/render.ts`。
- Test: `desktop/packages/editor/test/media-shapes.test.ts`, `scripts/smoke-editor.mjs`。

**Interfaces:** `MosaicCache.get(key:string):HTMLCanvasElement|undefined`、`put(key,canvas)`、`clear()`；`setSource(image,revision)`；`TextShape` 的 mode=plain/filled/outlined，fontSize≥8；TagData 保存 anchor、text、bounds。内存缓存不进入 ShapeData 或历史 JSON。

- [ ] 用 Node 测 LRU 与文本数据，真实 Canvas 测二次贝塞尔、mosaic/blur 与导出。LRU 完整核心算法：

```ts
export class LRU<T> {
  private values = new Map<string,T>();
  get(key:string):T|undefined {
    const value=this.values.get(key);
    if (value!==undefined) { this.values.delete(key); this.values.set(key,value); }
    return value;
  }
  put(key:string,value:T):void {
    this.values.delete(key); this.values.set(key,value);
    if (this.values.size>8) this.values.delete(this.values.keys().next().value!);
  }
  clear():void { this.values.clear(); }
}
```

- [ ] 红灯：第九项没有淘汰、命中未提升顺序、底图更换未清缓存，均必须失败。命令 `node --experimental-strip-types --test desktop/packages/editor/test/media-shapes.test.ts`。
- [ ] Brush 用相邻点中点和 quadraticCurveTo 平滑，shape 能绘制单点，BrushTool 只提交 points>1。文字 filled padding 与 bounds 一致；resize 改字号≥8；Tag 逐字符支持 CJK 换行，计算 pointer 并夹紧卡片在画布内。
- [ ] Mosaic 先处理底图再 clip 到 rectangle/brush mask；brush 离屏 mask 复用，destination-in 合成；缓存 key 含 mode/strength，图像 revision/DPR/尺寸变化清空。resize 后 brushSize 维持视觉尺度，不用每帧重新处理整张图。
- [ ] 浏览器测试用 canvas 生成棋盘格输入，检查裁剪外像素不变、缓存第九项淘汰、两个 mode 不共用结果、对象绘制不污染后续 ctx。提交 `feat(M3.2): add brush text tag and mosaic shapes`。

### Task M3.3：历史、工具手势与四层渲染

**Files:**
- Create: `desktop/packages/editor/src/history.ts`, `desktop/packages/editor/src/renderer.ts`, `desktop/packages/editor/src/tools.ts`, `desktop/packages/editor/src/editor.ts`。
- Test: `desktop/packages/editor/test/history.test.ts`, `desktop/packages/editor/test/interaction.test.ts`。
- Modify: `desktop/packages/editor/src/index.ts`, `desktop/packages/editor/test/render.ts`。

**Interfaces:** `HistoryManager.push(snapshot),undo(current),redo(current)`；`Editor.beginInteraction/commitInteraction/cancelInteraction`；`Editor.setTool/select/deleteShapes/undo/redo/loadImage/toBlob/getState/on/off/destroy`。事件名固定 change/selection/text-edit，回调由 on 返回 unsubscribe。

- [ ] 历史检查使用真实数据，禁止测试只断言 mock 被调用：

```ts
test('undo and redo restore snapshots and a new edit clears redo',()=>{
  const history=new HistoryManager<number>(50);
  history.push(0);
  assert.equal(history.undo(100),0);
  assert.equal(history.redo(0),100);
  assert.equal(history.undo(100),0);
  history.push(1);
  assert.equal(history.redo(2),undefined);
});
```

- [ ] `node --experimental-strip-types --test desktop/packages/editor/test/history.test.ts desktop/packages/editor/test/interaction.test.ts`；补 gesture begin 幂等、commit 无变化不入历史、容量50、snapshot points/style 深拷贝、恢复同 ID 实例。
- [ ] Renderer 四层 bg/shape/preview/main；移动中的 shape 从静态 shape 层剔除一次，后续 preview 只画活动 shape，rAF 合帧；selection 最后画在 main。四层合成仍需拷贝像素，O(1) 指不遍历所有 shape，不声称常数像素开销。
- [ ] pointer capture、original+delta 变换、四角 resize、<4px 无效图形丢弃。pointercancel 按原实现提交；cancelInteraction 仅清交互快照，文本撤销逻辑放下一任务。导出从源图尺寸重绘，不导出 selection/preview；loadImage 用递增 revision 防旧图片异步覆盖；destroy 清 rAF、DOM、监听器和缓存。
- [ ] WebView 计数一次帧内多次 invalidate 只有一次 composite；drag100次不重绘全部 shapes100次，导出尺寸正确。提交 `feat(M3.3): compose transactional canvas interactions`。

### Task M3.4：React 封装、透明 textarea 与标注配对

**Files:**
- Create: `desktop/src/features/editor/ImageEditor.tsx`, `desktop/src/features/editor/EditorToolbar.tsx`, `desktop/src/features/editor/AnnotationEditor.tsx`, `desktop/src/features/editor/annotations.ts`。
- Test: `desktop/src/features/editor/annotations.test.ts`。
- Modify: `desktop/src/features/assets/AssetLibrary.tsx`, `scripts/smoke-editor.mjs`。

**Interfaces:** `ImageEditor({assetId,source,onExport,onAnnotations})`；`Annotation{id,assetId,rectangleId,tagId,comment,bounds}`；`annotationShapeIds(annotations,selectedIds):Set<string>` 扩展成对删除；`collectAnnotations():Annotation[]` 后供聊天消费。

- [ ] 红灯覆盖：删除任一 tag/rectangle 删除两者；一次 undo 恢复配对；IME Delete 不删除；输入焦点不触发编辑快捷键。

```ts
export function shouldHandleShortcut(event:KeyboardEvent):boolean {
  const target=event.target;
  return !event.isComposing && !(target instanceof HTMLElement &&
    (target.isContentEditable || target.closest('input,textarea,select,[contenteditable="true"]')));
}
```

- [ ] `node --experimental-strip-types --test desktop/src/features/editor/annotations.test.ts` 验证纯配对逻辑；DOM/IME 用 `node scripts/smoke-editor.mjs` 验证真实 WebView。
- [ ] useEffect 创建/销毁 Editor，prop 更新不重建历史。textarea 字体、行距、位置与 Canvas 同坐标；文字透明、caret 可见。input 更新画布；已有文字 Escape 恢复原文，新文字 Escape 删除且不留历史；blur 提交，空白删除。Tag Escape 按原行为删除并提交，不能混用 text 撤销语义。
- [ ] 标注工作流 rectangle draft→comment→rectangle+tag；配对删除和位置更新共用同一交互事务。工具栏按钮具备名称、当前工具状态、键盘焦点和禁用原因；保存通过 upload/commit/deliver(new_version)，不能覆写源图。
- [ ] 真实编辑八图形、撤销重做、输入中文、导出并创建血缘版本；提交 `feat(M3.4): integrate image annotations and text editing`。

### Task M3.5：持久无限画布与 M3 验收

**Files:**
- Create: `gateway/internal/store/canvas.go`, `gateway/internal/server/canvas.go`, `gateway/internal/store/canvas_test.go`。
- Create: `gateway/migrations/000005_canvas.up.sql`, `gateway/migrations/000005_canvas.down.sql`。
- Modify: `desktop/src/features/canvas/CanvasWorkspace.tsx`, `desktop/src/gateway.ts`, `desktop/src/App.tsx`, `scripts/check.ps1`, `docs/contracts.md`, `docs/milestones.md`。
- Create: `desktop/src/features/canvas/CanvasNode.tsx`, `desktop/src/features/canvas/canvas.ts`, `desktop/src/features/canvas/canvas.test.ts`, `scripts/lib/cdp.mjs`, `scripts/smoke-canvas.mjs`。

**Interfaces:** document=`{schemaVersion:1,revision,nodes,edges}`；node kind=composition/placeholder/frame；activeItem=`{type:'version',resourceId,versionId}|{type:'task-output',taskId,outputKey}`；pendingGeneration=`{requestId,outputKey}`。command 输入 `{requestId,expectedRevision,command}`，回执 `{requestId,appliedRevision,document,replayed,effects}`；这是原 commandId 的明确改名适配。

- [ ] Go tests 检查旧 revision409、重复requestId回放、同键异patch拒绝、父环/悬空边/重复node/noop/空patch拒绝；Node tests 检查 generation-request 不能进入 activeItem。

```ts
export type ItemRef =
  | {type:'version';resourceId:string;versionId:string}
  | {type:'task-output';taskId:string;outputKey:string};
export type PendingGeneration = {requestId:string;outputKey:string};
export const CANVAS_LIMITS = {nodes:10000,edges:20000,changes:30000,batch:64,content:200,references:64} as const;
```

- [ ] 红灯命令 `go -C gateway test ./internal/store -run TestCanvas -v` 和 `node --experimental-strip-types --test desktop/src/features/canvas/canvas.test.ts`。
- [ ] 一个事务验证 expectedRevision、应用 patch、更新 doc、保存 command receipt；revision 不一致不部分落库。content 分页≤200；batch≤64不嵌套。媒体类型由对象内容决定，不发明 image/video/document node.kind。
- [ ] React 工作台实现平移/缩放/选择/拖动/缩放节点、媒体播放、文档阅读、版本选择与属性；空间变换只影响视图，节点位置写服务端。图片/视频明确使用crossOrigin=anonymous，使跨源媒体请求携带Origin；验证Range/CORS与Canvas导出不被污染，不给媒体路由添加Origin豁免。可见节点裁剪降低 DOM 数量；10,000 节点数据不能全部当图像编辑器 shapes。抽取现有 CDP 检查为 scripts/lib/cdp.mjs，原 M1 smoke 同步复用且保持通过。
- [ ] 更新 check.ps1 加入 Node editor/canvas checks；真实窗口创建媒体/文档节点、移动、重启、切版本，验证持久状态。全量测试、M1/M2回归通过后提交 `feat(M3.5): persist object canvas commands` 并推送 M3。
