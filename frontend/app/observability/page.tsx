'use client';

import {useState} from 'react';
import {LineChart} from 'lucide-react';
import {Card} from '@astryxdesign/core/Card';
import {Grid} from '@astryxdesign/core/Grid';
import {Icon} from '@astryxdesign/core/Icon';
import {HStack, Layout, LayoutContent, VStack} from '@astryxdesign/core/Layout';
import {SegmentedControl, SegmentedControlItem} from '@astryxdesign/core/SegmentedControl';
import {Tab, TabList} from '@astryxdesign/core/TabList';
import {Text} from '@astryxdesign/core/Text';
import {PageHeader} from '../components/PageHeader';
import {useTranslation} from '../lib/i18n';

// Shell only - see docs/ui_backlog.md. Runs are already recorded; what is
// missing is the token, cost and latency telemetry to fill these in, so every
// figure reads as unknown rather than as zero.
export default function ObservabilityPage() {
  const t = useTranslation();
  const [audience, setAudience] = useState('application');
  const [subject, setSubject] = useState('agents');
  const [range, setRange] = useState('7d');

  const figures = [t('observe.conversations'), t('observe.runs'), t('observe.tokens'), t('observe.cost')];
  const charts = [t('observe.conversations'), t('observe.tokens')];

  return (
    <Layout
      content={
        <LayoutContent padding={6}>
          <VStack gap={6} width="100%">
            <HStack gap={4} vAlign="center" wrap="wrap">
              <TabList onChange={setSubject} value={subject}>
                <Tab label={t('agent.title')} value="agents" />
                <Tab label={t('nav.workflow')} value="workflows" />
              </TabList>
              <SegmentedControl label={t('observe.range')} onChange={setRange} size="sm" value={range}>
                <SegmentedControlItem label={t('observe.range7d')} value="7d" />
                <SegmentedControlItem label={t('observe.range1m')} value="1m" />
                <SegmentedControlItem label={t('observe.range3m')} value="3m" />
                <SegmentedControlItem label={t('observe.range1y')} value="1y" />
              </SegmentedControl>
            </HStack>

            <VStack gap={3} width="100%">
              <Text type="label">{t('observe.usage')}</Text>
              <Grid columns={{minWidth: 220, max: 4}} gap={4} width="100%">
                {figures.map((figure) => (
                  <Card key={figure} padding={4}>
                    <VStack gap={1}>
                      <Text color="secondary" type="supporting">{figure}</Text>
                      <Text size="xl" type="large">—</Text>
                    </VStack>
                  </Card>
                ))}
              </Grid>
            </VStack>

            <VStack gap={3} width="100%">
              <Text type="label">{t('observe.trends')}</Text>
              <Grid columns={{minWidth: 420, max: 2}} gap={4} width="100%">
                {charts.map((chart) => (
                  <Card key={chart} padding={4}>
                    <VStack gap={4}>
                      <HStack gap={2} vAlign="center">
                        <Text type="label">{chart}</Text>
                        <Text color="secondary" type="supporting">{t('observe.range7d')}</Text>
                      </HStack>
                      <VStack gap={2} hAlign="center" height={220} vAlign="center" width="100%">
                        <Icon icon={LineChart} size="lg" />
                        <Text color="secondary" type="supporting">{t('observe.noSeries')}</Text>
                      </VStack>
                    </VStack>
                  </Card>
                ))}
              </Grid>
            </VStack>
          </VStack>
        </LayoutContent>
      }
      header={
        <PageHeader
          actions={
            <SegmentedControl label={t('observe.audience')} onChange={setAudience} size="sm" value={audience}>
              <SegmentedControlItem label={t('observe.application')} value="application" />
              <SegmentedControlItem label={t('observe.member')} value="member" />
            </SegmentedControl>
          }
          description={t('observe.subtitle')}
          title={t('nav.observability')}
        />
      }
      height="fill"
    />
  );
}
