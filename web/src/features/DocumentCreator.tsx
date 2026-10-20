import { useEffect, useMemo, useState } from 'react';
import { ArrowDown, ArrowUp, Copy, Download, Eye, File, FileOutput, FilePlus2, LoaderCircle, Redo2, RotateCw, Trash2, Undo2, UploadCloud, X } from 'lucide-react';
import { UploadArea, selectFiles } from '../components/UploadArea';
import { uploadWithProgress, type Session } from './session';

type StoredUpload = { id: string; fileName: string; extension: string; size: number };
type PageDraft = { id: string; file: File; rotation: number; upload?: StoredUpload };
type HistoryState = PageDraft[];

const MAX_SOURCES = 50;
const extension = (name: string) => name.includes('.') ? name.split('.').pop()!.toLowerCase() : '';
const formatBytes = (size: number) => size < 1024 * 1024 ? `${Math.round(size / 1024)} KB` : `${(size / 1024 / 1024).toFixed(1)} MB`;
const pageID = () => globalThis.crypto?.randomUUID ? globalThis.crypto.randomUUID() : `page-${Date.now()}-${Math.random().toString(16).slice(2)}`;
const rasterExtensions = new Set(['png', 'jpg', 'jpeg']);
const isRaster = (file: File) => rasterExtensions.has(extension(file.name));

function useObjectURL(file: File) {
  const [url, setUrl] = useState('');
  useEffect(() => {
    const next = URL.createObjectURL(file);
    setUrl(next);
    return () => URL.revokeObjectURL(next);
  }, [file]);
  return url;
}

function PageVisual({ page, large = false }: { page: PageDraft; large?: boolean }) {
  const url = useObjectURL(page.file);
  const ext = extension(page.file.name);
  if (isRaster(page.file)) return <img className={large ? 'document-preview-image' : 'document-thumbnail-image'} src={url} alt={`Preview of ${page.file.name}`} style={{ transform: `rotate(${page.rotation}deg)` }} />;
  if (ext === 'pdf' && large) return <iframe className="document-preview-frame" src={url} title={`Preview of ${page.file.name}`} />;
  return <div className="document-file-placeholder"><File size={large ? 34 : 22} /><b>{ext.toUpperCase() || 'FILE'}</b><span>{large ? 'Preview available after export' : 'Preview'}</span></div>;
}

type PDFImage = { width: number; height: number; bytes: Uint8Array };

async function rasterize(file: File, rotation: number): Promise<PDFImage> {
  const url = URL.createObjectURL(file);
  try {
    const image = await new Promise<HTMLImageElement>((resolve, reject) => {
      const element = new Image();
      element.onload = () => resolve(element);
      element.onerror = () => reject(new Error(`${file.name} could not be decoded as an image.`));
      element.src = url;
    });
    const angle = rotation % 360;
    const swap = angle === 90 || angle === 270;
    const canvas = document.createElement('canvas');
    canvas.width = swap ? image.naturalHeight : image.naturalWidth;
    canvas.height = swap ? image.naturalWidth : image.naturalHeight;
    if (!canvas.width || !canvas.height) throw new Error(`${file.name} has no usable image dimensions.`);
    const context = canvas.getContext('2d');
    if (!context) throw new Error('This browser cannot prepare a PDF canvas.');
    context.translate(canvas.width / 2, canvas.height / 2);
    context.rotate(angle * Math.PI / 180);
    context.drawImage(image, -image.naturalWidth / 2, -image.naturalHeight / 2);
    const encoded = atob(canvas.toDataURL('image/jpeg', 0.92).split(',')[1]);
    const bytes = Uint8Array.from(encoded, character => character.charCodeAt(0));
    return { width: canvas.width, height: canvas.height, bytes };
  } finally {
    URL.revokeObjectURL(url);
  }
}

