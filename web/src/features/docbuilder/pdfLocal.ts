import {
  backgroundRGB,
  resolvePageGeometry,
  type DocumentLayoutSettings,
  type ImageDimensions,
  type PageLayoutOverride,
} from '../documentLayout';

export const MAX_IMAGE_PIXELS = 80_000_000;
const rasterExtensions = new Set(['png', 'jpg', 'jpeg']);

export const fileExtension = (name: string) =>
  name.includes('.') ? name.split('.').pop()!.toLowerCase() : '';

export const isRasterName = (name: string) => rasterExtensions.has(fileExtension(name));
export const quarterTurns = (rotation: number) => ((Math.round(rotation / 90) % 4) + 4) % 4;
const swappedByOrientation = (orientation: number) => orientation >= 5 && orientation <= 8;

export async function readExifOrientation(file: File): Promise<number> {
  if (!['jpg', 'jpeg'].includes(fileExtension(file.name))) return 1;
  const bytes = new DataView(await file.slice(0, Math.min(file.size, 128 * 1024)).arrayBuffer());
  if (bytes.byteLength < 4 || bytes.getUint16(0, false) !== 0xffd8) return 1;
  let offset = 2;
  while (offset + 4 <= bytes.byteLength) {
    if (bytes.getUint8(offset) !== 0xff) {
      offset++;
      continue;
    }
    const marker = bytes.getUint8(offset + 1);
    offset += 2;
    if (marker === 0xd8 || marker === 0xd9) continue;
    if (marker === 0xda || offset + 2 > bytes.byteLength) break;
    const length = bytes.getUint16(offset, false);
    const dataStart = offset + 2;
    const dataEnd = offset + length;
    if (
      marker === 0xe1 &&
      dataStart + 8 <= dataEnd &&
      dataEnd <= bytes.byteLength &&
      bytes.getUint8(dataStart) === 0x45 &&
      bytes.getUint8(dataStart + 1) === 0x78 &&
      bytes.getUint8(dataStart + 2) === 0x69 &&
      bytes.getUint8(dataStart + 3) === 0x66 &&
      bytes.getUint8(dataStart + 4) === 0 &&
      bytes.getUint8(dataStart + 5) === 0
    ) {
      const tiff = dataStart + 6;
      const order = bytes.getUint16(tiff, false);
      const little = order === 0x4949;
      if ((little || order === 0x4d4d) && bytes.getUint16(tiff + 2, little) === 42) {
        const ifd = tiff + bytes.getUint32(tiff + 4, little);
        if (ifd + 2 <= dataEnd) {
          const entries = bytes.getUint16(ifd, little);
          for (let index = 0; index < entries; index++) {
            const entry = ifd + 2 + index * 12;
            if (entry + 12 > dataEnd) break;
            if (bytes.getUint16(entry, little) !== 0x0112) continue;
            const type = bytes.getUint16(entry + 2, little);
            const count = bytes.getUint32(entry + 4, little);
            if (type === 3 && count >= 1) {
              const value = bytes.getUint16(entry + 8, little);
              return value >= 1 && value <= 8 ? value : 1;
            }
          }
        }
      }
    }
    if (length < 2) break;
    offset = dataEnd;
  }
  return 1;
}

type DecodedImage = {
  source: CanvasImageSource;
  width: number;
  height: number;
  exifApplied: boolean;
  close: () => void;
};

