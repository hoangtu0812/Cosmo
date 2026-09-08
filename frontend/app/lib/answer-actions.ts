// Older replies embedded a menu in prose. Only convert an explicit trailing
// offer followed entirely by short list items; ordinary answer lists stay text.
export function answerActions(content: string): {body: string; actions: string[]} {
  const lines = content.split('\n');
  let fence = '';
  for (let i = 0; i < lines.length; i++) {
    const marker = lines[i].trim().match(/^(`{3,}|~{3,})/);
    if (marker) { fence = fence ? '' : marker[1][0]; continue; }
    if (fence) continue;
    const heading = lines[i].trim().replace(/^#{1,6}\s+/, '').replace(/\*\*/g, '');
    if (!/^(?:(?:nếu (?:cần|bạn muốn)|bạn có thể|tôi có thể).*(?:tiếp|sau|thêm)|(?:gợi ý|thao tác|bước|các thao tác|các bước) tiếp theo|nếu không, vui lòng trả lời|suggested next (?:steps|actions)|next steps)\s*[:：]?$/i.test(heading)) continue;
    const tail = lines.slice(i + 1).filter((line) => line.trim());
    if (!tail.length || tail.length > 4) continue;
    const actions = tail.map((line) => line.trim().match(/^(?:[-*•]|\d+[.)])\s+(.+)$/)?.[1]?.replace(/\*\*/g, '').trim());
    if (actions.some((item) => !item || item.length > 200)) continue;
    return {body: lines.slice(0, i).join('\n').trimEnd(), actions: [...new Set(actions as string[])]};
  }
  return {body: content, actions: []};
}
