'use client';

import {Clock, Search} from 'lucide-react';
import {Button} from '@astryxdesign/core/Button';
import {EmptyState} from '@astryxdesign/core/EmptyState';
import {HStack, Layout, LayoutContent, VStack} from '@astryxdesign/core/Layout';
import {Icon} from '@astryxdesign/core/Icon';
import {Selector} from '@astryxdesign/core/Selector';
import {TextInput} from '@astryxdesign/core/TextInput';
import {PageHeader} from '../components/PageHeader';
import {useTranslation} from '../lib/i18n';

// Shell only, as with the other areas that have no engine behind them yet.
export default function SchedulePage() {
  const t = useTranslation();

  return (
  <Layout
    content={
      <LayoutContent padding={6}>
        <VStack gap={6} width="100%">
          <HStack gap={2} vAlign="center">
            <TextInput
              isDisabled
              isLabelHidden
              label={t('schedule.search')}
              placeholder={t('schedule.search')}
              size="lg"
              startIcon={<Icon icon={Search} size="sm" />}
              value=""
              width={280}
            />
                <Selector isDisabled isLabelHidden label={t('schedule.types')} onChange={() => undefined} options={[{value: 'all', label: t('schedule.allTypes')}]} size="sm" value="all" />
                <Selector isDisabled isLabelHidden label={t('schedule.statuses')} onChange={() => undefined} options={[{value: 'all', label: t('schedule.allStatuses')}]} size="sm" value="all" />
                <Selector isDisabled isLabelHidden label={t('schedule.targets')} onChange={() => undefined} options={[{value: 'all', label: t('schedule.allTargets')}]} size="sm" value="all" />
              </HStack>
              <EmptyState description={t('schedule.emptyBody')} icon={<Clock size={64} strokeWidth={1} />} title={t('schedule.empty')} />
            </VStack>
          </LayoutContent>
        }
        header={
          <PageHeader
            actions={<Button isDisabled label={t('schedule.new')} size="sm" variant="primary" />}
            count={0}
            description={t('schedule.subtitle')}
            title={t('nav.schedule')}
          />
        }
        height="fill"
      />
  );
}
