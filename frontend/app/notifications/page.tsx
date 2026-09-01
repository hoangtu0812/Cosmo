'use client';

import {useState} from 'react';
import {Bell} from 'lucide-react';
import {EmptyState} from '@astryxdesign/core/EmptyState';
import {Layout, LayoutContent, VStack} from '@astryxdesign/core/Layout';
import {SegmentedControl, SegmentedControlItem} from '@astryxdesign/core/SegmentedControl';
import {PageHeader} from '../components/PageHeader';
import {useTranslation} from '../lib/i18n';

// Shell only. The filter moves but there is nothing behind it yet.
export default function NotificationsPage() {
  const t = useTranslation();
  const [scope, setScope] = useState('all');

  return (
  <Layout
    content={
      <LayoutContent padding={6}>
        <VStack gap={6} hAlign="center" width="100%">
          <SegmentedControl label={t('notif.scope')} onChange={setScope} size="md" value={scope}>
            <SegmentedControlItem label={t('notif.all')} value="all" />
            <SegmentedControlItem label={t('notif.interactive')} value="interactive" />
            <SegmentedControlItem label={t('notif.workspace')} value="workspace" />
            <SegmentedControlItem label={t('notif.organization')} value="organization" />
            <SegmentedControlItem label={t('nav.schedule')} value="schedule" />
            <SegmentedControlItem label={t('nav.projects')} value="projects" />
          </SegmentedControl>
          <EmptyState description={t('notif.emptyBody')} icon={<Bell size={64} strokeWidth={1} />} title={t('notif.empty')} />
        </VStack>
      </LayoutContent>
    }
    header={<PageHeader description={t('notif.subtitle')} title={t('nav.notification')} />}
    height="fill"
  />
  );
}
