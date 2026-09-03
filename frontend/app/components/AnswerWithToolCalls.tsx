'use client';

import {Maximize2, Wrench} from 'lucide-react';
import {Button} from '@astryxdesign/core/Button';
import {Collapsible} from '@astryxdesign/core/Collapsible';
import {HStack, VStack} from '@astryxdesign/core/Layout';
import {Icon} from '@astryxdesign/core/Icon';
import {Markdown} from '@astryxdesign/core/Markdown';

// Astryx's scale shrinks below body text after h4: h5 is 12px and h6 is 10px,
// against a 14px body. Starting at 3 pushed an answer's own section headings
// (##) to h4 and its sub-headings (###) to h5, so the headings came out
// *smaller* than the paragraphs under them. Starting at 2 puts sections at h3
// (17px) and sub-headings at h4 (14px, semibold) - a hierarchy that descends
// rather than inverts.
import {Spinner} from '@astryxdesign/core/Spinner';
import {Text} from '@astryxdesign/core/Text';
import {StatusLabel} from './StatusLabel';
import {MessageToolCall} from '../lib/api';
import {ChartSpec, ChartView, chartFromResult} from './ChartView';
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
export function AnswerWithToolCalls({calls, children, isStreaming, onOpenChart}: {
  calls: MessageToolCall[];
  children: string;
  isStreaming?: boolean;
  /** Given where there is somewhere to open a chart into - the chat's side
      panel. Without it a chart still draws, it just has nowhere to go. */
  onOpenChart?: (chart: ChartSpec) => void;
}) {
  // autolink: a model writing a source table puts the address in a cell as bare
  // text, not as a markdown link, and web search made that the common case. GFM
  // rules skip code spans and existing links, and Astryx opens an external one
  // in a new tab, so following a source does not lose the conversation.
  if (calls.length === 0) {
    return <Markdown autolink="gfm" headingLevelStart={2} isStreaming={isStreaming}>{children}</Markdown>;
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
      parts.push(<Markdown autolink="gfm" headingLevelStart={2} key={`text-${index}`}>{text}</Markdown>);
    }
    parts.push(<ToolCallPill call={call} key={call.id} onOpenChart={onOpenChart} />);
    cursor = at;
  });

  const tail = runes.slice(cursor).join('');
  if (tail.trim()) {
    parts.push(<Markdown autolink="gfm" headingLevelStart={2} isStreaming={isStreaming} key="tail">{tail}</Markdown>);
  }

  return <VStack gap={2} width="100%">{parts}</VStack>;
}

/**
 * One call as a pill: what was called, and - once it is open - what was sent
 * and what came back. Closed by default, because the answer is the thing being
 * read and the call is the thing being checked.
 */
function ToolCallPill({call, onOpenChart}: {
  call: MessageToolCall;
  onOpenChart?: (chart: ChartSpec) => void;
}) {
  const t = useTranslation();
  const isRunning = call.status === 'running';
  // A chart call is the one whose result is the point rather than the receipt,
  // so it is drawn above the pill instead of hidden inside it. The JSON stays
  // where every other result is, for anyone checking the numbers.
  const chart = call.status === 'complete' ? chartFromResult(call.detail) : null;

  return (
    <VStack gap={2} width="100%">
    {chart ? (
      // The whole thing is the way in, because that is what a reader will
      // click at: the picture, not a control beside it.
      <VStack
        className={onOpenChart ? 'cursor-pointer' : undefined}
        gap={0}
        onClick={onOpenChart ? () => onOpenChart(chart) : undefined}
        width="100%"
      >
        <ChartView chart={chart} />
      </VStack>
    ) : null}
    <Collapsible
      defaultIsOpen={false}
      trigger={
        <HStack gap={2} vAlign="center">
          {isRunning ? <Spinner size="sm" /> : <Icon icon={Wrench} size="sm" />}
          <Text type="code">{call.action}</Text>
          <Text color="secondary" maxLines={1} type="supporting">{call.tool}</Text>
          {call.status === 'error' ? <StatusLabel label={t('tool.callFailed')} variant="error" /> : null}
          {chart && onOpenChart ? (
            <HStack onClick={(event) => { event.stopPropagation(); onOpenChart(chart); }}>
              <Button
                icon={<Maximize2 size={13} />}
                label={t('chart.open')}
                size="sm"
                variant="ghost"
              />
            </HStack>
          ) : null}
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
    </VStack>
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
