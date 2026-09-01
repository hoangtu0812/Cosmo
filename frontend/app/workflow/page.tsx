'use client';

import {Search, Workflow} from 'lucide-react';
import {Button} from '@astryxdesign/core/Button';
import {EmptyState} from '@astryxdesign/core/EmptyState';
import {Icon} from '@astryxdesign/core/Icon';
import {Layout, LayoutContent, VStack} from '@astryxdesign/core/Layout';
import {TextInput} from '@astryxdesign/core/TextInput';
import {PageHeader} from '../components/PageHeader';
import {useTranslation} from '../lib/i18n';

// Shell only - see docs/ui_backlog.md. Laid out like the agent list it will
// sit beside, so the two read as the same kind of thing.
export default function WorkflowPage() {
  const t = useTranslation();

  return (
    <Layout
      content={
        <LayoutContent padding={6}>
          <VStack gap={6} width="100%">
            <TextInput
              isDisabled
              isLabelHidden
              label={t('workflow.search')}
              placeholder={t('workflow.search')}
              size="lg"
              startIcon={<Icon icon={Search} size="sm" />}
              value=""
              width={280}
            />
            <EmptyState description={t('workflow.emptyBody')} icon={<Workflow size={64} strokeWidth={1} />} title={t('workflow.empty')} />
          </VStack>
        </LayoutContent>
      }
      header={
        <PageHeader
          actions={<Button isDisabled label={t('workflow.new')} size="sm" variant="primary" />}
          count={0}
          hasIntroduction
          description={t('workflow.subtitle')}
          title={t('nav.workflow')}
        />
      }
      height="fill"
    />
  );
}
