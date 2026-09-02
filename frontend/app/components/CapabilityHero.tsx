'use client';

import {ReactNode} from 'react';
import {Card} from '@astryxdesign/core/Card';
import {Grid} from '@astryxdesign/core/Grid';
import {HStack, VStack} from '@astryxdesign/core/Layout';
import {Icon, IconType} from '@astryxdesign/core/Icon';
import {Section} from '@astryxdesign/core/Section';
import {Text} from '@astryxdesign/core/Text';
import {cardHover} from './motion';

// A capability nobody has used yet has nothing to list, so the screen explains
// what the capability is for and what it will be able to do. The reference
// opens Tool and Skill this way; ours says the same in the same shape, with
// the action disabled until there is something behind it.
export function CapabilityHero({flow, title, description, action, points}: {
  // Three tiles reading left to right, the middle one being the capability
  // itself. The reference opens every one of these screens this way, because
  // a capability is defined by what sits either side of it - and a single
  // icon cannot say that.
  flow: {icon: IconType; label: string}[];
  title: string;
  description: string;
  action: ReactNode;
  points: {icon: IconType; title: string; description: string}[];
}) {
  return (
    <VStack gap={8} hAlign="center" padding={8} width="100%">
      <VStack gap={5} hAlign="center">
        <HStack gap={2} vAlign="center">
          {flow.map((step, index) => {
            const isSubject = index === Math.floor(flow.length / 2);
            return (
              <HStack gap={2} key={step.label} vAlign="center">
                {index > 0 ? <Text color="secondary" type="supporting">→</Text> : null}
                <VStack gap={1} hAlign="center">
                  {/* The subject sits on a raised card between two flat ones,
                      so the eye lands on it first. */}
                  {isSubject ? (
                    <Card padding={5}><Icon icon={step.icon} size="lg" /></Card>
                  ) : (
                    <Section padding={4} variant="muted"><Icon icon={step.icon} size="md" /></Section>
                  )}
                  <Text color="secondary" type="supporting">{step.label}</Text>
                </VStack>
              </HStack>
            );
          })}
        </HStack>
        <VStack gap={2} hAlign="center" maxWidth={560}>
          <Text type="large">{title}</Text>
          <Text color="secondary" type="supporting">{description}</Text>
        </VStack>
        {action}
      </VStack>
      <Grid columns={3} gap={4} maxWidth={760} width="100%">
        {points.map((point) => (
          <Card className={cardHover} key={point.title} padding={4}>
            <VStack gap={2}>
              {/* The tile is a badge, not a banner: left-aligned so it does
                  not stretch to the card's width. */}
              <HStack hAlign="start">
                <Section padding={2} variant="muted"><Icon icon={point.icon} size="md" /></Section>
              </HStack>
              <Text type="label">{point.title}</Text>
              <Text color="secondary" type="supporting">{point.description}</Text>
            </VStack>
          </Card>
        ))}
      </Grid>
    </VStack>
  );
}
