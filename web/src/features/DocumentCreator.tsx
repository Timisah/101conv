import { useEffect, useMemo, useState } from 'react';
import { ArrowDown, ArrowUp, Copy, Eye, File, FileOutput, FilePlus2, LoaderCircle, Redo2, RotateCw, Trash2, Undo2, UploadCloud, X } from 'lucide-react';
import { UploadArea, selectFiles } from '../components/UploadArea';
import { uploadWithProgress, type Session } from './session';
import {
  DEFAULT_LAYOUT,
  backgroundRGB,
  layoutLabel,
  mergeLayoutSettings,
  resolvePageGeometry,
  type DocumentLayoutSettings,
  type ImageDimensions,
  type LayoutMode,
  type PageLayoutOverride,
} from './documentLayout';

type StoredUpload = { id: string; fileName: string; extension: string; size: number };
type PageDraft = { id: string; file: File; rotation: number; layout?: PageLayoutOverride; upload?: StoredUpload };
type HistoryState = { pages: PageDraft[]; layout: DocumentLayoutSettings };

const MAX_SOURCES = 50;
const MAX_IMAGE_PIXELS = 80_000_000;
const extension = (name: string) => name.includes('.') ? name.split('.').pop()!.toLowerCase() : '';
const formatBytes = (size: number) => size < 1024 * 1024 ? `${Math.round(size / 1024)} KB` : `${(size / 1024 / 1024).toFixed(1)} MB`;
const pageID = () => globalThis.crypto?.randomUUID ? globalThis.crypto.randomUUID() : `page-${Date.now()}-${Math.random().toString(16).slice(2)}`;
const rasterExtensions = new Set(['png', 'jpg', 'jpeg']);
const isRaster = (file: File) => rasterExtensions.has(extension(file.name));
const quarterTurns = (rotation: number) => ((Math.round(rotation / 90) % 4) + 4) % 4;
const swappedByOrientation = (orientation: number) => orientation >= 5 && orientation <= 8;

function useObjectURL(file: File) {
  const [url, setUrl] = useState('');
  useEffect(() => {
    const next = URL.createObjectURL(file);
    setUrl(next);
    return () => URL.revokeObjectURL(next);
  }, [file]);
  return url;
}

async function readExifOrientation(file: File) {
  if (!['jpg', 'jpeg'].includes(extension(file.name))) return 1;
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
    if (marker === 0xe1 && dataStart + 8 <= dataEnd && dataEnd <= bytes.byteLength &&
      bytes.getUint8(dataStart) === 0x45 && bytes.getUint8(dataStart + 1) === 0x78 &&
      bytes.getUint8(dataStart + 2) === 0x69 && bytes.getUint8(dataStart + 3) === 0x66 &&
      bytes.getUint8(dataStart + 4) === 0 && bytes.getUint8(dataStart + 5) === 0) {
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

type DecodedImage = { source: CanvasImageSource; width: number; height: number; exifApplied: boolean; close: () => void };

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
    if (!width || !height || orientedWidth * orientedHeight > MAX_IMAGE_PIXELS) throw new Error(`${file.name} is too large for browser PDF generation.`);
    return { source: image, width, height, exifApplied: false, close: () => undefined };
  } finally {
    URL.revokeObjectURL(url);
  }
}

