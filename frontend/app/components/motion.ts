/**
 * The motion the reference uses, in one place so the numbers cannot drift
 * apart between screens.
 *
 * Measured from the reference rather than invented: a card lifts 2px over
 * 200ms, a cover image scales to 1.05 over 100ms, and everything is a plain
 * ease. Three speeds, each doing one job - anything slower reads as lag on a
 * list you are scanning, anything faster is not seen at all.
 *
 * These are class strings rather than component props because a hover
 * transform is not something Astryx exposes as a prop. Colours still come
 * from theme tokens, never literals.
 *
 * Tailwind v4 writes these to the `translate` and `scale` properties rather
 * than to `transform`, which is worth knowing before concluding they do
 * nothing: `getComputedStyle(el).transform` stays `none` while the card is
 * lifting.
 */

/**
 * A card in a list: lifts slightly and gains a shadow under the pointer. It
 * carries `group` so a cover inside it can answer to the same hover.
 */
export const cardHover =
  'group transition-all duration-200 ease-in-out hover:-translate-y-0.5 hover:shadow-[var(--shadow-med)]';

/**
 * The image on a card, which zooms inside its frame rather than moving with
 * it. `group-hover` means it answers to the card, so the two read as one
 * gesture; the frame needs `overflow-hidden` for the zoom to be contained.
 */
export const coverFrame = 'overflow-hidden';
export const coverZoom = 'transition-transform duration-100 ease-in-out group-hover:scale-105';
