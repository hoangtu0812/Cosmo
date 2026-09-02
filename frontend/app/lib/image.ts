// Avatars and workspace icons are stored as a small square, and the upload
// endpoints take raw base64 rather than a file. Doing the crop and the encode
// in the browser keeps a 7MB photo from ever being sent.
//
// This lived in three screens as three copies; it is one function now.
export async function resizeToSquare(file: File, size = 128): Promise<{mime: string; data: string}> {
  const bitmap = await createImageBitmap(file);
  const canvas = document.createElement('canvas');
  canvas.width = size;
  canvas.height = size;
  const context = canvas.getContext('2d');
  if (!context) throw new Error('Canvas unavailable');
  // Crop to the centre square first, so a wide photo is not squashed.
  const side = Math.min(bitmap.width, bitmap.height);
  context.drawImage(bitmap, (bitmap.width - side) / 2, (bitmap.height - side) / 2, side, side, 0, 0, size, size);
  bitmap.close();
  const blob: Blob = await new Promise((resolve, reject) => {
    canvas.toBlob((result) => (result ? resolve(result) : reject(new Error('Encode failed'))), 'image/png');
  });
  const bytes = new Uint8Array(await blob.arrayBuffer());
  let binary = '';
  bytes.forEach((byte) => { binary += String.fromCharCode(byte); });
  return {mime: 'image/png', data: btoa(binary)};
}
