'use client';

import {ChatToolCalls} from '@astryxdesign/core/Chat';
import {Text} from '@astryxdesign/core/Text';
import {VStack} from '@astryxdesign/core/Layout';
import {MessageToolCall} from '../lib/api';
import {useTranslation} from '../lib/i18n';

/**
 * What a turn called, shown where the answer is rather than behind a button.
 *
 * A tool round produces no words, so without this the reader watches a still
 * screen and then a paragraph appears from nowhere. Each call arrives running
 * and settles in place, and the settled set is stored on the message, so
 * reopening the conversation still shows what the answer was built on.
 */
export function ToolCallTrail({calls}: {calls: MessageToolCall[]}) {
  const t = useTranslation();
  if (calls.length === 0) return null;

  return (
    <ChatToolCalls
      calls={calls.map((call) => ({
        key: call.id,
        // The tool answers questions about a subject; the action is the verb.
        // Naming the action first reads as a sentence: "get_forecast · Weather".
        name: call.action,
        target: call.tool,
        status: call.status === 'complete' ? 'complete' : call.status === 'error' ? 'error' : 'running',
        duration: call.duration_ms ? formatDuration(call.duration_ms) : undefined,
        errorMessage: call.status === 'error' ? call.detail : undefined,
        resultDetail: call.status === 'complete' && call.detail
          ? (
            <VStack gap={1} padding={2}>
              <Text color="secondary" type="supporting">{t('tool.callResult')}</Text>
              <Text type="code">{call.detail}</Text>
            </VStack>
          )
          : undefined,
      }))}
    />
  );
}

// Sub-second calls are the common case, and "0.3s" hides the difference
// between a fast tool and a slow one better than milliseconds do.
function formatDuration(milliseconds: number): string {
  if (milliseconds < 1000) return `${milliseconds}ms`;
  return `${(milliseconds / 1000).toFixed(1)}s`;
}
