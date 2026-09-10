'use client';

import {useMemo, useRef} from 'react';
import {Button} from '@astryxdesign/core/Button';
import {HStack, VStack} from '@astryxdesign/core/Layout';
import {Text} from '@astryxdesign/core/Text';

export type HTMLSpec = {title: string; content: string};

export function htmlFromResult(detail?: string): HTMLSpec | null {
  if (!detail) return null;
  try {
    const value = JSON.parse(detail)?.html;
    return value && typeof value.title === 'string' && typeof value.content === 'string' && value.content.trim()
      ? value : null;
  } catch {
    return null;
  }
}

// The policy precedes all generated markup. Never grant allow-same-origin:
// scripts must not gain access to the chat session or parent document.
const policy = `<meta http-equiv="Content-Security-Policy" content="default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data: blob:; font-src data:; connect-src 'none'; frame-src 'none'; object-src 'none'; base-uri 'none'; form-action 'none'">`;

export function HTMLView({page, fill = false}: {page: HTMLSpec; fill?: boolean}) {
  const frame = useRef<HTMLIFrameElement>(null);
  const document = useMemo(() => `<!doctype html><html><head><meta charset="utf-8">${policy}</head><body>${page.content}</body></html>`, [page.content]);
  function download() {
    const url = URL.createObjectURL(new Blob([document], {type: 'text/html;charset=utf-8'}));
    const link = window.document.createElement('a');
    link.href = url;
    link.download = 'dashboard.html';
    link.click();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  }
  return <VStack gap={2} width="100%" height={fill ? '100%' : undefined} className="min-h-0">
    <HStack gap={2} wrap="wrap" vAlign="center">
      <Text>{page.title}</Text>
      <Button label="Toàn màn hình" size="sm" variant="ghost" onClick={() => { void frame.current?.requestFullscreen().catch(() => {}); }} />
      <Button label="Tải HTML" size="sm" variant="ghost" onClick={download} />
    </HStack>
    <iframe ref={frame} title={page.title} srcDoc={document} sandbox="allow-scripts" allow="fullscreen" referrerPolicy="no-referrer" className={`w-full rounded-lg border border-subtle bg-surface ${fill ? 'min-h-0 flex-1' : 'h-96'}`} />
  </VStack>;
}
