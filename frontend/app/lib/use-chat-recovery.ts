'use client';

import {useEffect, useRef} from 'react';
import {api, Message} from './api';

// A reloaded page follows persisted work without resubmitting the question.
// Stop before a local stream starts so polling cannot replace its placeholders.
export function useChatRecovery(conversationID: string, enabled: boolean, callbacks: {
  isBusy: () => boolean;
  onMessages: (messages: Message[]) => void;
  onError: (message: string) => void;
}) {
  const current = useRef(callbacks);
  current.current = callbacks;
  useEffect(() => {
    if (!conversationID || !enabled) return;
    let active = true;
    let timer: ReturnType<typeof setTimeout>;
    async function poll() {
      if (!active || current.current.isBusy()) return;
      try {
        const result = await api.messages(conversationID);
        if (!active || current.current.isBusy()) return;
        current.current.onMessages(result.messages);
        if (!result.messages.some((message) => message.is_pending)) return;
      } catch (error) {
        if (active && !current.current.isBusy()) current.current.onError(error instanceof Error ? error.message : 'Không tải được trạng thái Chat.');
      }
      if (active) timer = setTimeout(() => void poll(), 2000);
    }
    timer = setTimeout(() => void poll(), 2000);
    return () => { active = false; clearTimeout(timer); };
  }, [conversationID, enabled]);
}
