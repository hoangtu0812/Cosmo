'use client';

import {ReactNode} from 'react';
import {HStack, LayoutHeader, VStack} from '@astryxdesign/core/Layout';
import {Text} from '@astryxdesign/core/Text';
import {Toolbar} from '@astryxdesign/core/Toolbar';

// Every area opens the same way in the reference: what you are looking at,
// how much of it there is, one line saying what it is for, and the actions
// that create more of it. Keeping that in one place stops each screen from
// inventing its own header.
export function PageHeader({title, count, description, actions}: {
  title: string;
  count?: number;
  description?: string;
  actions?: ReactNode;
}) {
  return (
    <LayoutHeader hasDivider>
      <Toolbar
        endContent={actions}
        label={title}
        startContent={
          <VStack gap={0}>
            <HStack gap={2} vAlign="center">
              <Text type="label">{title}</Text>
              {count === undefined ? null : <Text color="secondary" type="supporting">{count}</Text>}
            </HStack>
            {description ? <Text color="secondary" type="supporting">{description}</Text> : null}
          </VStack>
        }
      />
    </LayoutHeader>
  );
}
