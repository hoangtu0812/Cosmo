'use client';

import {Button} from '@astryxdesign/core/Button';
import {VStack} from '@astryxdesign/core/Layout';

export type ImageSpec = {title: string; src: string};

export function imageFromResult(detail?: string): ImageSpec | null {
  if (!detail || detail.length > 12 * 1024 * 1024) return null;
  try {
    const value = JSON.parse(detail)?.image;
    return value && typeof value.title === 'string' && typeof value.src === 'string'
      && /^data:image\/(png|jpeg|webp|gif);base64,[A-Za-z0-9+/]+=*$/.test(value.src) ? value : null;
  } catch { return null; }
}

export function ImageView({image}: {image: ImageSpec}) {
  function download() {
    const anchor = document.createElement('a');
    anchor.href = image.src;
    anchor.download = `image.${image.src.slice(11, image.src.indexOf(';'))}`;
    anchor.click();
  }
  return <VStack gap={3} width="100%">
    <Button label="Tải ảnh" variant="secondary" size="sm" onClick={download} />
    {/* Native img preserves the generated raster without remote optimization. */}
    {/* eslint-disable-next-line @next/next/no-img-element */}
    <img src={image.src} alt={image.title} className="w-full h-auto rounded-lg" />
  </VStack>;
}