async function decodeImage(file: File): Promise<DecodedImage> {
  if (typeof createImageBitmap === 'function') {
    try {
      const bitmap = await createImageBitmap(file, { imageOrientation: 'from-image' });
      if (!bitmap.width || !bitmap.height || bitmap.width * bitmap.height > MAX_IMAGE_PIXELS) {
        bitmap.close();
        throw new Error(`${file.name} is too large for browser PDF generation.`);
      }
      return { source: bitmap, width: bitmap.width, height: bitmap.height, exifApplied: true, close: () => bitmap.close() };
    } catch (error) {
      if (error instanceof Error && /too large/.test(error.message)) throw error;
    }
  }
  const url = URL.createObjectURL(file);
  try {
    const image = await new Promise<HTMLImageElement>((resolve, reject) => {
      const element = new Image();
      element.onload = () => resolve(element);
      element.onerror = () => reject(new Error(`${file.name} could not be decoded as an image.`));
      element.src = url;
    });
    const orientation = await readExifOrientation(file);
    const width = image.naturalWidth;
    const height = image.naturalHeight;
    const orientedWidth = swappedByOrientation(orientation) ? height : width;
    const orientedHeight = swappedByOrientation(orientation) ? width : height;
    if (!width || !height || orientedWidth * orientedHeight > MAX_IMAGE_PIXELS) {
      throw new Error(`${file.name} is too large for browser PDF generation.`);
    }
    return { source: image, width, height, exifApplied: false, close: () => undefined };
  } finally {
    URL.revokeObjectURL(url);
  }
}

export async function imageDimensions(file: File): Promise<ImageDimensions> {
  const decoded = await decodeImage(file);
  try {
    if (decoded.exifApplied) return { width: decoded.width, height: decoded.height };
    const orientation = await readExifOrientation(file);
    return swappedByOrientation(orientation)
      ? { width: decoded.height, height: decoded.width }
      : { width: decoded.width, height: decoded.height };
  } finally {
    decoded.close();
  }
}

async function isNativeJPEG(file: File) {
  const header = new Uint8Array(await file.slice(0, 3).arrayBuffer());
  return header.length === 3 && header[0] === 0xff && header[1] === 0xd8 && header[2] === 0xff;
}

function drawExifOrientation(context: CanvasRenderingContext2D, orientation: number, width: number, height: number) {
  switch (orientation) {
    case 2: context.translate(width, 0); context.scale(-1, 1); break;
    case 3: context.translate(width, height); context.rotate(Math.PI); break;
    case 4: context.translate(0, height); context.scale(1, -1); break;
    case 5: context.rotate(Math.PI / 2); context.scale(1, -1); break;
    case 6: context.translate(height, 0); context.rotate(Math.PI / 2); break;
    case 7: context.translate(height, width); context.rotate(Math.PI / 2); context.scale(-1, 1); break;
    case 8: context.translate(0, width); context.rotate(-Math.PI / 2); break;
    default: break;
  }
}

export type PDFImage = { width: number; height: number; bytes: Uint8Array; filter: 'DCTDecode' };

export async function rasterizeForLocalPDF(file: File, rotation: number): Promise<PDFImage> {
  const orientation = await readExifOrientation(file);
  const decoded = await decodeImage(file);
  try {
    if (['jpg', 'jpeg'].includes(fileExtension(file.name)) && orientation === 1 && rotation === 0 && await isNativeJPEG(file)) {
      return { width: decoded.width, height: decoded.height, bytes: new Uint8Array(await file.arrayBuffer()), filter: 'DCTDecode' };
    }
    let source: CanvasImageSource = decoded.source;
    let sourceWidth = decoded.width;
    let sourceHeight = decoded.height;
    if (!decoded.exifApplied && orientation !== 1) {
      const temporary = document.createElement('canvas');
      sourceWidth = swappedByOrientation(orientation) ? decoded.height : decoded.width;
      sourceHeight = swappedByOrientation(orientation) ? decoded.width : decoded.height;
      temporary.width = sourceWidth;
      temporary.height = sourceHeight;
      const context = temporary.getContext('2d');
      if (!context) throw new Error('This browser cannot prepare a PDF canvas.');
      drawExifOrientation(context, orientation, decoded.width, decoded.height);
      context.drawImage(decoded.source, 0, 0);
      source = temporary;
    }
    const rotated = quarterTurns(rotation) % 2 === 1;
    const canvas = document.createElement('canvas');
    canvas.width = rotated ? sourceHeight : sourceWidth;
    canvas.height = rotated ? sourceWidth : sourceHeight;
    if (canvas.width * canvas.height > MAX_IMAGE_PIXELS) {
      throw new Error(`${file.name} is too large for browser PDF generation.`);
    }
    const context = canvas.getContext('2d');
    if (!context) throw new Error('This browser cannot prepare a PDF canvas.');
    context.translate(canvas.width / 2, canvas.height / 2);
    context.rotate(quarterTurns(rotation) * Math.PI / 2);
    context.drawImage(source, -sourceWidth / 2, -sourceHeight / 2);
    const blob = await new Promise<Blob>((resolve, reject) =>
      canvas.toBlob(
        value => value ? resolve(value) : reject(new Error('This browser could not encode the image for PDF export.')),
        'image/jpeg',
        0.95,
      ),
    );
    return { width: canvas.width, height: canvas.height, bytes: new Uint8Array(await blob.arrayBuffer()), filter: 'DCTDecode' };
  } finally {
    decoded.close();
  }
}