function makePDF(images: PDFImage[]) {
  const pageWidth = 595;
  const pageHeight = 842;
  const margin = 24;
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
    const maxWidth = pageWidth - margin * 2;
    const maxHeight = pageHeight - margin * 2;
    const scale = Math.min(maxWidth / image.width, maxHeight / image.height);
    const drawWidth = image.width * scale;
    const drawHeight = image.height * scale;
    const x = (pageWidth - drawWidth) / 2;
    const y = (pageHeight - drawHeight) / 2;
    object(pageIDs[index], `<< /Type /Page /Parent 2 0 R /MediaBox [0 0 ${pageWidth} ${pageHeight}] /Resources << /ProcSet [/PDF /ImageC] /XObject << /Im0 ${imageIDs[index]} 0 R >> >> /Contents ${contentIDs[index]} 0 R >>`);
    offsets[imageIDs[index]] = length;
    text(`${imageIDs[index]} 0 obj\n<< /Type /XObject /Subtype /Image /Width ${image.width} /Height ${image.height} /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /DCTDecode /Length ${image.bytes.length} >>\nstream\n`);
    bytes(image.bytes);
    text(`\nendstream\nendobj\n`);
    const stream = `q ${drawWidth.toFixed(3)} 0 0 ${drawHeight.toFixed(3)} ${x.toFixed(3)} ${y.toFixed(3)} cm /Im0 Do Q\n`;
    object(contentIDs[index], `<< /Length ${new TextEncoder().encode(stream).length} >>\nstream\n${stream}endstream`);
  });
  const xref = length;
  text(`xref\n0 ${objectCount + 1}\n0000000000 65535 f \n`);
  for (let index = 1; index <= objectCount; index++) text(`${String(offsets[index]).padStart(10, '0')} 00000 n \n`);
  text(`trailer\n<< /Size ${objectCount + 1} /Root 1 0 R >>\nstartxref\n${xref}\n%%EOF\n`);
  return new Blob(chunks, { type: 'application/pdf' });
}

