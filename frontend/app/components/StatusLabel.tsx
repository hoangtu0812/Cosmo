'use client';

import {HStack} from '@astryxdesign/core/Layout';
import {StatusDot} from '@astryxdesign/core/StatusDot';
import {Text} from '@astryxdesign/core/Text';

type Variant = 'success' | 'warning' | 'error' | 'accent' | 'neutral';

// StatusDot paints a dot and nothing else: its label is the accessible name,
// not visible text. A state still has to be readable, so the two travel
// together. Using this everywhere keeps a status from silently becoming a
// bare dot again.
export function StatusLabel({label, variant = 'neutral', isPulsing}: {
  label: string;
  variant?: Variant;
  isPulsing?: boolean;
}) {
  return (
    <HStack gap={1} vAlign="center">
      <StatusDot isPulsing={isPulsing} label={label} variant={variant} />
      <Text color="secondary" type="supporting">{label}</Text>
    </HStack>
  );
}
