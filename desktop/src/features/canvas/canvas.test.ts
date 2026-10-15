import test from 'node:test';
import assert from 'node:assert/strict';
import { parseItemRef, visibleNodes, emptyDocument, worldPosition } from './canvas.ts';
test('active selection excludes pending generation', () => {
 assert.throws(() => parseItemRef({type:'generation-request',requestId:'r',outputKey:'o'}));
 assert.deepEqual(parseItemRef({type:'version',resourceId:'r',versionId:'v'}),{type:'version',resourceId:'r',versionId:'v'});
});
test('viewport culls ten thousand nodes', () => {
 const doc=emptyDocument(); for(let i=0;i<10000;i++)doc.nodes['n'+i]={id:'n'+i,kind:'frame',title:'f',collapsed:false,parentId:null,position:{x:i*500,y:0},size:{width:350,height:300},zIndex:0,hidden:false};
 assert(visibleNodes(doc,{x:0,y:0,scale:1},800,600).length<5);
});
test('nested frame coordinates and collapse remain local',()=>{
 const d=emptyDocument(); const f={id:'f',kind:'frame' as const,title:'f',collapsed:false,parentId:null,position:{x:100,y:70},size:{width:500,height:500},zIndex:0,hidden:false};
 d.nodes.f=f;d.nodes.child={...f,id:'child',parentId:'f',position:{x:20,y:30},size:{width:100,height:100}};
 assert.deepEqual(worldPosition(d,d.nodes.child),{x:120,y:100});
 d.nodes.f={...f,position:{x:200,y:80}};assert.deepEqual(worldPosition(d,d.nodes.child),{x:220,y:110});assert.deepEqual(d.nodes.child.position,{x:20,y:30});
 d.nodes.f={...f,collapsed:true};assert.deepEqual(visibleNodes(d,{x:0,y:0,scale:1},800,600).map(n=>n.id),['f']);
});
