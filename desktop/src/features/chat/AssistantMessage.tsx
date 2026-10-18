import type {Part} from './messages';
export function AssistantMessage({parts}: {parts: Part[]}) {
 return <div className="assistant-message" aria-label="助手回复">{parts.map((part,i) => part.part_type === 'tool'
  ? <details className="chat-tool" key={`${part.step}-${part.part_id}-${part.call_id}-${i}`}><summary>工具 · {part.tool} · {part.state}</summary><pre>{JSON.stringify(part.tool_input,null,2)}</pre><pre>{part.tool_output}</pre></details>
  : part.part_type === 'reasoning' ? <details key={`${part.step}-${part.part_id}-${i}`}><summary>思考过程</summary><pre>{part.content}</pre></details>
  : <pre key={`${part.step}-${part.part_id}-${i}`}>{part.content}</pre>)}</div>;
}
