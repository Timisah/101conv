import { useMemo, useState } from 'react';
import { ArrowDown, ArrowUp, Copy, Download, File, FilePlus2, LoaderCircle, Redo2, RotateCw, Trash2, Undo2, UploadCloud } from 'lucide-react';
import { UploadArea, selectFiles } from '../components/UploadArea';
import { apiRequest as api, uploadWithProgress, type Session } from './session';

type StoredUpload = { id: string; fileName: string; extension: string; size: number };
type PageDraft = { id: string; file: File; rotation: number; upload?: StoredUpload };
type HistoryState = PageDraft[];

const MAX_SOURCES = 50;
const extension = (name: string) => name.includes('.') ? name.split('.').pop()!.toLowerCase() : '';
const formatBytes = (size: number) => size < 1024 * 1024 ? `${Math.round(size / 1024)} KB` : `${(size / 1024 / 1024).toFixed(1)} MB`;
const pageID = () => globalThis.crypto?.randomUUID ? globalThis.crypto.randomUUID() : `page-${Date.now()}-${Math.random().toString(16).slice(2)}`;

export function DocumentCreator({ notify, session }: { notify: (message: string) => void; session: Session }) {
  const [pages, setPages] = useState<PageDraft[]>([]);
  const [past, setPast] = useState<HistoryState[]>([]);
  const [future, setFuture] = useState<HistoryState[]>([]);
  const [message, setMessage] = useState('');
  const [projectName, setProjectName] = useState('Untitled document');
  const [busy, setBusy] = useState(false);
  const [progress, setProgress] = useState(0);

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
    <div className="page-heading"><div><p className="eyebrow">BUILD / DOCUMENT CREATOR</p><h1>Assemble a document</h1><p className="page-description">Arrange source pages into a non-destructive PDF project. Originals stay intact while you experiment.</p></div><div className="document-actions"><button className="secondary" disabled={!past.length} onClick={undo}><Undo2 size={15}/> Undo</button><button className="secondary" disabled={!future.length} onClick={redo}><Redo2 size={15}/> Redo</button></div></div>
    <div className="banner setup-notice"><FilePlus2 size={18}/><div><b>Start with the page plan</b><span>The first Build slice keeps one selected source as one page. Multipage PDF/Office expansion and fidelity-tested PDF export are the next layers.</span></div></div>
    <div className="document-builder">
      <section className="document-source-panel">
        <label className="field-label" htmlFor="document-project-name">Project name</label>
        <input id="document-project-name" value={projectName} maxLength={120} onChange={event => setProjectName(event.target.value)} />
        <UploadArea mode="convert" files={pages.length} onFiles={choose} limit={MAX_SOURCES} disabled={busy} formats={['jpg', 'jpeg', 'png', 'pdf', 'docx', 'odt']} message={message} onDismiss={() => setMessage('')} />
        {pages.length > 0 && <button className="primary document-prepare" disabled={busy} onClick={() => void uploadSources()}>{busy ? <><LoaderCircle className="spin"/> Preparing {progress}%</> : <><UploadCloud size={16}/> Prepare sources</>}</button>}
      </section>
      <section className="document-pages-panel">
        <div className="section-row"><div><h2>Pages <span className="count-badge">{pages.length}</span></h2><p>Use the controls on each card for an accessible alternative to drag-and-drop.</p></div>{pages.length > 0 && <button className="secondary small" onClick={downloadManifest} disabled={!pages.every(page => page.upload)}>Download manifest</button>}</div>
        {!pages.length ? <div className="empty-state"><span className="empty-icon"><FilePlus2 size={22}/></span><h3>Your document starts here</h3><p>Add images or supported documents to create an ordered page plan.</p></div> : <ol className="document-page-list">{pages.map((page, index) => <li className="document-page-card" key={page.id}><div className="document-page-number">{index + 1}</div><div className="document-page-preview"><File size={22}/><b>{extension(page.file.name).toUpperCase() || 'FILE'}</b></div><div className="document-page-info"><strong title={page.file.name}>{page.file.name}</strong><span>{formatBytes(page.file.size)} · {page.rotation}° rotation{page.upload ? ' · source prepared' : ''}</span></div><div className="document-page-controls"><button className="icon-btn" aria-label={`Move ${page.file.name} up`} disabled={index === 0} onClick={() => move(index, -1)}><ArrowUp size={16}/></button><button className="icon-btn" aria-label={`Move ${page.file.name} down`} disabled={index === pages.length - 1} onClick={() => move(index, 1)}><ArrowDown size={16}/></button><button className="icon-btn" aria-label={`Rotate ${page.file.name}`} onClick={() => rotate(index)}><RotateCw size={16}/></button><button className="icon-btn" aria-label={`Duplicate ${page.file.name}`} onClick={() => duplicate(index)}><Copy size={16}/></button><button className="icon-btn quiet" aria-label={`Remove ${page.file.name}`} onClick={() => remove(index)}><Trash2 size={16}/></button></div></li>)}</ol>}
      </section>
    </div>
  </>;
}
