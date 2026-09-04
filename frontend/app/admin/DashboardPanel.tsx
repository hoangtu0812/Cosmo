'use client';

import {useEffect, useState} from 'react';
import {Badge} from '@astryxdesign/core/Badge';
import {Card} from '@astryxdesign/core/Card';
import {EmptyState} from '@astryxdesign/core/EmptyState';
import {Grid} from '@astryxdesign/core/Grid';
import {Heading} from '@astryxdesign/core/Heading';
import {HStack, VStack} from '@astryxdesign/core/Layout';
import {Item} from '@astryxdesign/core/Item';
import {List} from '@astryxdesign/core/List';
import {SegmentedControl, SegmentedControlItem} from '@astryxdesign/core/SegmentedControl';
import {Skeleton} from '@astryxdesign/core/Skeleton';
import {Table} from '@astryxdesign/core/Table';
import {proportional} from '@astryxdesign/core/Table';
import {Text} from '@astryxdesign/core/Text';
import {Timestamp} from '@astryxdesign/core/Timestamp';
import {Token} from '@astryxdesign/core/Token';
import {ChartSpec, ChartView} from '../components/ChartView';
import {PlatformAnalytics, api} from '../lib/api';
import {useTranslation} from '../lib/i18n';

/**
 * What the platform is being used for, and by whom.
 *
 * Every number here is counted from records the platform already keeps - runs,
 * run steps, messages and the audit log - so nothing on this screen can
 * disagree with the screens that show those records one at a time.
 *
 * The charts are drawn by the same component the chat uses, which means their
 * colours are the theme's rather than this screen's: a series never carries a
 * colour picked here, and a status is always a dot beside its own name rather
 * than a colour a reader has to decode.
 */

type Range = '7' | '30' | '90';

// A count as a reader writes it. Charts do their own axis rounding; these are
// the figures beside them, which should read exactly.
function count(value: number): string {
  return Math.round(value).toLocaleString();
}

function seconds(value: number): string {
  if (value <= 0) return '—';
  if (value < 1) return `${Math.round(value * 1000)}ms`;
  if (value < 60) return `${value.toFixed(1)}s`;
  return `${Math.round(value / 60)}m`;
}

function milliseconds(value: number): string {
  if (value <= 0) return '—';
  if (value < 1000) return `${Math.round(value)}ms`;
  return `${(value / 1000).toFixed(1)}s`;
}

/** A day as an axis label: the year is the same on every one of them. */
function dayLabel(date: string): string {
  const parsed = new Date(`${date}T00:00:00`);
  if (Number.isNaN(parsed.getTime())) return date;
  return parsed.toLocaleDateString(undefined, {day: '2-digit', month: '2-digit'});
}

