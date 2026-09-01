'use client';

import {ReactNode} from 'react';
import {Card} from '@astryxdesign/core/Card';
import {Grid} from '@astryxdesign/core/Grid';
import {Icon} from '@astryxdesign/core/Icon';
import {IconType} from '@astryxdesign/core/Icon';
import {VStack} from '@astryxdesign/core/Layout';
import {Text} from '@astryxdesign/core/Text';

// A capability nobody has used yet has nothing to list, so the screen explains
// what the capability is for and what it will be able to do. The reference
// opens Tool and Skill this way; ours says the same in the same shape, with
// the action disabled until there is something behind it.
export function CapabilityHero({icon, title, description, action, points}: {
  icon: IconType;
  title: string;
  description: string;
  action: ReactNode;
  points: {icon: IconType; title: string; description: string}[];
}) {
  return (
    <VStack gap={8} hAlign="center" padding={8} width="100%">
      <VStack gap={4} hAlign="center">
        <Icon icon={icon} size="lg" />
        <VStack gap={2} hAlign="center">
          <Text type="large">{title}</Text>
          <Text color="secondary" type="supporting">{description}</Text>
        </VStack>
        {action}
      </VStack>
      <Grid columns={3} gap={4} width="100%">
        {points.map((point) => (
          <Card key={point.title} padding={4}>
            <VStack gap={2}>
              <Icon icon={point.icon} size="md" />
              <Text type="label">{point.title}</Text>
              <Text color="secondary" type="supporting">{point.description}</Text>
            </VStack>
          </Card>
        ))}
      </Grid>
    </VStack>
  );
}
