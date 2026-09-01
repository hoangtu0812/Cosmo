'use client';

import {useState} from 'react';
import {FolderKanban, Inbox, Search} from 'lucide-react';
import {Button} from '@astryxdesign/core/Button';
import {EmptyState} from '@astryxdesign/core/EmptyState';
import {HStack, Layout, LayoutContent, VStack} from '@astryxdesign/core/Layout';
import {SegmentedControl, SegmentedControlItem} from '@astryxdesign/core/SegmentedControl';
import {TextInput} from '@astryxdesign/core/TextInput';
import {Icon} from '@astryxdesign/core/Icon';
import {PageHeader} from '../components/PageHeader';
import {useTranslation} from '../lib/i18n';

// The shape of this area exists before the feature behind it does, so the
// product's outline is visible while it is being built. Everything that would
// change data is disabled - see docs/ui_backlog.md.
export default function ProjectsPage() {
  const t = useTranslation();
  const [scope, setScope] = useState('active');

  return (
  <Layout
    content={
      <LayoutContent padding={6}>
        <VStack gap={6} width="100%">
          <HStack gap={2} vAlign="center">
            <TextInput
              isDisabled
              isLabelHidden
              label={t('projects.search')}
              placeholder={t('projects.search')}
              size="lg"
              startIcon={<Icon icon={Search} size="sm" />}
              value=""
              width={280}
            />
                <SegmentedControl label={t('projects.scope')} onChange={setScope} size="md" value={scope}>
                  <SegmentedControlItem label={t('projects.active')} value="active" />
                  <SegmentedControlItem label={t('projects.archived')} value="archived" />
                </SegmentedControl>
              </HStack>
              <EmptyState description={t('projects.emptyBody')} icon={<FolderKanban size={64} strokeWidth={1} />} title={t('projects.empty')} />
            </VStack>
          </LayoutContent>
        }
        header={
          <PageHeader
            actions={
              <HStack gap={2} vAlign="center">
                <Button icon={<Icon icon={Inbox} size="sm" />} isDisabled label={t('projects.inbox')} size="sm" variant="ghost" />
                <Button isDisabled label={t('projects.new')} size="sm" variant="primary" />
              </HStack>
            }
            count={0}
            description={t('projects.subtitle')}
            title={t('nav.projects')}
          />
        }
        height="fill"
      />
  );
}
