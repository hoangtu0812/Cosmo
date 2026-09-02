'use client';

import {ChevronRight, Wrench} from 'lucide-react';
import {Collapsible} from '@astryxdesign/core/Collapsible';
import {HStack, VStack} from '@astryxdesign/core/Layout';
import {Icon} from '@astryxdesign/core/Icon';
import {Markdown} from '@astryxdesign/core/Markdown';
import {Spinner} from '@astryxdesign/core/Spinner';
import {Text} from '@astryxdesign/core/Text';
import {StatusLabel} from './StatusLabel';
import {MessageToolCall} from '../lib/api';
import {useTranslation} from '../lib/i18n';

/**
 * An answer with its tool calls put back where they happened.
 *
 * The reference interleaves them: the model says what it is about to do, the
 * call appears as a pill, then the model reacts to what came back. Gathering
 * every call into one block above the answer loses the order, and the order is
 * the part that explains the pause. Each call carries the point in the answer
 * it was made at, so the text is split there and the pill dropped in.
 */
export function AnswerWithToolCalls({calls, children, isStreaming}: {
  calls: MessageToolCall[];
  children: string;
  isStreaming?: boolean;
}) {
  if (calls.length === 0) {
    return <Markdown headingLevelStart={3} isStreaming={isStreaming}>{children}</Markdown>;
  }

  // Runes, because `at` counts runes: splitting a UTF-16 string by code unit
  // would cut Vietnamese text in the wrong place.
  const runes = [...children];
  const ordered = [...calls].sort((first, second) => first.at - second.at);
  const parts: React.ReactNode[] = [];
  let cursor = 0;

  ordered.forEach((call, index) => {
    const at = Math.min(Math.max(call.at, cursor), runes.length);
    const text = runes.slice(cursor, at).join('');
    if (text.trim()) {
      parts.push(<Markdown headingLevelStart={3} key={`text-${index}`}>{text}</Markdown>);
    }
    parts.push(<ToolCallPill call={call} key={call.id} />);
    cursor = at;
  });

  const tail = runes.slice(cursor).join('');
  if (tail.trim()) {
    parts.push(<Markdown headingLevelStart={3} isStreaming={isStreaming} key="tail">{tail}</Markdown>);
  }

  return <VStack gap={2} width="100%">{parts}</VStack>;
}

/**
 * One call as a pill: what was called, and - once it is open - what was sent
 * and what came back. Closed by default, because the answer is the thing being
 * read and the call is the thing being checked.
 */
function ToolCallPill({call}: {call: MessageToolCall}) {
  const t = useTranslation();
  const isRunning = call.status === 'running';

  return (
    <Collapsible
      defaultIsOpen={false}
      trigger={
        <HStack gap={2} vAlign="center">
          {isRunning ? <Spinner size="sm" /> : <Icon icon={Wrench} size="sm" />}
          <Text type="code">{call.action}</Text>
          <Text color="secondary" maxLines={1} type="supporting">{call.tool}</Text>
          {call.status === 'error' ? <StatusLabel label={t('tool.callFailed')} variant="error" /> : null}
          <Icon icon={ChevronRight} size="sm" />
        </HStack>
      }
    >
      <VStack gap={3} padding={3} width="100%">
        {call.arguments ? (
          <VStack gap={1} width="100%">
            <Text color="secondary" type="supporting">{t('tool.callParameters')}</Text>
            <Markdown>{fence(call.arguments)}</Markdown>
          </VStack>
        ) : null}
        {call.detail ? (
          <VStack gap={1} width="100%">
            <Text color="secondary" type="supporting">
              {call.status === 'error' ? t('tool.callFailed') : t('tool.callResult')}
            </Text>
            <Markdown>{fence(call.detail)}</Markdown>
          </VStack>
        ) : null}
      </VStack>
    </Collapsible>
  );
}

// Pretty-printed when it is JSON, verbatim when it is not: an API that answers
// in plain text should not be shown as a failed parse.
function fence(raw: string): string {
  try {
    return '```json\n' + JSON.stringify(JSON.parse(raw), null, 2) + '\n```';
  } catch {
    return '```\n' + raw + '\n```';
  }
}
