'use client';

import {ReactNode} from 'react';
import {HStack} from '@astryxdesign/core/Layout';
import {Divider} from '@astryxdesign/core/Divider';

/**
 * One of the floating bars over the canvas.
 *
 * Measured from the reference: 40px tall, 8px corners, white, a hairline
 * border and no shadow. No shadow is the part worth keeping - the canvas is
 * already a busy surface, and a raised bar over a dotted ground reads as
 * clutter where a drawn edge reads as an edge.
 *
 * They float rather than sitting in a header because the canvas is the page:
 * a header would take a strip of drawing room the whole time it is open.
 */
export function CanvasToolbar({children}: {children: ReactNode}) {
  return (
    <HStack
      className="h-10 rounded-lg border border-[var(--color-border)] bg-[var(--color-background-surface)] px-1.5"
      gap={1}
      vAlign="center"
    >
      {children}
    </HStack>
  );
}

/** The hairline between groups inside one bar. */
export function ToolbarDivider() {
  return <Divider orientation="vertical" />;
}
