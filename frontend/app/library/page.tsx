'use client';

import {Archive} from 'lucide-react';
import {EmptyState} from '@astryxdesign/core/EmptyState';
import {Layout, LayoutContent, VStack} from '@astryxdesign/core/Layout';
import {Tab, TabList} from '@astryxdesign/core/TabList';
import {useTranslation} from '../lib/i18n';

// Shell only. The sections that would fill it live in the second column and
// are disabled there for the same reason - see docs/ui_backlog.md.
export default function LibraryPage() {
  const t = useTranslation();

  return (
  <Layout
    content={
      <LayoutContent padding={6}>
        <VStack gap={6} width="100%">
          <TabList aria-label={t('library.sort')} onChange={() => undefined} value="newest">
            <Tab label={t('library.newest')} value="newest" />
          </TabList>
          <EmptyState description={t('library.emptyBody')} icon={<Archive size={64} strokeWidth={1} />} title={t('library.empty')} />
        </VStack>
      </LayoutContent>
    }
    height="fill"
  />
  );
}
