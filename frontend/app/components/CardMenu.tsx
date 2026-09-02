'use client';

import {ReactNode} from 'react';
import {MoreHorizontal} from 'lucide-react';
import {ContextMenu, ContextMenuOption} from '@astryxdesign/core/ContextMenu';
import {DropdownMenu} from '@astryxdesign/core/DropdownMenu';
import {HStack} from '@astryxdesign/core/Layout';

/**
 * The actions on a card, reachable two ways from one list.
 *
 * The ⋯ button is what a reader finds; right-click is what they use once they
 * know. Both read the same array, so the two can never come to say different
 * things about the same card - which is the only reason this is a component
 * and not two call sites.
 *
 * Astryx's own types agree: ContextMenuOption is DropdownMenuOption, so the
 * list needs no translation between them.
 */
export type CardMenuItems = ContextMenuOption[];

/** Wraps a whole card so right-click anywhere on it opens the menu. */
export function CardContextMenu({items, label, children}: {
  items: CardMenuItems;
  label: string;
  children: ReactNode;
}) {
  return (
    <ContextMenu className="w-full" items={items} label={label} menuWidth={220}>
      {children}
    </ContextMenu>
  );
}

/**
 * The ⋯ itself.
 *
 * A card is usually a link, so a click here reaches the card too and the
 * navigation wins the race - the menu opens and is gone before it draws. The
 * wrapper stops the click at the button, which is the whole reason it exists.
 */
export function CardMenuButton({items, label}: {items: CardMenuItems; label: string}) {
  return (
    <HStack onClick={(event) => event.stopPropagation()}>
      <DropdownMenu
        alignment="end"
        button={{icon: <MoreHorizontal size={15} />, isIconOnly: true, label, size: 'sm', variant: 'ghost'}}
        hasChevron={false}
        items={items}
      />
    </HStack>
  );
}
