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
  // Each card takes its own hue, as the reference does, so three cards in a
  // row read as three things rather than one repeated. The pairs come from the
  // theme: every tinted background has an icon colour built to sit on it.
  const hues = ['blue', 'purple', 'orange'];
  return (
    <VStack gap={8} hAlign="center" padding={8} width="100%">
      <VStack gap={5} hAlign="center">
        <HStack gap={2} vAlign="center">
          {flow.map((step, index) => {
            const isSubject = index === Math.floor(flow.length / 2);
            return (
              <HStack gap={2} key={step.label} vAlign="center">
                {index > 0 ? <Text color="disabled" type="supporting">·</Text> : null}
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
        {points.map((point, index) => {
          const hue = hues[index % hues.length];
          return (
            <Card className={cardHover} key={point.title} padding={5}>
              <VStack gap={3}>
                {/* A round tinted badge, not a banner: fixed size and
                    left-aligned so it does not stretch to the card's width.
                    It grows a little under the card's hover, which is the
                    reference's one flourish here. */}
                <HStack hAlign="start">
                  <VStack
                    className="size-10 rounded-full transition-transform duration-200 group-hover:scale-110"
                    hAlign="center"
                    style={{backgroundColor: `var(--color-background-${hue})`}}
                    vAlign="center"
                  >
                    <Icon icon={point.icon} size="md" style={{color: `var(--color-icon-${hue})`}} />
                  </VStack>
                </HStack>
                <Text type="label">{point.title}</Text>
                <Text color="secondary" type="supporting">{point.description}</Text>
              </VStack>
            </Card>
          );
        })}
      </Grid>
    </VStack>
  );
}