export function DashboardPanel({onError}: {onError: (message: string) => void}) {
  const t = useTranslation();
  const [range, setRange] = useState<Range>('30');
  const [data, setData] = useState<PlatformAnalytics | null>(null);
  const [isLoading, setIsLoading] = useState(true);

  // The spinner is switched on where the range is chosen, not here: a setState
  // in an effect body renders twice for every fetch, and the only thing that
  // ever changes this range is a click.
  useEffect(() => {
    let isCurrent = true;
    api.analytics(Number(range))
      .then((result) => { if (isCurrent) setData(result); })
      .catch((caught) => onError(caught instanceof Error ? caught.message : t('admin.loadFailed')))
      .finally(() => { if (isCurrent) setIsLoading(false); });
    return () => { isCurrent = false; };
  }, [range, onError, t]);

  const days = Number(range);
  const trend: ChartSpec | null = data && data.trend.length > 0 ? {
    type: 'line',
    labels: data.trend.map((day) => dayLabel(day.date)),
    series: [
      {name: t('admin.dash.runs'), values: data.trend.map((day) => day.runs)},
      {name: t('admin.dash.messages'), values: data.trend.map((day) => day.messages)},
      {name: t('admin.dash.toolCalls'), values: data.trend.map((day) => day.tool_calls)},
      {name: t('admin.dash.activeUsers'), values: data.trend.map((day) => day.active_users)},
    ],
  } : null;

  // One series, so no legend: the title names what the bars measure and the
  // category is written beside each one.
  const tools: ChartSpec | null = data && data.tools.length > 0 ? {
    type: 'hbar',
    labels: data.tools.map((tool) => tool.name),
    series: [{values: data.tools.map((tool) => tool.calls)}],
  } : null;

  const hourly: ChartSpec | null = data ? {
    type: 'bar',
    labels: data.hourly.map((hour) => `${String(hour.hour).padStart(2, '0')}h`),
    series: [{values: data.hourly.map((hour) => hour.runs)}],
  } : null;

  const outcomes: ChartSpec | null = data && data.run_status.length > 0 ? {
    type: 'hbar',
    labels: data.run_status.map((item) => item.label),
    series: [{values: data.run_status.map((item) => item.count)}],
  } : null;

  const models: ChartSpec | null = data && data.models.length > 0 ? {
    type: 'hbar',
    labels: data.models.map((item) => item.label),
    series: [{values: data.models.map((item) => item.count)}],
  } : null;

  return (
    <VStack gap={5}>
      <HStack hAlign="between" vAlign="center" wrap="wrap">
        <Heading level={1} type="display-3">{t('admin.dashboard')}</Heading>
        <SegmentedControl
          label={t('admin.dash.rangeLabel')}
          onChange={(value) => { setIsLoading(true); setRange(value as Range); }}
          size="sm"
          value={range}
        >
          <SegmentedControlItem label={t('admin.dash.range7')} value="7" />
          <SegmentedControlItem label={t('admin.dash.range30')} value="30" />
          <SegmentedControlItem label={t('admin.dash.range90')} value="90" />
        </SegmentedControl>
      </HStack>

      {isLoading && !data ? <Skeleton height={180} width="100%" /> : null}

      {data ? (
        <>
          <Grid columns={{minWidth: 200}} gap={3}>
            <StatTile days={days} label={t('admin.dash.runs')} previous={data.previous.runs} value={data.window.runs} />
            <StatTile days={days} label={t('admin.dash.activeUsers')} previous={data.previous.active_users} value={data.window.active_users} />
            <StatTile days={days} label={t('admin.dash.activeWorkspaces')} previous={data.previous.active_workspaces} value={data.window.active_workspaces} />
            <StatTile days={days} label={t('admin.dash.toolCalls')} previous={data.previous.tool_calls} value={data.window.tool_calls} />
            <StatTile days={days} label={t('admin.dash.messages')} previous={data.previous.messages} value={data.window.messages} />
            <StatTile days={days} isLowerBetter label={t('admin.dash.failedRuns')} previous={data.previous.failed_runs} value={data.window.failed_runs} />
          </Grid>

          <Card width="100%">
            <VStack gap={3}>
              <Text type="label">{t('admin.dash.activity')}</Text>
              {trend ? <ChartView chart={trend} isInteractive /> : <Text color="secondary" type="supporting">{t('admin.dash.empty')}</Text>}
            </VStack>
          </Card>

          <Card padding={0} width="100%">
            <VStack gap={3} padding={4}>
              <Text type="label">{t('admin.dash.topWorkspaces')}</Text>
              {data.workspaces.length === 0
                ? <Text color="secondary" type="supporting">{t('admin.dash.empty')}</Text>
                : (
                  <Table
                    columns={[
                      {
                        key: 'name', header: t('admin.dash.name'), width: proportional(2),
                        renderCell: (row) => (
                          <HStack gap={2} vAlign="center">
                            <Text type="body">{row.name}</Text>
                            <Token label={row.type} size="sm" />
                          </HStack>
                        ),
                      },
                      {key: 'members', header: t('admin.dash.members'), align: 'end', width: proportional(1)},
                      {key: 'active_users', header: t('admin.dash.activeUsers'), align: 'end', width: proportional(1)},
                      {key: 'runs', header: t('admin.dash.runs'), align: 'end', width: proportional(1)},
                      {key: 'messages', header: t('admin.dash.messages'), align: 'end', width: proportional(1)},
                      {key: 'tool_calls', header: t('admin.dash.toolCalls'), align: 'end', width: proportional(1)},
                      {
                        key: 'last_active_at', header: t('admin.dash.lastActive'), align: 'end', width: proportional(1),
                        renderCell: (row) => row.last_active_at
                          ? <Timestamp format="relative" value={row.last_active_at} />
                          : <Text color="secondary" type="supporting">{t('admin.dash.never')}</Text>,
                      },
                    ]}
                    data={data.workspaces}
                    density="compact"
                    idKey="id"
                  />
                )}
            </VStack>
          </Card>

          <Grid columns={{minWidth: 380}} gap={3}>
            <Card width="100%">
              <VStack gap={3}>
                <Text type="label">{t('admin.dash.topTools')}</Text>
                {tools ? <ChartView chart={tools} isInteractive /> : <Text color="secondary" type="supporting">{t('admin.dash.empty')}</Text>}
              </VStack>
            </Card>
            <Card width="100%">
              <VStack gap={3}>
                <Text type="label">{t('admin.dash.hourly')}</Text>
                {hourly ? <ChartView chart={hourly} isInteractive /> : null}
              </VStack>
            </Card>
            <Card width="100%">
              <VStack gap={3}>
                <Text type="label">{t('admin.dash.runStatus')}</Text>
                {outcomes ? <ChartView chart={outcomes} isInteractive /> : <Text color="secondary" type="supporting">{t('admin.dash.empty')}</Text>}
              </VStack>
            </Card>
            <Card width="100%">
              <VStack gap={3}>
                <Text type="label">{t('admin.dash.models')}</Text>
                {models ? <ChartView chart={models} isInteractive /> : <Text color="secondary" type="supporting">{t('admin.dash.empty')}</Text>}
              </VStack>
            </Card>
          </Grid>

          {/* The same tool figures as a table: a bar says which is busiest, a
              row says how often it failed and how long it took. */}
          <Card padding={0} width="100%">
            <VStack gap={3} padding={4}>
              <Text type="label">{t('admin.dash.toolDetail')}</Text>
              {data.tool_actions.length === 0
                ? <Text color="secondary" type="supporting">{t('admin.dash.empty')}</Text>
                : (
                  <Table
                    columns={[
                      {
                        key: 'name', header: t('admin.dash.topTools'), width: proportional(2),
                        renderCell: (row) => (
                          <HStack gap={2} vAlign="center">
                            <Text type="body">{row.name}</Text>
                            {row.action ? <Text color="secondary" type="supporting">{row.action}</Text> : null}
                          </HStack>
                        ),
                      },
                      {key: 'calls', header: t('admin.dash.calls'), align: 'end', width: proportional(1)},
                      {
                        key: 'failures', header: t('admin.dash.failures'), align: 'end', width: proportional(1),
                        renderCell: (row) => row.failures > 0
                          ? <Badge label={count(row.failures)} variant="error" />
                          : <Text color="secondary" type="supporting">0</Text>,
                      },
                      {key: 'workspaces', header: t('admin.dash.workspaces'), align: 'end', width: proportional(1)},
                      {
                        key: 'avg_ms', header: t('admin.dash.avgTime'), align: 'end', width: proportional(1),
                        renderCell: (row) => <Text type="body">{milliseconds(row.avg_ms)}</Text>,
                      },
                    ]}
                    data={data.tool_actions}
                    density="compact"
                    idKey={(row) => `${row.name}/${row.action ?? ''}`}
                  />
                )}
            </VStack>
          </Card>

          <Grid columns={{minWidth: 380}} gap={3}>
            <Card padding={0} width="100%">
              <VStack gap={0}>
                <VStack padding={4}><Text type="label">{t('admin.dash.topAgents')}</Text></VStack>
                {data.agents.length === 0
                  ? <VStack padding={4}><Text color="secondary" type="supporting">{t('admin.dash.empty')}</Text></VStack>
                  : (
                    <List>
                      {data.agents.map((agent) => (
                        <Item
                          as="li"
                          description={agent.workspace_name}
                          endContent={<Text type="label">{count(agent.runs)}</Text>}
                          key={agent.id}
                          label={agent.name}
                        />
                      ))}
                    </List>
                  )}
              </VStack>
            </Card>
            <Card padding={0} width="100%">
              <VStack gap={0}>
                <VStack padding={4}><Text type="label">{t('admin.dash.inventory')}</Text></VStack>
                <List>
                  <Item as="li" endContent={<Text type="label">{count(data.inventory.workspaces)}</Text>} label={t('admin.dash.workspaces')} />
                  <Item as="li" endContent={<Text type="label">{count(data.inventory.users)}</Text>} label={t('admin.dash.users')} />
                  <Item as="li" endContent={<Text type="label">{count(data.inventory.agents)}</Text>} label={t('admin.dash.agents')} />
                  <Item as="li" endContent={<Text type="label">{count(data.inventory.tools)}</Text>} label={t('admin.dash.tools')} />
                  <Item as="li" endContent={<Text type="label">{count(data.inventory.workflows)}</Text>} label={t('admin.dash.workflows')} />
                  <Item as="li" endContent={<Text type="label">{count(data.inventory.knowledge_bases)}</Text>} label={t('admin.dash.knowledgeBases')} />
                  <Item
                    as="li"
                    description={documentSummary(data)}
                    endContent={<Text type="label">{count(data.inventory.documents)}</Text>}
                    label={t('admin.dash.documents')}
                  />
                </List>
              </VStack>
            </Card>
          </Grid>

          <Card padding={0} width="100%">
            <VStack gap={0}>
              <VStack padding={4}><Text type="label">{t('admin.dash.security')}</Text></VStack>
              <List>
                <Item as="li" endContent={<Text type="label">{count(data.window.sign_ins)}</Text>} label={t('admin.dash.signIns')} />
                <Item
                  as="li"
                  endContent={data.window.failed_sign_ins > 0
                    ? <Badge label={count(data.window.failed_sign_ins)} variant="error" />
                    : <Text type="label">0</Text>}
                  label={t('admin.dash.failedSignIns')}
                />
                <Item as="li" endContent={<Text type="label">{count(data.window.audit_events)}</Text>} label={t('admin.dash.auditEvents')} />
                <Item as="li" endContent={<Text type="label">{seconds(data.window.avg_run_seconds)}</Text>} label={t('admin.dash.avgRun')} />
              </List>
            </VStack>
          </Card>
        </>
      ) : null}

      {!isLoading && !data ? <EmptyState description={t('admin.dash.empty')} title="—" /> : null}
    </VStack>
  );
}

