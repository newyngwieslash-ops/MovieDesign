import Mention from '@tiptap/extension-mention';
import type {Editor} from '@tiptap/core';

export type MentionAsset = {id: string; name: string; path: string; mime_type: string|null; status: string};
export const thumbnail = (project: string, asset: MentionAsset) => `http://127.0.0.1:8001/api/files/content?project_id=${encodeURIComponent(project)}&path=${encodeURIComponent(asset.path)}`;
export function AssetMention(project: string, assets: () => MentionAsset[], active: (value: boolean) => void) {
 return Mention.extend({
  addAttributes() { return {...this.parent?.(),path:{default:null,parseHTML:element => {const path=element.getAttribute('data-path');return assets().some(a=>`asset:${a.id}`===path)?path:null;},renderHTML:attrs=>({'data-path':attrs.path})}}; },
  renderHTML({node}) {
   const asset = assets().find(a=>`asset:${a.id}`===node.attrs.path);
   return ['span',{'data-type':'mention','data-path':asset?`asset:${asset.id}`:'','class':'asset-mention'},
    ...(asset?.mime_type?.startsWith('image/') ? [['img',{src:thumbnail(project,asset),crossorigin:'anonymous',alt:'',width:24,height:24}]] : []), `@${asset?.name ?? '素材不可用'}`] as import('@tiptap/pm/model').DOMOutputSpec;
  },
 }).configure({deleteTriggerWithBackspace:true,renderText:({node})=>`@${assets().find(a=>`asset:${a.id}`===node.attrs.path)?.name ?? node.attrs.path ?? '素材不可用'}`,
  suggestion:{
   items:({query})=>assets().filter(a=>a.status==='active'&&a.name.toLocaleLowerCase().includes(query.toLocaleLowerCase())).slice(0,20),
   command:({editor,range,props})=>{const asset=assets().find(a=>a.id===props.id);if(asset)editor.chain().focus().insertContentAt(range,[{type:'mention',attrs:{path:`asset:${asset.id}`,label:asset.name}},{type:'text',text:' '}]).run();},
   render:()=>{
    let popup: HTMLDivElement|undefined, selected=0, current:{items:MentionAsset[];command:(a:MentionAsset)=>void;clientRect?: (()=>DOMRect|null)|null;editor:Editor}|undefined;
    const paint=()=>{
     if(!popup||!current)return;popup.replaceChildren();
     current.items.forEach((asset,i)=>{const b=document.createElement('button');b.type='button';b.setAttribute('role','option');b.id=`chat-asset-option-${i}`;b.setAttribute('aria-selected',String(i===selected));b.textContent=asset.name;b.onmousedown=e=>e.preventDefault();b.onclick=()=>current?.command(asset);
      if(asset.mime_type?.startsWith('image/')){const img=document.createElement('img');img.crossOrigin='anonymous';img.src=thumbnail(project,asset);img.alt='';b.prepend(img);}popup!.append(b);});
     if(!current.items.length)popup.textContent='没有匹配素材';
     const rect=current.clientRect?.();if(rect){popup.style.left=`${Math.min(rect.left,Math.max(0,innerWidth-280))}px`;popup.style.top=`${Math.min(rect.bottom+4,Math.max(0,innerHeight-250))}px`;}
     current.editor.view.dom.setAttribute('aria-expanded','true');current.editor.view.dom.setAttribute('aria-controls','chat-asset-options');
     if(current.items.length)current.editor.view.dom.setAttribute('aria-activedescendant',`chat-asset-option-${selected}`);
    };
    const close=()=>{popup?.remove();popup=undefined;active(false);if(current){current.editor.view.dom.setAttribute('aria-expanded','false');current.editor.view.dom.removeAttribute('aria-activedescendant');current.editor.view.dom.removeAttribute('aria-controls');}};
    return {onStart:props=>{current=props;selected=0;popup=document.createElement('div');popup.className='asset-suggestions';popup.id='chat-asset-options';popup.setAttribute('role','listbox');popup.setAttribute('aria-label','选择素材');document.body.append(popup);active(true);paint();},onUpdate:props=>{current=props;selected=0;paint();},onKeyDown:({event})=>{if(event.isComposing||event.keyCode===229)return false;if(event.key==='Escape'){close();return true;}if(!popup||!current)return false;if(event.key==='ArrowDown'||event.key==='ArrowUp'){selected=(selected+(event.key==='ArrowDown'?1:-1)+current.items.length)%Math.max(1,current.items.length);paint();return true;}if(event.key==='Enter'){if(current.items[selected])current.command(current.items[selected]);return true;}return false;},onExit:close};
   },
  },
 });
}
