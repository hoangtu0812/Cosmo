import {createAvatar} from '@dicebear/core';
import {notionists, bottts} from '@dicebear/collection';

/**
 * Illustrated avatars, generated here rather than shipped as artwork.
 *
 * The reference offers a set of drawn characters. Those are its own artwork
 * and do not travel with it, so these come from DiceBear instead: `notionists`
 * and `bottts` are drawn by their authors and released CC0 and
 * free-for-commercial-use respectively, and every avatar is produced in the
 * browser from a seed - nothing is fetched, and no request leaves the machine.
 *
 * A chosen avatar is turned into a PNG and handed to the existing upload path,
 * so it becomes an ordinary agent image with no new storage or rendering to
 * maintain. The seed only has to be stable long enough for the grid to look
 * the same each time it opens.
 */

export type CharacterGroup = {
  id: 'people' | 'robots';
  seeds: string[];
};

// Fixed seeds, so the grid is the same set every time rather than a lottery.
// Names are arbitrary - they are hashed - but keeping them readable makes a
// missing or duplicated one obvious in review.
export const CHARACTER_GROUPS: CharacterGroup[] = [
  {
    id: 'people',
    seeds: [
      'Aneka', 'Bao', 'Chi', 'Dung', 'Emery', 'Felix', 'Giang',
      'Hoa', 'Ivy', 'Jules', 'Khanh', 'Linh', 'Mai', 'Nam',
      'Oanh', 'Phuc', 'Quyen', 'Robin', 'Son', 'Trang', 'Uyen',
    ],
  },
  {
    id: 'robots',
    seeds: ['Atlas', 'Bolt', 'Circuit', 'Dynamo', 'Ember', 'Flux', 'Gizmo'],
  },
];

/** The avatar as an inline SVG data URI, for the grid and the preview. */
export function characterDataURI(group: CharacterGroup['id'], seed: string): string {
  // Each style carries its own options type, so the call is made inside the
  // branch rather than choosing a style first and widening both to nothing.
  const options = {seed, size: 96, radius: 50};
  return group === 'robots'
    ? createAvatar(bottts, options).toDataUri()
    : createAvatar(notionists, options).toDataUri();
}

/**
 * The chosen avatar as a PNG file, so it can go through the same upload path
 * as a picture from disk. Rasterised rather than sent as SVG because the
 * server accepts raster types only - and because an SVG from a generator is
 * markup the server would then have to be careful with.
 */
export async function characterFile(group: CharacterGroup['id'], seed: string): Promise<File> {
  const source = characterDataURI(group, seed);
  const image = new Image();
  image.src = source;
  await image.decode();

  const canvas = document.createElement('canvas');
  canvas.width = 256;
  canvas.height = 256;
  const context = canvas.getContext('2d');
  if (!context) throw new Error('canvas unavailable');
  context.drawImage(image, 0, 0, canvas.width, canvas.height);

  const blob = await new Promise<Blob | null>((resolve) => canvas.toBlob(resolve, 'image/png'));
  if (!blob) throw new Error('could not render the avatar');
  return new File([blob], `${group}-${seed}.png`, {type: 'image/png'});
}
