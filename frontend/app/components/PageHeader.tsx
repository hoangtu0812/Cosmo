'use client';

import {ReactNode} from 'react';
import {CircleHelp} from 'lucide-react';
import {Button} from '@astryxdesign/core/Button';
import {Icon} from '@astryxdesign/core/Icon';
import {HStack, LayoutHeader, VStack} from '@astryxdesign/core/Layout';
import {Text} from '@astryxdesign/core/Text';
import {Toolbar} from '@astryxdesign/core/Toolbar';
import {useTranslation} from '../lib/i18n';

// Every area opens the same way in the reference: what you are looking at,
// how much of it there is, one line saying what it is for, and the actions
// that create more of it. Keeping that in one place stops each screen from
// inventing its own header.
export function PageHeader({title, count, description, actions, hasIntroduction}: {
  title: string;
  count?: number;
  description?: string;
  actions?: ReactNode;
  /** The reference offers a short tour of an area beside its name. Ours has
      the affordance but nothing behind it yet - see docs/ui_backlog.md. */
  hasIntroduction?: boolean;
}) {
  const t = useTranslation();
  return (
    <LayoutHeader hasDivider>
      <Toolbar
        endContent={actions}
        label={title}
        startContent={
          <VStack gap={0}>
            <HStack gap={2} vAlign="center">
              <Text size="xl" type="large">{title}</Text>
              {count === undefined ? null : <Text color="secondary" type="supporting">{count}</Text>}
              {hasIntroduction ? <Button icon={<Icon icon={CircleHelp} size="sm" />} isDisabled label={t('nav.introduction')} size="sm" variant="ghost" /> : null}
            </HStack>
            {description ? <Text color="secondary" type="supporting">{description}</Text> : null}
          </VStack>
        }
      />
    </LayoutHeader>
  );
}