export function makeLocalPDF(images: PDFImage[], settings: DocumentLayoutSettings, layouts: PageLayoutOverride[]): Blob {
  const pageIDs = images.map((_, index) => 3 + index * 3);
  const imageIDs = images.map((_, index) => 4 + index * 3);
  const contentIDs = images.map((_, index) => 5 + index * 3);
  const objectCount = 2 + images.length * 3;
  const chunks: ArrayBuffer[] = [];
  const offsets = new Array<number>(objectCount + 1).fill(0);
  let length = 0;
  const text = (value: string) => {
    const encoded = new TextEncoder().encode(value);
    chunks.push(encoded.buffer as ArrayBuffer);
    length += encoded.length;
  };
  const bytes = (value: Uint8Array) => {
    chunks.push(value.buffer.slice(value.byteOffset, value.byteOffset + value.byteLength) as ArrayBuffer);
    length += value.length;
  };
  const object = (id: number, body: string) => {
    offsets[id] = length;
    text(`${id} 0 obj\n${body}\nendobj\n`);
  };
  text('%PDF-1.4\n% 101conv\n');
  object(1, '<< /Type /Catalog /Pages 2 0 R >>');
  object(2, `<< /Type /Pages /Kids [${pageIDs.map(id => `${id} 0 R`).join(' ')}] /Count ${images.length} >>`);
  images.forEach((image, index) => {
    const geometry = resolvePageGeometry({ width: image.width, height: image.height }, 0, settings, layouts[index]);
    const [red, green, blue] = backgroundRGB(geometry.background);
    const background = geometry.background === 'transparent' ? '' : `${red} ${green} ${blue} rg 0 0 ${geometry.pageWidth.toFixed(3)} ${geometry.pageHeight.toFixed(3)} re f\n`;
    const stream = `${background}q ${geometry.drawWidth.toFixed(3)} 0 0 ${geometry.drawHeight.toFixed(3)} ${geometry.drawX.toFixed(3)} ${geometry.drawY.toFixed(3)} cm /Im0 Do Q\n`;
    object(pageIDs[index], `<< /Type /Page /Parent 2 0 R /MediaBox [0 0 ${geometry.pageWidth.toFixed(3)} ${geometry.pageHeight.toFixed(3)}] /Resources << /ProcSet [/PDF /ImageC] /XObject << /Im0 ${imageIDs[index]} 0 R >> >> /Contents ${contentIDs[index]} 0 R >>`);
    offsets[imageIDs[index]] = length;
    text(`${imageIDs[index]} 0 obj\n<< /Type /XObject /Subtype /Image /Width ${image.width} /Height ${image.height} /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /${image.filter} /Length ${image.bytes.length} >>\nstream\n`);
    bytes(image.bytes);
    text(`\nendstream\nendobj\n`);
    object(contentIDs[index], `<< /Length ${new TextEncoder().encode(stream).length} >>\nstream\n${stream}endstream`);
  });
  const xref = length;
  text(`xref\n0 ${objectCount + 1}\n0000000000 65535 f \n`);
  for (let index = 1; index <= objectCount; index++) text(`${String(offsets[index]).padStart(10, '0')} 00000 n \n`);
  text(`trailer\n<< /Size ${objectCount + 1} /Root 1 0 R >>\nstartxref\n${xref}\n%%EOF\n`);
  return new Blob(chunks, { type: 'application/pdf' });
}
