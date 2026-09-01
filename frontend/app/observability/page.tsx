'use client';

import {BarChart3} from 'lucide-react';
import {EmptyState} from '@astryxdesign/core/EmptyState';
import {Layout, LayoutContent, VStack} from '@astryxdesign/core/Layout';
import {PageHeader} from '../components/PageHeader';
import {useTranslation} from '../lib/i18n';

// Shell only - see docs/ui_backlog.md. Runs are already recorded; what is
// missing is the token, cost and latency telemetry to chart them by.
export default function ObservabilityPage() {
  const t = useTranslation();

  return (
    <Layout
      content={
        <LayoutContent padding={6}>
          <VStack gap={6} width="100%">
            <EmptyState description={t('observe.emptyBody')} icon={<BarChart3 size={64} strokeWidth={1} />} title={t('observe.empty')} />
          </VStack>
        </LayoutContent>
      }
      header={<PageHeader description={t('observe.subtitle')} title={t('nav.observability')} />}
      height="fill"
    />
  );
}
