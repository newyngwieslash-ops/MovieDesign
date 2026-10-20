import {PromptReview} from '../pipeline/PromptReview';
import {StoryboardReview} from '../pipeline/StoryboardReview';
import {useEffect,useState,type PointerEvent} from 'react';
import type {Content,Node,Point} from './canvas';
export type CanvasAsset={id:string;path:string;name:string;mime_type:string|null;status:string;size:number;width?:number|null;height?:number|null;duration_ms?:number|null;metadata?:string|null};
export type CanvasVersion={id:string;resource_id:string;asset_id:string|null};
export function CanvasNode({node,position,selected,asset,content,projectID,onSelect,onDrag,onEdit}:{node:Node;position:Point;selected:boolean;asset?:CanvasAsset;content?:Content;projectID:string;onSelect:()=>void;onDrag:(e:PointerEvent,resize:boolean)=>void;onEdit:(version:string)=>void}) {
 const src=asset?`http://127.0.0.1:8001/api/files/content?project_id=${encodeURIComponent(projectID)}&path=${encodeURIComponent(asset.path)}`:'';
 const [text,setText]=useState('');
 useEffect(()=>{setText('');if(!asset?.mime_type?.startsWith('text/')||asset.status!=='active')return;if(asset.size>1024*1024){setText('文档超过 1 MiB 预览上限，请从素材目录打开。');return;}const abort=new AbortController();void fetch(src,{signal:abort.signal}).then(r=>{if(!r.ok)throw Error('无法读取文档');return r.text()}).then(t=>{if(!abort.signal.aborted)setText(t)}).catch(e=>{if(!abort.signal.aborted)setText(e.message)});return()=>abort.abort();},[src,asset?.mime_type,asset?.status,asset?.size]);
 const kinds:Record<string,string>={image:'图像',video:'视频',audio:'音频',text:'文本',file:'文件',table:'表格',subject:'主体'};
 const title=node.kind==='frame'?node.title:node.kind==='placeholder'?`占位 · ${kinds[node.expectedKind]??node.expectedKind}`:content?.title??asset?.name??'对象';
 const isPrompt=node.kind==='composition'&&node.activeItem?.type==='version'&&(content?.resources?.[node.activeItem.resourceId]?.name==='final_video_prompt.md'||asset?.name==='final_video_prompt.md');
 const videoMeta=(()=>{if(!asset?.mime_type?.startsWith('video/')||!asset.metadata)return null;try{return JSON.parse(asset.metadata) as {codec?:string;audioCodec?:string;timebase?:string;audioTracks?:number}}catch{return null}})();
 return <article className={`object-node ${node.kind} ${selected?'selected':''}`} data-node-id={node.id} style={{left:position.x,top:position.y,width:node.size.width,height:node.size.height,zIndex:node.zIndex,borderColor:node.kind==='frame'?node.backgroundColor:undefined}} tabIndex={0} aria-label={title} onFocus={onSelect} onPointerDown={e=>{e.stopPropagation();onSelect()}}>
  <button className="node-handle" aria-label={`移动 ${title}`} onPointerDown={e=>onDrag(e,false)}>{title}</button>
  {node.kind==='frame'?<p>{node.collapsed?'已折叠':'分组画框'}</p>:asset?.status==='active'?<div className="node-content">
   {asset.mime_type?.startsWith('image/')?<img crossOrigin="anonymous" src={src} alt={asset.name} draggable={false}/>:asset.mime_type?.startsWith('video/')?<div className="video-player-card" data-video-asset={asset.id} data-version-id={node.kind==='composition'&&node.activeItem?.type==='version'?node.activeItem.versionId:''}><video crossOrigin="anonymous" src={src} controls preload="metadata" style={{width:'100%',maxHeight:220,background:'#0b1218',borderRadius:6}}/><p className="video-meta" style={{margin:'6px 0 0',fontSize:12}}>{asset.duration_ms?`${Math.round(asset.duration_ms/1000)}s`:'视频'}{asset.width&&asset.height?` · ${asset.width}×${asset.height}`:''}{videoMeta?.timebase?` · tb ${videoMeta.timebase}`:''}{typeof videoMeta?.audioTracks==='number'?` · 音轨 ${videoMeta.audioTracks}`:''}</p></div>:asset.mime_type?.startsWith('audio/')?<audio crossOrigin="anonymous" src={src} controls preload="metadata"/>:asset.mime_type?.startsWith('text/')?node.kind==='composition'&&node.activeItem?.type==='version'?isPrompt?<PromptReview projectID={projectID} versionID={node.activeItem.versionId} text={text}/>:<StoryboardReview projectID={projectID} versionID={node.activeItem.versionId} text={text}/>:<pre>{text||'读取文档…'}</pre>:<p>{asset.name} · {asset.mime_type??'文件'}</p>}
   {asset.mime_type?.startsWith('image/')&&node.kind==='composition'&&node.activeItem?.type==='version'&&<button onClick={()=>onEdit(node.activeItem!.type==='version'?node.activeItem!.versionId:'')}>在图像编辑器打开</button>}
  </div>:<p>{node.kind==='placeholder'?node.pendingGeneration?'生成意图已保存 · 请在任务中心查看状态':'导入素材后可添加到画布':node.kind==='composition'&&node.activeItem===null?'请选择版本':'文件不可用'}</p>}
  {selected&&<button className="node-resize" aria-label="调整节点尺寸" onPointerDown={e=>onDrag(e,true)}>↘</button>}
 </article>;
}