export function DocumentCreator({ notify, session }: { notify: (message: string) => void; session: Session }) {
  const [pages, setPages] = useState<PageDraft[]>([]);
  const [past, setPast] = useState<HistoryState[]>([]);
  const [future, setFuture] = useState<HistoryState[]>([]);
  const [message, setMessage] = useState('');
  const [projectName, setProjectName] = useState('Untitled document');
  const [busy, setBusy] = useState(false);
  const [progress, setProgress] = useState(0);
  const [previewOpen, setPreviewOpen] = useState(false);

  const canGeneratePDF = pages.length > 0 && pages.every(page => isRaster(page.file));
  const unsupportedForPDF = pages.find(page => !isRaster(page.file));

  const update = (next: PageDraft[]) => {
    setPast(current => [...current.slice(-49), pages]);
    setPages(next);
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

  const undo = () => {
    const previous = past[past.length - 1];
    if (!previous) return;
    setFuture(current => [pages, ...current]);
    setPages(previous);
    setPast(current => current.slice(0, -1));
  };
  const redo = () => {
    const next = future[0];
    if (!next) return;
    setPast(current => [...current, pages]);
    setPages(next);
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
      notify('Sources prepared. Your page order is ready for the PDF exporter.');
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
      const blob = makePDF(images);
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
    pages: pages.map(page => ({ id: page.id, uploadId: page.upload?.id || `pending-${page.id}`, sourcePage: 1, rotation: page.rotation })),
  }), [pages, projectName]);

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
    <div className="page-heading"><div><p className="eyebrow">BUILD / DOCUMENT CREATOR</p><h1>Assemble a document</h1><p className="page-description">Arrange source pages into a non-destructive PDF project. Originals stay intact while you experiment.</p></div><div className="document-actions"><button className="secondary" disabled={!past.length || busy} onClick={undo}><Undo2 size={15}/> Undo</button><button className="secondary" disabled={!future.length || busy} onClick={redo}><Redo2 size={15}/> Redo</button></div></div>
    <div className="banner setup-notice"><FilePlus2 size={18}/><div><b>Preview, then generate</b><span>Image pages can be previewed, reordered, rotated, and generated into a PDF in your browser. PDF and Office sources can be planned now; server-side expansion and merging are next.</span></div></div>
    <div className="document-builder">
      <section className="document-source-panel">
        <label className="field-label" htmlFor="document-project-name">Project name</label>
        <input id="document-project-name" value={projectName} maxLength={120} onChange={event => setProjectName(event.target.value)} />
        <UploadArea mode="convert" files={pages.length} onFiles={choose} limit={MAX_SOURCES} disabled={busy} formats={['jpg', 'jpeg', 'png', 'pdf', 'docx', 'odt']} message={message} onDismiss={() => setMessage('')} />
        {pages.length > 0 && <div className="document-source-actions"><button className="secondary" disabled={busy} onClick={() => setPreviewOpen(true)}><Eye size={16}/> Preview</button><button className="primary" disabled={busy || !canGeneratePDF} onClick={() => void generatePDF()}>{busy ? <><LoaderCircle className="spin"/> Generating {progress}%</> : <><FileOutput size={16}/> Generate PDF</>}</button><button className="secondary" disabled={busy} onClick={() => void uploadSources()}><UploadCloud size={16}/> Prepare sources</button></div>}
        {pages.length > 0 && !canGeneratePDF && <p className="document-output-note">PDF generation is enabled for image pages in this first slice. <b>{unsupportedForPDF?.file.name}</b> needs the server exporter for PDF/Office expansion or merging.</p>}
      </section>
      <section className="document-pages-panel">
        <div className="section-row"><div><h2>Pages <span className="count-badge">{pages.length}</span></h2><p>Use the controls on each card for an accessible alternative to drag-and-drop.</p></div>{pages.length > 0 && <div className="document-page-actions"><button className="secondary small" onClick={() => setPreviewOpen(true)}><Eye size={14}/> Preview</button><button className="secondary small" onClick={downloadManifest} disabled={!pages.every(page => page.upload)}>Download manifest</button></div>}</div>
        {!pages.length ? <div className="empty-state"><span className="empty-icon"><FilePlus2 size={22}/></span><h3>Your document starts here</h3><p>Add images or supported documents to create an ordered page plan.</p></div> : <ol className="document-page-list">{pages.map((page, index) => <li className="document-page-card" key={page.id}><div className="document-page-number">{index + 1}</div><div className="document-page-preview"><PageVisual page={page}/></div><div className="document-page-info"><strong title={page.file.name}>{page.file.name}</strong><span>{formatBytes(page.file.size)} · {page.rotation}° rotation{page.upload ? ' · source prepared' : ''}</span></div><div className="document-page-controls"><button className="icon-btn" aria-label={`Move ${page.file.name} up`} disabled={index === 0 || busy} onClick={() => move(index, -1)}><ArrowUp size={16}/></button><button className="icon-btn" aria-label={`Move ${page.file.name} down`} disabled={index === pages.length - 1 || busy} onClick={() => move(index, 1)}><ArrowDown size={16}/></button><button className="icon-btn" aria-label={`Rotate ${page.file.name}`} disabled={busy} onClick={() => rotate(index)}><RotateCw size={16}/></button><button className="icon-btn" aria-label={`Duplicate ${page.file.name}`} disabled={busy} onClick={() => duplicate(index)}><Copy size={16}/></button><button className="icon-btn quiet" aria-label={`Remove ${page.file.name}`} disabled={busy} onClick={() => remove(index)}><Trash2 size={16}/></button></div></li>)}</ol>}
      </section>
    </div>
    {previewOpen && <div className="modal-scrim document-preview-scrim" role="presentation" onMouseDown={event => { if (event.target === event.currentTarget) setPreviewOpen(false); }}><section className="modal document-preview-modal" role="dialog" aria-modal="true" aria-labelledby="document-preview-title"><div className="modal-top"><div><p className="eyebrow">DOCUMENT PREVIEW</p><h2 id="document-preview-title">{projectName.trim() || 'Untitled document'}</h2><p>{pages.length} page{pages.length === 1 ? '' : 's'} in export order</p></div><button className="icon-btn" aria-label="Close preview" onClick={() => setPreviewOpen(false)}><X size={18}/></button></div><div className="document-preview-list">{pages.map((page, index) => <article className="document-preview-page" key={page.id}><div className="document-preview-page-number"><span>{index + 1}</span><b>{page.file.name}</b><small>{page.rotation}° rotation</small></div><div className="document-preview-surface"><PageVisual page={page} large /></div></article>)}</div><div className="modal-actions"><button className="secondary" onClick={() => setPreviewOpen(false)}>Close preview</button>{canGeneratePDF && <button className="primary" disabled={busy} onClick={() => { setPreviewOpen(false); void generatePDF(); }}><FileOutput size={15}/> Generate PDF</button>}</div></section></div>}
  </>;
}