/** The index in a sentence: "412 ready, 3 failed". */
function documentSummary(data: PlatformAnalytics): string {
  return data.document_status.map((item) => `${item.label} ${count(item.count)}`).join(' · ');
}

/**
 * One headline number with the direction it moved.
 *
 * The comparison is against the window immediately before this one, of the same
 * length, which is the only comparison that does not need explaining. A period
 * with nothing before it says so rather than claiming an infinite rise.
 */
function StatTile({label, value, previous, days, isLowerBetter = false}: {
  label: string;
  value: number;
  previous: number;
  days: number;
  isLowerBetter?: boolean;
}) {
  const t = useTranslation();
  const change = previous > 0 ? Math.round(((value - previous) / previous) * 100) : null;
  const isUp = change !== null && change > 0;
  const isGood = isLowerBetter ? !isUp : isUp;
  return (
    <Card width="100%">
      <VStack gap={1}>
        <Text color="secondary" type="supporting">{label}</Text>
        <Text type="display-3">{count(value)}</Text>
        {/* With nothing to compare against, the tile says so and stops. Saying
            "no earlier period" and "vs previous 30 days" in the same breath is
            two answers to one question. */}
        {change === null ? (
          <Text color="secondary" type="supporting">{t('admin.dash.noBaseline')}</Text>
        ) : (
          <HStack gap={2} vAlign="center">
            {change === 0
              ? <Text color="secondary" type="supporting">±0%</Text>
              : <Badge label={`${isUp ? '+' : ''}${change}%`} variant={isGood ? 'success' : 'error'} />}
            <Text color="secondary" type="supporting">{t('admin.dash.vsPrevious', {days})}</Text>
          </HStack>
        )}
      </VStack>
    </Card>
  );
}