async function imageDimensions(file: File): Promise<ImageDimensions> {
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

function useImageDimensions(file: File) {
  const [dimensions, setDimensions] = useState<ImageDimensions>();
  useEffect(() => {
    let live = true;
    setDimensions(undefined);
    void imageDimensions(file).then(value => { if (live) setDimensions(value); }).catch(() => { if (live) setDimensions(undefined); });
    return () => { live = false; };
  }, [file]);
  return dimensions;
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

function PageVisual({ page, settings, large = false }: { page: PageDraft; settings: DocumentLayoutSettings; large?: boolean }) {
  const url = useObjectURL(page.file);
  const dimensions = useImageDimensions(page.file);
  const ext = extension(page.file.name);
  if (!isRaster(page.file)) {
    if (ext === 'pdf' && large) return <iframe className="document-preview-frame" src={url} title={`Preview of ${page.file.name}`} />;
    return <div className="document-file-placeholder"><File size={large ? 34 : 22} /><b>{ext.toUpperCase() || 'FILE'}</b><span>{large ? 'Preview available after export' : 'Preview'}</span></div>;
  }
  if (!dimensions) return <div className="document-file-placeholder"><LoaderCircle className="spin" size={large ? 28 : 18} /><span>Loading preview</span></div>;
  const geometry = resolvePageGeometry(dimensions, page.rotation, settings, page.layout);
  const rotated = quarterTurns(page.rotation) % 2 === 1;
  const unrotatedWidth = rotated ? geometry.drawHeight : geometry.drawWidth;
  const unrotatedHeight = rotated ? geometry.drawWidth : geometry.drawHeight;
  const imageStyle = {
    position: 'absolute' as const,
    left: `${(geometry.drawX + geometry.drawWidth / 2) / geometry.pageWidth * 100}%`,
    top: `${(geometry.drawY + geometry.drawHeight / 2) / geometry.pageHeight * 100}%`,
    width: `${unrotatedWidth / geometry.pageWidth * 100}%`,
    height: `${unrotatedHeight / geometry.pageHeight * 100}%`,
    maxWidth: 'none',
    maxHeight: 'none',
    transform: `translate(-50%, -50%) rotate(${page.rotation}deg)`,
    objectFit: 'fill' as const,
  };
  const surfaceClass = `document-page-surface${large ? ' large' : ''}${geometry.background === 'transparent' ? ' transparent' : ''}`;
  return <div className={surfaceClass} style={{ aspectRatio: `${geometry.pageWidth} / ${geometry.pageHeight}`, backgroundColor: geometry.background === 'black' ? '#000' : geometry.background === 'white' ? '#fff' : undefined }}><img className="document-layout-image" src={url} alt={`Preview of ${page.file.name}`} style={imageStyle} /></div>;
}

type PDFImage = { width: number; height: number; bytes: Uint8Array; filter: 'DCTDecode' };

async function rasterize(file: File, rotation: number): Promise<PDFImage> {
  const orientation = await readExifOrientation(file);
  const decoded = await decodeImage(file);
  try {
    if (['jpg', 'jpeg'].includes(extension(file.name)) && orientation === 1 && rotation === 0 && await isNativeJPEG(file)) {
      return { width: decoded.width, height: decoded.height, bytes: new Uint8Array(await file.arrayBuffer()), filter: 'DCTDecode' };
    }
    let source: CanvasImageSource = decoded.source;
    let sourceWidth = decoded.width;
    let sourceHeight = decoded.height;
    let temporary: HTMLCanvasElement | undefined;
    if (!decoded.exifApplied && orientation !== 1) {
      temporary = document.createElement('canvas');
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
    if (canvas.width * canvas.height > MAX_IMAGE_PIXELS) throw new Error(`${file.name} is too large for browser PDF generation.`);
    const context = canvas.getContext('2d');
    if (!context) throw new Error('This browser cannot prepare a PDF canvas.');
    context.translate(canvas.width / 2, canvas.height / 2);
    context.rotate(quarterTurns(rotation) * Math.PI / 2);
    context.drawImage(source, -sourceWidth / 2, -sourceHeight / 2);
    const blob = await new Promise<Blob>((resolve, reject) => canvas.toBlob(value => value ? resolve(value) : reject(new Error('This browser could not encode the image for PDF export.')), 'image/jpeg', 0.95));
    return { width: canvas.width, height: canvas.height, bytes: new Uint8Array(await blob.arrayBuffer()), filter: 'DCTDecode' };
  } finally {
    decoded.close();
  }
}

function makePDF(images: PDFImage[], settings: DocumentLayoutSettings, layouts: PageLayoutOverride[]) {
  const pageIDs = images.map((_, index) => 3 + index * 3);
  const imageIDs = images.map((_, index) => 4 + index * 3);
  const contentIDs = images.map((_, index) => 5 + index * 3);
  const objectCount = 2 + images.length * 3;
  const chunks: ArrayBuffer[] = [];
  const offsets = new Array<number>(objectCount + 1).fill(0);
  let length = 0;
  const text = (value: string) => { const encoded = new TextEncoder().encode(value); chunks.push(encoded.buffer as ArrayBuffer); length += encoded.length; };
  const bytes = (value: Uint8Array) => { chunks.push(value.buffer.slice(value.byteOffset, value.byteOffset + value.byteLength) as ArrayBuffer); length += value.length; };
  const object = (id: number, body: string) => { offsets[id] = length; text(`${id} 0 obj\n${body}\nendobj\n`); };
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

function PageLayoutSelect({ page, settings, onChange }: { page: PageDraft; settings: DocumentLayoutSettings; onChange: (value: string) => void }) {
  return <label className="page-layout-control">Layout<select aria-label={`Layout for ${page.file.name}`} value={page.layout?.mode || 'inherit'} onChange={event => onChange(event.target.value)}><option value="inherit">Document default</option><option value="automatic">Automatic</option><option value="fit">Fit to page</option><option value="fill">Fill page</option></select><small>{layoutLabel(settings, page.layout)}</small></label>;
}

export function DocumentCreator({ notify, session }: { notify: (message: string) => void; session: Session }) {
  const [pages, setPages] = useState<PageDraft[]>([]);
  const [layout, setLayout] = useState<DocumentLayoutSettings>(DEFAULT_LAYOUT);
  const [past, setPast] = useState<HistoryState[]>([]);
  const [future, setFuture] = useState<HistoryState[]>([]);
  const [message, setMessage] = useState('');
  const [projectName, setProjectName] = useState('Untitled document');
  const [busy, setBusy] = useState(false);
  const [progress, setProgress] = useState(0);
  const [previewOpen, setPreviewOpen] = useState(false);

  const canGeneratePDF = pages.length > 0 && pages.every(page => isRaster(page.file));
  const unsupportedForPDF = pages.find(page => !isRaster(page.file));
  const snapshot = (): HistoryState => ({ pages, layout });
  const update = (nextPages: PageDraft[] = pages, nextLayout: DocumentLayoutSettings = layout) => {
    setPast(current => [...current.slice(-49), snapshot()]);
    setPages(nextPages);
    setLayout(nextLayout);
    setFuture([]);
  };

  const choose = (incoming: File[]) => {
    const selected = selectFiles(pages.map(page => page.file), incoming, MAX_SOURCES);
    setMessage(selected.message);
    if (selected.files.length !== pages.length) {
      const existing = pages.map(page => page.file);
      const additions = selected.files.slice(existing.length).map(file => ({ id: pageID(), file, rotation: 0 }));
      update([...pages, ...additions]);
    }
  };

  const move = (index: number, delta: number) => {
    const target = index + delta;
    if (target < 0 || target >= pages.length) return;
    const next = [...pages];
    [next[index], next[target]] = [next[target], next[index]];
    update(next);
  };

  const rotate = (index: number) => update(pages.map((page, pageIndex) => pageIndex === index ? { ...page, rotation: (page.rotation + 90) % 360 } : page));
  const remove = (index: number) => update(pages.filter((_, pageIndex) => pageIndex !== index));
  const duplicate = (index: number) => update([...pages.slice(0, index + 1), { ...pages[index], id: pageID() }, ...pages.slice(index + 1)]);
  const updateLayout = (change: Partial<DocumentLayoutSettings>) => update(pages, mergeLayoutSettings(layout, change));
  const changePageLayout = (index: number, value: string) => {
    const next = pages.map((page, pageIndex) => {
      if (pageIndex !== index) return page;
      const override = { ...(page.layout || {}) };
      if (value === 'inherit') delete override.mode;
      else override.mode = value as LayoutMode;
      return { ...page, layout: Object.keys(override).length ? override : undefined };
    });
    update(next);
  };

  const undo = () => {
    const previous = past[past.length - 1];
    if (!previous) return;
    setFuture(current => [snapshot(), ...current]);
    setPages(previous.pages);
    setLayout(previous.layout);
    setPast(current => current.slice(0, -1));
  };
  const redo = () => {
    const next = future[0];
    if (!next) return;
    setPast(current => [...current, snapshot()]);
    setPages(next.pages);
    setLayout(next.layout);
    setFuture(current => current.slice(1));
  };

  const uploadSources = async () => {
    if (!pages.length || busy) return;
    setBusy(true);
    setProgress(0);
    try {
      const byFile = new Map<File, StoredUpload>();
      let completed = 0;
      for (const page of pages) {
        if (!byFile.has(page.file)) {
          const upload = await uploadWithProgress<StoredUpload>(page.file, percent => setProgress(Math.round((completed + percent / 100) / pages.length * 100)), session.guest === true);
          byFile.set(page.file, upload);
          completed++;
        }
      }
      setPages(current => current.map(page => ({ ...page, upload: byFile.get(page.file) })));
      notify('Sources prepared. Your page order and layout are ready for the PDF exporter.');
    } catch (error) {
      notify((error as Error).message || 'The document sources could not be prepared.');
    } finally {
      setBusy(false);
    }
  };

  const generatePDF = async () => {
    if (!canGeneratePDF || busy) return;
    setBusy(true);
    setProgress(0);
    try {
      const images: PDFImage[] = [];
      for (const [index, page] of pages.entries()) {
        images.push(await rasterize(page.file, page.rotation));
        setProgress(Math.round((index + 1) / pages.length * 100));
      }
      const blob = makePDF(images, layout, pages.map(page => page.layout || {}));
      const url = URL.createObjectURL(blob);
      const anchor = document.createElement('a');
      anchor.href = url;
      anchor.download = `${(projectName.trim() || 'untitled-document').replace(/[^a-z0-9_-]+/gi, '-')}.pdf`;
      anchor.click();
      URL.revokeObjectURL(url);
      notify('PDF generated and downloaded.');
    } catch (error) {
      notify((error as Error).message || 'The PDF could not be generated.');
    } finally {
      setBusy(false);
    }
  };

  const manifest = useMemo(() => ({
    schemaVersion: 1,
    name: projectName.trim() || 'Untitled document',
    outputFormat: 'pdf',
    layout,
    pages: pages.map(page => ({ id: page.id, uploadId: page.upload?.id || `pending-${page.id}`, sourcePage: 1, rotation: page.rotation, layout: page.layout })),
  }), [pages, projectName, layout]);

  const downloadManifest = () => {
    const blob = new Blob([JSON.stringify(manifest, null, 2)], { type: 'application/json' });
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement('a');
    anchor.href = url;
    anchor.download = `${(projectName.trim() || 'untitled-document').replace(/[^a-z0-9_-]+/gi, '-')}-manifest.json`;
    anchor.click();
    URL.revokeObjectURL(url);
  };

  return <>
    <div className="page-heading"><div><p className="eyebrow">BUILD / DOCUMENT CREATOR</p><h1>Assemble a document</h1><p className="page-description">Arrange source pages into a non-destructive PDF project. Each image keeps its proportions and can use its own page layout.</p></div><div className="document-actions"><button className="secondary" disabled={!past.length || busy} onClick={undo}><Undo2 size={15}/> Undo</button><button className="secondary" disabled={!future.length || busy} onClick={redo}><Redo2 size={15}/> Redo</button></div></div>
    <div className="banner setup-notice"><FilePlus2 size={18}/><div><b>Preview, then generate</b><span>Automatic layout follows each image's shape. Fit and Fill use a shared paper size when you need a consistent document.</span></div></div>
    <div className="document-builder">
      <section className="document-source-panel">
        <label className="field-label" htmlFor="document-project-name">Project name</label>
        <input id="document-project-name" value={projectName} maxLength={120} onChange={event => setProjectName(event.target.value)} />
        <section className="document-layout-controls" aria-label="Document layout settings">
          <div className="section-row"><div><h2>Page layout</h2><p>Automatic is recommended for mixed image shapes.</p></div></div>
          <div className="document-layout-grid">
            <label className="field-label">Layout mode<select aria-label="Layout mode" value={layout.mode} onChange={event => { const mode = event.target.value as LayoutMode; updateLayout({ mode, pageSize: mode === 'automatic' ? 'auto' : layout.pageSize === 'auto' ? 'a4' : layout.pageSize }); }}><option value="automatic">Automatic · follow image</option><option value="fit">Fit to page · show all</option><option value="fill">Fill page · center crop</option></select></label>
            {layout.mode !== 'automatic' && <label className="field-label">Paper size<select aria-label="Paper size" value={layout.pageSize === 'auto' ? 'a4' : layout.pageSize} onChange={event => updateLayout({ pageSize: event.target.value as DocumentLayoutSettings['pageSize'] })}><option value="a4">A4 · auto orientation</option><option value="letter">Letter · auto orientation</option><option value="custom">Custom · millimetres</option></select></label>}
            {layout.mode !== 'automatic' && layout.pageSize === 'custom' && <><label className="field-label">Width (mm)<input type="number" min={25} max={1000} step={1} value={layout.customWidthMm} onChange={event => updateLayout({ customWidthMm: Number(event.target.value) })} /></label><label className="field-label">Height (mm)<input type="number" min={25} max={1000} step={1} value={layout.customHeightMm} onChange={event => updateLayout({ customHeightMm: Number(event.target.value) })} /></label></>}
            <label className="field-label">Margin (mm)<input type="number" min={0} max={50} step={1} value={layout.marginMm} onChange={event => updateLayout({ marginMm: Number(event.target.value) })} /></label>
            <label className="field-label">Background<select aria-label="Page background" value={layout.background} onChange={event => updateLayout({ background: event.target.value as DocumentLayoutSettings['background'] })}><option value="white">White</option><option value="transparent">Transparent</option><option value="black">Black</option></select></label>
          </div>
        </section>
        <UploadArea mode="convert" files={pages.length} onFiles={choose} limit={MAX_SOURCES} disabled={busy} formats={['jpg', 'jpeg', 'png', 'pdf', 'docx', 'odt']} message={message} onDismiss={() => setMessage('')} />
        {pages.length > 0 && <div className="document-source-actions"><button className="secondary" disabled={busy} onClick={() => setPreviewOpen(true)}><Eye size={16}/> Preview</button><button className="primary" disabled={busy || !canGeneratePDF} onClick={() => void generatePDF()}>{busy ? <><LoaderCircle className="spin"/> Generating {progress}%</> : <><FileOutput size={16}/> Generate PDF</>}</button><button className="secondary" disabled={busy} onClick={() => void uploadSources()}><UploadCloud size={16}/> Prepare sources</button></div>}
        {pages.length > 0 && !canGeneratePDF && <p className="document-output-note">PDF generation is enabled for image pages in this first slice. <b>{unsupportedForPDF?.file.name}</b> needs the server exporter for PDF/Office expansion or merging.</p>}
      </section>
      <section className="document-pages-panel">
        <div className="section-row"><div><h2>Pages <span className="count-badge">{pages.length}</span></h2><p>Use the controls on each card for an accessible alternative to drag-and-drop.</p></div>{pages.length > 0 && <div className="document-page-actions"><button className="secondary small" onClick={() => setPreviewOpen(true)}><Eye size={14}/> Preview</button><button className="secondary small" onClick={downloadManifest} disabled={!pages.every(page => page.upload)}>Download manifest</button></div>}</div>
        {!pages.length ? <div className="empty-state"><span className="empty-icon"><FilePlus2 size={22}/></span><h3>Your document starts here</h3><p>Add images or supported documents to create an ordered page plan.</p></div> : <ol className="document-page-list">{pages.map((page, index) => <li className="document-page-card" key={page.id}><div className="document-page-number">{index + 1}</div><div className="document-page-preview"><PageVisual page={page} settings={layout} /></div><div className="document-page-info"><strong title={page.file.name}>{page.file.name}</strong><span>{formatBytes(page.file.size)} · {page.rotation}° rotation{page.upload ? ' · source prepared' : ''}</span><PageLayoutSelect page={page} settings={layout} onChange={value => changePageLayout(index, value)} /></div><div className="document-page-controls"><button className="icon-btn" aria-label={`Move ${page.file.name} up`} disabled={index === 0 || busy} onClick={() => move(index, -1)}><ArrowUp size={16}/></button><button className="icon-btn" aria-label={`Move ${page.file.name} down`} disabled={index === pages.length - 1 || busy} onClick={() => move(index, 1)}><ArrowDown size={16}/></button><button className="icon-btn" aria-label={`Rotate ${page.file.name}`} disabled={busy} onClick={() => rotate(index)}><RotateCw size={16}/></button><button className="icon-btn" aria-label={`Duplicate ${page.file.name}`} disabled={busy} onClick={() => duplicate(index)}><Copy size={16}/></button><button className="icon-btn quiet" aria-label={`Remove ${page.file.name}`} disabled={busy} onClick={() => remove(index)}><Trash2 size={16}/></button></div></li>)}</ol>}
      </section>
    </div>
    {previewOpen && <div className="modal-scrim document-preview-scrim" role="presentation" onMouseDown={event => { if (event.target === event.currentTarget) setPreviewOpen(false); }}><section className="modal document-preview-modal" role="dialog" aria-modal="true" aria-labelledby="document-preview-title"><div className="modal-top"><div><p className="eyebrow">DOCUMENT PREVIEW</p><h2 id="document-preview-title">{projectName.trim() || 'Untitled document'}</h2><p>{pages.length} page{pages.length === 1 ? '' : 's'} in export order</p></div><button className="icon-btn" aria-label="Close preview" onClick={() => setPreviewOpen(false)}><X size={18}/></button></div><div className="document-preview-list">{pages.map((page, index) => <article className="document-preview-page" key={page.id}><div className="document-preview-page-number"><span>{index + 1}</span><b>{page.file.name}</b><small>{layoutLabel(layout, page.layout)} · {page.rotation}°</small></div><PageVisual page={page} settings={layout} large /></article>)}</div><div className="modal-actions"><button className="secondary" onClick={() => setPreviewOpen(false)}>Close preview</button>{canGeneratePDF && <button className="primary" disabled={busy} onClick={() => { setPreviewOpen(false); void generatePDF(); }}><FileOutput size={15}/> Generate PDF</button>}</div></section></div>}
  </>;
}
