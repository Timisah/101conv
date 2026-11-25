import { useEffect, useMemo, useState } from 'react';
import { Link } from 'react-router-dom';
import {
  Download,
  Eye,
  FileOutput,
  FilePlus2,
  FolderOpen,
  Layers,
  List,
  LoaderCircle,
  Redo2,
  Save,
  Server,
  Trash2,
  Undo2,
  UploadCloud,
  X,
} from 'lucide-react';
import { UploadArea, selectFiles } from '../../components/UploadArea';
import { uploadWithProgress, type Session } from '../session';
import {
  DEFAULT_LAYOUT,
  layoutLabel,
  mergeLayoutSettings,
  type DocumentLayoutSettings,
  type LayoutMode,
  type PageLayoutOverride,
} from '../documentLayout';
import {
  deleteDocumentProject,
  downloadBuildResult,
  exportDocumentOnServer,
  fetchBuildJob,
  fetchDocumentFormats,
  inspectDocumentSource,
  listDocumentProjects,
  saveDocumentProject,
} from './api';
import { PageCard, PageVisual } from './PageCard';
import { fileExtension, isRasterName, makeLocalPDF, rasterizeForLocalPDF, type PDFImage } from './pdfLocal';
import {
  DEFAULT_DOCUMENT_FORMATS,
  type DocumentFormatInfo,
  type DocumentManifest,
  type DocumentOutputFormat,
  type DocumentProject,
  type ExportJob,
  type PageDraft,
  type StoredUpload,
} from './types';
import './build.css';

type HistoryState = {
  pages: PageDraft[];
  layout: DocumentLayoutSettings;
  outputFormat: DocumentOutputFormat;
};

const MAX_SOURCES = 50;
const MAX_TOTAL_PAGES = 500;
const pageID = () =>
  globalThis.crypto?.randomUUID
    ? globalThis.crypto.randomUUID()
    : `page-${Date.now()}-${Math.random().toString(16).slice(2)}`;

const classifyFileKind = (name: string): 'image' | 'pdf' | 'office' => {
  const ext = fileExtension(name);
  if (ext === 'pdf') return 'pdf';
  if (isRasterName(name) || ext === 'gif') return 'image';
  return 'office';
};

const safeFileSlug = (name: string) =>
  (name.trim() || 'untitled-document').replace(/[^a-z0-9_-]+/gi, '-');

const formatBytes = (size: number) =>
  size < 1024 * 1024 ? `${Math.max(1, Math.round(size / 1024))} KB` : `${(size / 1024 / 1024).toFixed(1)} MB`;

export function DocumentCreator({ notify, session }: { notify: (message: string) => void; session: Session }) {
  const [pages, setPages] = useState<PageDraft[]>([]);
  const [layout, setLayout] = useState<DocumentLayoutSettings>(DEFAULT_LAYOUT);
  const [outputFormat, setOutputFormat] = useState<DocumentOutputFormat>('pdf');
  const [formats, setFormats] = useState<DocumentFormatInfo[]>(DEFAULT_DOCUMENT_FORMATS);
  const [projects, setProjects] = useState<DocumentProject[]>([]);
  const [activeProjectId, setActiveProjectId] = useState<string>('');
  const [past, setPast] = useState<HistoryState[]>([]);
  const [future, setFuture] = useState<HistoryState[]>([]);
  const [message, setMessage] = useState('');
  const [projectName, setProjectName] = useState('Untitled document');
  const [busy, setBusy] = useState(false);
  const [progress, setProgress] = useState(0);
  const [previewOpen, setPreviewOpen] = useState(false);
  const [viewMode, setViewMode] = useState<'flat' | 'grouped'>('flat');
  const [dragIndex, setDragIndex] = useState<number | null>(null);
  const [dragOverIndex, setDragOverIndex] = useState<number | null>(null);
  const [exportJob, setExportJob] = useState<ExportJob | null>(null);

  useEffect(() => {
    let live = true;
    void fetchDocumentFormats()
      .then(list => { if (live && list.length) setFormats(list); })
      .catch(() => { /* keep built-in default format list */ });
    void listDocumentProjects()
      .then(list => { if (live) setProjects(list); })
      .catch(() => { /* projects are optional when backend is unavailable */ });
    return () => { live = false; };
  }, []);

  // Poll active server build job until it reaches a terminal state.
  useEffect(() => {
    if (!exportJob || (exportJob.state !== 'queued' && exportJob.state !== 'running')) return;
    let live = true;
    let failures = 0;
    const timer = window.setInterval(() => {
      if (document.hidden) return;
      void fetchBuildJob(exportJob.id)
        .then(next => {
          if (!live) return;
          failures = 0;
          setExportJob(next);
          if (next.state === 'succeeded') {
            notify('Server document export finished and is ready to download.');
          } else if (next.state === 'failed') {
            notify(next.error || 'The server document export failed.');
          }
        })
        .catch(() => {
          failures++;
          if (failures >= 3 && live) {
            window.clearInterval(timer);
          }
        });
    }, 1000);
    return () => {
      live = false;
      window.clearInterval(timer);
    };
  }, [exportJob?.id, exportJob?.state, notify]);

  const activeFormat = useMemo(
    () => formats.find(item => item.id === outputFormat) || DEFAULT_DOCUMENT_FORMATS[0],
    [formats, outputFormat],
  );

  const canGenerateLocalPDF =
    outputFormat === 'pdf' &&
    pages.length > 0 &&
    pages.every(page => page.file && isRasterName(page.file.name));

  const snapshot = (): HistoryState => ({ pages, layout, outputFormat });
  const update = (
    nextPages: PageDraft[] = pages,
    nextLayout: DocumentLayoutSettings = layout,
    nextFormat: DocumentOutputFormat = outputFormat,
  ) => {
    setPast(current => [...current.slice(-49), snapshot()]);
    setPages(nextPages);
    setLayout(nextLayout);
    setOutputFormat(nextFormat);
    setFuture([]);
  };

  const choose = async (incoming: File[]) => {
    const existingFiles = pages.map(page => page.file).filter((f): f is File => Boolean(f));
    const selected = selectFiles(existingFiles, incoming, MAX_SOURCES);
    setMessage(selected.message);
    if (selected.files.length === existingFiles.length) return;

    const newFiles = selected.files.slice(existingFiles.length);
    const additions: PageDraft[] = [];

    // Immediately add raster images; inspect PDFs on the server to expand multi-page PDFs into individual pages.
    const needsServerInspect = newFiles.some(f => classifyFileKind(f.name) === 'pdf');
    if (needsServerInspect) setBusy(true);
    try {
      for (const file of newFiles) {
        const kind = classifyFileKind(file.name);
        if (kind === 'pdf') {
          try {
            const upload = await uploadWithProgress<StoredUpload>(file, () => undefined, session.guest === true);
            const inspected = await inspectDocumentSource(upload.id);
            const count = Math.min(inspected.pageCount || 1, MAX_TOTAL_PAGES - pages.length - additions.length);
            for (let p = 1; p <= count; p++) {
              const info = inspected.pages?.[p - 1];
              additions.push({
                id: pageID(),
                file,
                upload,
                sourceName: file.name,
                sourceKind: 'pdf',
                sourcePage: p,
                sourcePageCount: inspected.pageCount || 1,
                sourceSize: file.size,
                widthPt: info?.widthPt,
                heightPt: info?.heightPt,
                rotation: 0,
              });
            }
            if ((inspected.pageCount || 1) > 1) {
              notify(`Expanded ${file.name} into ${inspected.pageCount} rearrangeable pages.`);
            }
          } catch {
            // Graceful fallback when offline or in fixture tests.
            additions.push({
              id: pageID(),
              file,
              sourceName: file.name,
              sourceKind: 'pdf',
              sourcePage: 1,
              sourcePageCount: 1,
              sourceSize: file.size,
              rotation: 0,
            });
          }
        } else {
          additions.push({
            id: pageID(),
            file,
            sourceName: file.name,
            sourceKind: kind,
            sourcePage: 1,
            sourcePageCount: 1,
            sourceSize: file.size,
            rotation: 0,
          });
        }
      }
      if (additions.length) {
        update([...pages, ...additions].slice(0, MAX_TOTAL_PAGES));
      }
    } finally {
      if (needsServerInspect) setBusy(false);
    }
  };

  const move = (index: number, delta: number) => {
    const target = index + delta;
    if (target < 0 || target >= pages.length) return;
    const next = [...pages];
    [next[index], next[target]] = [next[target], next[index]];
    update(next);
  };

  const reorder = (fromIndex: number, toIndex: number) => {
    if (fromIndex === toIndex || fromIndex < 0 || toIndex < 0 || fromIndex >= pages.length || toIndex >= pages.length) return;
    const next = [...pages];
    const [moved] = next.splice(fromIndex, 1);
    next.splice(toIndex, 0, moved);
    update(next);
  };

  const rotate = (index: number) =>
    update(pages.map((page, pageIndex) => (pageIndex === index ? { ...page, rotation: (page.rotation + 90) % 360 } : page)));
  const remove = (index: number) => update(pages.filter((_, pageIndex) => pageIndex !== index));
  const removeSourceGroup = (groupKey: string) =>
    update(pages.filter(page => (page.upload?.id || page.sourceName) !== groupKey));
  const duplicate = (index: number) =>
    update([...pages.slice(0, index + 1), { ...pages[index], id: pageID() }, ...pages.slice(index + 1)].slice(0, MAX_TOTAL_PAGES));
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
    setOutputFormat(previous.outputFormat);
    setPast(current => current.slice(0, -1));
  };

  const redo = () => {
    const next = future[0];
    if (!next) return;
    setPast(current => [...current, snapshot()]);
    setPages(next.pages);
    setLayout(next.layout);
    setOutputFormat(next.outputFormat);
    setFuture(current => current.slice(1));
  };

  const ensureSourcesUploaded = async (): Promise<PageDraft[]> => {
    const byFile = new Map<File, StoredUpload>();
    for (const page of pages) {
      if (page.file && page.upload) byFile.set(page.file, page.upload);
    }
    const pendingFiles = Array.from(
      new Set(pages.filter(p => p.file && !p.upload && !byFile.has(p.file)).map(p => p.file!)),
    );
    let completed = 0;
    for (const file of pendingFiles) {
      const upload = await uploadWithProgress<StoredUpload>(
        file,
        percent => setProgress(Math.round(((completed + percent / 100) / Math.max(1, pendingFiles.length)) * 100)),
        session.guest === true,
      );
      byFile.set(file, upload);
      completed++;
    }
    const hydrated = pages.map(page => (page.upload ? page : page.file ? { ...page, upload: byFile.get(page.file) } : page));
    setPages(hydrated);
    return hydrated;
  };

  const buildManifest = (list: PageDraft[]): DocumentManifest => ({
    schemaVersion: 1,
    projectId: activeProjectId || undefined,
    name: projectName.trim() || 'Untitled document',
    outputFormat,
    layout,
    pages: list.map(page => ({
      id: page.id,
      uploadId: page.upload?.id || `pending-${page.id}`,
      sourcePage: page.sourcePage,
      rotation: page.rotation,
      layout: page.layout,
      sourceName: page.sourceName,
      sourceKind: page.sourceKind,
      sourcePageCount: page.sourcePageCount,
    })),
  });

  const manifest = useMemo(() => buildManifest(pages), [pages, projectName, layout, outputFormat, activeProjectId]);

  const uploadSources = async () => {
    if (!pages.length || busy) return;
    setBusy(true);
    setProgress(0);
    try {
      await ensureSourcesUploaded();
      notify('Sources prepared. Your page order and layout are ready for export.');
    } catch (error) {
      notify((error as Error).message || 'The document sources could not be prepared.');
    } finally {
      setBusy(false);
    }
  };

  const saveProject = async () => {
    if (!pages.length || busy) return;
    setBusy(true);
    setProgress(0);
    try {
      const hydrated = await ensureSourcesUploaded();
      const saved = await saveDocumentProject(buildManifest(hydrated), activeProjectId || undefined);
      setActiveProjectId(saved.id);
      setProjects(prev => [saved, ...prev.filter(item => item.id !== saved.id)]);
      notify(session.guest ? 'Guest project saved (expires with your guest session).' : 'Project saved.');
    } catch (error) {
      notify((error as Error).message || 'Could not save the document project.');
    } finally {
      setBusy(false);
    }
  };

  const loadProject = (id: string) => {
    if (!id) {
      setActiveProjectId('');
      return;
    }
    const found = projects.find(p => p.id === id);
    if (!found) return;
    setActiveProjectId(found.id);
    setProjectName(found.name);
    setOutputFormat(found.manifest.outputFormat || 'pdf');
    setLayout(mergeLayoutSettings(DEFAULT_LAYOUT, found.manifest.layout));
    setPages(
      found.manifest.pages.map(p => ({
        id: p.id,
        upload: { id: p.uploadId, fileName: p.sourceName || p.uploadId, extension: fileExtension(p.sourceName || ''), size: 0 },
        sourceName: p.sourceName || p.uploadId,
        sourceKind: (p.sourceKind as 'image' | 'pdf' | 'office') || 'image',
        sourcePage: p.sourcePage || 1,
        sourcePageCount: p.sourcePageCount || 1,
        sourceSize: 0,
        rotation: p.rotation || 0,
        layout: p.layout,
      })),
    );
    setPast([]);
    setFuture([]);
    setExportJob(null);
    notify(`Loaded project "${found.name}".`);
  };

  const removeActiveProject = async () => {
    if (!activeProjectId || busy) return;
    setBusy(true);
    try {
      await deleteDocumentProject(activeProjectId);
      setProjects(prev => prev.filter(p => p.id !== activeProjectId));
      setActiveProjectId('');
      notify('Saved project deleted. Original uploaded files were not deleted.');
    } catch (error) {
      notify((error as Error).message || 'Could not delete the saved project.');
    } finally {
      setBusy(false);
    }
  };

  const exportOnServer = async () => {
    if (!pages.length || busy) return;
    setBusy(true);
    setProgress(0);
    setExportJob(null);
    try {
      const hydrated = await ensureSourcesUploaded();
      const readyManifest = buildManifest(hydrated);
      const idempotencyKey = `build-${Date.now()}-${Math.random().toString(16).slice(2, 10)}`;
      const job = await exportDocumentOnServer(readyManifest, idempotencyKey);
      setExportJob(job);
      notify(`Queued server ${activeFormat.label} export.`);
    } catch (error) {
      notify((error as Error).message || 'Could not queue the document export.');
    } finally {
      setBusy(false);
    }
  };

  const generateLocalPDF = async () => {
    if (!canGenerateLocalPDF || busy) return;
    setBusy(true);
    setProgress(0);
    try {
      const images: PDFImage[] = [];
      for (const [index, page] of pages.entries()) {
        images.push(await rasterizeForLocalPDF(page.file!, page.rotation));
        setProgress(Math.round(((index + 1) / pages.length) * 100));
      }
      const blob = makeLocalPDF(images, layout, pages.map(page => page.layout || {}));
      const url = URL.createObjectURL(blob);
      const anchor = document.createElement('a');
      anchor.href = url;
      anchor.download = `${safeFileSlug(projectName)}.pdf`;
      anchor.click();
      URL.revokeObjectURL(url);
      notify('PDF generated and downloaded.');
    } catch (error) {
      notify((error as Error).message || 'The PDF could not be generated.');
    } finally {
      setBusy(false);
    }
  };

  const downloadManifest = () => {
    const blob = new Blob([JSON.stringify(manifest, null, 2)], { type: 'application/json' });
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement('a');
    anchor.href = url;
    anchor.download = `${safeFileSlug(projectName)}-manifest.json`;
    anchor.click();
    URL.revokeObjectURL(url);
  };

  const sourceGroups = useMemo(() => {
    const map = new Map<string, { key: string; name: string; kind: string; items: Array<{ page: PageDraft; index: number }> }>();
    pages.forEach((page, index) => {
      const key = page.upload?.id || page.sourceName;
      const existing = map.get(key);
      if (existing) existing.items.push({ page, index });
      else map.set(key, { key, name: page.sourceName, kind: page.sourceKind, items: [{ page, index }] });
    });
    return Array.from(map.values());
  }, [pages]);

  return (
    <>
      <div className="page-heading">
        <div>
          <p className="eyebrow">BUILD / DOCUMENT CREATOR</p>
          <h1>Assemble a document</h1>
          <p className="page-description">
            Combine images, multi-page PDFs, and Office documents into a non-destructive project. Reorder, rotate, and export to PDF, CBZ, ZIP, TIFF, DOCX, PPTX, ODT, or ODP.
          </p>
        </div>
        <div className="document-actions">
          <button className="secondary" disabled={!past.length || busy} onClick={undo}>
            <Undo2 size={15} /> Undo
          </button>
          <button className="secondary" disabled={!future.length || busy} onClick={redo}>
            <Redo2 size={15} /> Redo
          </button>
        </div>
      </div>

      <section className="build-project-bar" aria-label="Document projects">
        <div className="build-project-field">
          <label className="field-label" htmlFor="document-project-name">Project name</label>
          <input
            id="document-project-name"
            value={projectName}
            maxLength={120}
            onChange={event => setProjectName(event.target.value)}
          />
        </div>
        {projects.length > 0 && (
          <div className="build-project-field">
            <label className="field-label" htmlFor="document-saved-project">Saved projects</label>
            <select
              id="document-saved-project"
              aria-label="Saved projects"
              value={activeProjectId}
              onChange={event => loadProject(event.target.value)}
            >
              <option value="">Unsaved / new project</option>
              {projects.map(proj => (
                <option key={proj.id} value={proj.id}>
                  {proj.name} ({proj.pageCount}p · {proj.outputFormat.toUpperCase()})
                </option>
              ))}
            </select>
          </div>
        )}
        <div className="build-project-actions">
          <button className="secondary" disabled={!pages.length || busy} onClick={() => void saveProject()}>
            <Save size={15} /> {activeProjectId ? 'Update project' : 'Save project'}
          </button>
          {activeProjectId && (
            <>
              <button
                className="secondary"
                disabled={busy}
                onClick={() => {
                  setActiveProjectId('');
                  setProjectName('Untitled document');
                  setPages([]);
                  setExportJob(null);
                }}
              >
                <FolderOpen size={15} /> New
              </button>
              <button className="secondary" disabled={busy} onClick={() => void removeActiveProject()}>
                <Trash2 size={15} /> Delete project
              </button>
            </>
          )}
        </div>
        {session.guest && (
          <p className="build-guest-note">
            Guest projects are non-destructive and expire automatically according to the instance guest retention window.
          </p>
        )}
      </section>

      <div className="document-builder">
        <section className="document-source-panel">
          <section className="document-layout-controls" aria-label="Document layout settings">
            <div className="section-row">
              <div>
                <h2>Page layout</h2>
                <p>Automatic preserves each image&apos;s shape and native PDF dimensions.</p>
              </div>
            </div>
            <div className="document-layout-grid">
              <label className="field-label">
                Layout mode
                <select
                  aria-label="Layout mode"
                  value={layout.mode}
                  onChange={event => {
                    const mode = event.target.value as LayoutMode;
                    updateLayout({ mode, pageSize: mode === 'automatic' ? 'auto' : layout.pageSize === 'auto' ? 'a4' : layout.pageSize });
                  }}
                >
                  <option value="automatic">Automatic · follow image</option>
                  <option value="fit">Fit to page · show all</option>
                  <option value="fill">Fill page · center crop</option>
                </select>
              </label>
              {layout.mode !== 'automatic' && (
                <label className="field-label">
                  Paper size
                  <select
                    aria-label="Paper size"
                    value={layout.pageSize === 'auto' ? 'a4' : layout.pageSize}
                    onChange={event => updateLayout({ pageSize: event.target.value as DocumentLayoutSettings['pageSize'] })}
                  >
                    <option value="a4">A4 · auto orientation</option>
                    <option value="letter">Letter · auto orientation</option>
                    <option value="custom">Custom · millimetres</option>
                  </select>
                </label>
              )}
              {layout.mode !== 'automatic' && layout.pageSize === 'custom' && (
                <>
                  <label className="field-label">
                    Width (mm)
                    <input
                      type="number"
                      min={25}
                      max={1000}
                      step={1}
                      value={layout.customWidthMm}
                      onChange={event => updateLayout({ customWidthMm: Number(event.target.value) })}
                    />
                  </label>
                  <label className="field-label">
                    Height (mm)
                    <input
                      type="number"
                      min={25}
                      max={1000}
                      step={1}
                      value={layout.customHeightMm}
                      onChange={event => updateLayout({ customHeightMm: Number(event.target.value) })}
                    />
                  </label>
                </>
              )}
              <label className="field-label">
                Margin (mm)
                <input
                  type="number"
                  min={0}
                  max={50}
                  step={1}
                  value={layout.marginMm}
                  onChange={event => updateLayout({ marginMm: Number(event.target.value) })}
                />
              </label>
              <label className="field-label">
                Background
                <select
                  aria-label="Page background"
                  value={layout.background}
                  onChange={event => updateLayout({ background: event.target.value as DocumentLayoutSettings['background'] })}
                >
                  <option value="white">White</option>
                  <option value="transparent">Transparent</option>
                  <option value="black">Black</option>
                </select>
              </label>
            </div>
          </section>

          <section className="build-format-section" aria-label="Output format">
            <label className="field-label">
              Export format
              <select
                aria-label="Export format"
                value={outputFormat}
                onChange={event => update(pages, layout, event.target.value as DocumentOutputFormat)}
              >
                {formats.map(item => (
                  <option key={item.id} value={item.id}>
                    {item.label} (.{item.extension})
                  </option>
                ))}
              </select>
            </label>
            <div className={`build-fidelity-note${activeFormat.fidelityWarning ? ' has-warning' : ''}`}>
              <span>{activeFormat.fidelitySummary}</span>
              {activeFormat.fidelityWarning && (
                <small className="build-fidelity-warning">{activeFormat.fidelityWarning}</small>
              )}
            </div>
          </section>

          <UploadArea
            mode="convert"
            files={pages.length}
            onFiles={files => void choose(files)}
            limit={MAX_SOURCES}
            disabled={busy}
            formats={['jpg', 'jpeg', 'png', 'pdf', 'docx', 'odt', 'pptx', 'odp']}
            message={message}
            onDismiss={() => setMessage('')}
          />

          {pages.length > 0 && (
            <div className="document-source-actions">
              <button className="primary" disabled={busy} onClick={() => void exportOnServer()}>
                {busy ? (
                  <><LoaderCircle className="spin" size={16} /> Working {progress}%</>
                ) : (
                  <><Server size={16} /> Export {activeFormat.extension.toUpperCase()}</>
                )}
              </button>
              {canGenerateLocalPDF && (
                <button className="secondary" disabled={busy} onClick={() => void generateLocalPDF()}>
                  <FileOutput size={16} /> Generate PDF
                </button>
              )}
              <button className="secondary" disabled={busy} onClick={() => setPreviewOpen(true)}>
                <Eye size={16} /> Preview
              </button>
              <button className="secondary" disabled={busy} onClick={() => void uploadSources()}>
                <UploadCloud size={16} /> Prepare sources
              </button>
            </div>
          )}

          {exportJob && (
            <div className="build-export-result" role="status" aria-live="polite">
              <div className="build-export-result-row">
                <strong>
                  {exportJob.state === 'succeeded'
                    ? `Ready (${exportJob.targetFormat.toUpperCase()}${exportJob.outputSize ? ` · ${formatBytes(exportJob.outputSize)}` : ''})`
                    : exportJob.state === 'failed'
                      ? 'Export failed'
                      : `Assembling ${exportJob.targetFormat.toUpperCase()} on server…`}
                </strong>
                <Link to="/jobs">Open in Jobs</Link>
              </div>
              {exportJob.state === 'failed' && exportJob.error && <small>{exportJob.error}</small>}
              {exportJob.state === 'succeeded' && (
                <button
                  className="primary small"
                  onClick={() => void downloadBuildResult(exportJob.id, `${safeFileSlug(projectName)}.${exportJob.targetFormat}`)}
                >
                  <Download size={14} /> Download {exportJob.targetFormat.toUpperCase()}
                </button>
              )}
            </div>
          )}
        </section>

        <section className="document-pages-panel">
          <div className="section-row">
            <div>
              <h2>
                Pages <span className="count-badge">{pages.length}</span>
              </h2>
              <p>Drag cards or use the keyboard/touch buttons to reorder, rotate, duplicate, or remove pages.</p>
            </div>
            {pages.length > 0 && (
              <div className="document-page-actions">
                <div className="build-view-toggle" role="group" aria-label="Page view mode">
                  <button
                    type="button"
                    className={viewMode === 'flat' ? 'active' : ''}
                    onClick={() => setViewMode('flat')}
                  >
                    <List size={13} /> Flat
                  </button>
                  <button
                    type="button"
                    className={viewMode === 'grouped' ? 'active' : ''}
                    onClick={() => setViewMode('grouped')}
                  >
                    <Layers size={13} /> By source
                  </button>
                </div>
                <button className="secondary small" onClick={() => setPreviewOpen(true)}>
                  <Eye size={14} /> Preview
                </button>
                <button className="secondary small" onClick={downloadManifest} disabled={!pages.every(page => page.upload)}>
                  Download manifest
                </button>
              </div>
            )}
          </div>

          {!pages.length ? (
            <div className="empty-state">
              <span className="empty-icon">
                <FilePlus2 size={22} />
              </span>
              <h3>Your document starts here</h3>
              <p>Add images, multi-page PDFs, or Office documents to assemble an ordered page plan.</p>
            </div>
          ) : viewMode === 'flat' ? (
            <ol className="document-page-list">
              {pages.map((page, index) => (
                <PageCard
                  key={page.id}
                  page={page}
                  index={index}
                  total={pages.length}
                  settings={layout}
                  busy={busy}
                  isDragging={dragIndex === index}
                  isDragOver={dragOverIndex === index && dragIndex !== index}
                  onMove={delta => move(index, delta)}
                  onRotate={() => rotate(index)}
                  onDuplicate={() => duplicate(index)}
                  onRemove={() => remove(index)}
                  onChangeLayout={value => changePageLayout(index, value)}
                  onDragStart={() => setDragIndex(index)}
                  onDragOver={event => {
                    event.preventDefault();
                    setDragOverIndex(index);
                  }}
                  onDrop={() => {
                    if (dragIndex !== null) reorder(dragIndex, index);
                    setDragIndex(null);
                    setDragOverIndex(null);
                  }}
                  onDragEnd={() => {
                    setDragIndex(null);
                    setDragOverIndex(null);
                  }}
                />
              ))}
            </ol>
          ) : (
            <div className="build-source-groups">
              {sourceGroups.map(group => (
                <section className="build-source-group" key={group.key}>
                  <div className="build-source-group-header">
                    <div className="build-source-group-title">
                      <strong>{group.name}</strong>
                      <span className="count-badge">{group.items.length}p</span>
                    </div>
                    <button
                      className="secondary small"
                      disabled={busy}
                      onClick={() => removeSourceGroup(group.key)}
                    >
                      <Trash2 size={13} /> Remove source
                    </button>
                  </div>
                  <ol className="build-source-group-pages">
                    {group.items.map(({ page, index }) => (
                      <PageCard
                        key={page.id}
                        page={page}
                        index={index}
                        total={pages.length}
                        settings={layout}
                        busy={busy}
                        isDragging={dragIndex === index}
                        isDragOver={dragOverIndex === index && dragIndex !== index}
                        onMove={delta => move(index, delta)}
                        onRotate={() => rotate(index)}
                        onDuplicate={() => duplicate(index)}
                        onRemove={() => remove(index)}
                        onChangeLayout={value => changePageLayout(index, value)}
                        onDragStart={() => setDragIndex(index)}
                        onDragOver={event => {
                          event.preventDefault();
                          setDragOverIndex(index);
                        }}
                        onDrop={() => {
                          if (dragIndex !== null) reorder(dragIndex, index);
                          setDragIndex(null);
                          setDragOverIndex(null);
                        }}
                        onDragEnd={() => {
                          setDragIndex(null);
                          setDragOverIndex(null);
                        }}
                      />
                    ))}
                  </ol>
                </section>
              ))}
            </div>
          )}
        </section>
      </div>

      {previewOpen && (
        <div
          className="modal-scrim document-preview-scrim"
          role="presentation"
          onMouseDown={event => {
            if (event.target === event.currentTarget) setPreviewOpen(false);
          }}
        >
          <section className="modal document-preview-modal" role="dialog" aria-modal="true" aria-labelledby="document-preview-title">
            <div className="modal-top">
              <div>
                <p className="eyebrow">DOCUMENT PREVIEW · {activeFormat.label.toUpperCase()}</p>
                <h2 id="document-preview-title">{projectName.trim() || 'Untitled document'}</h2>
                <p>
                  {pages.length} page{pages.length === 1 ? '' : 's'} in export order
                </p>
              </div>
              <button className="icon-btn" aria-label="Close preview" onClick={() => setPreviewOpen(false)}>
                <X size={18} />
              </button>
            </div>
            <div className="document-preview-list">
              {pages.map((page, index) => (
                <article className="document-preview-page" key={page.id}>
                  <div className="document-preview-page-number">
                    <span>{index + 1}</span>
                    <b>{page.sourceName}{page.sourcePageCount > 1 ? ` (p.${page.sourcePage})` : ''}</b>
                    <small>{layoutLabel(layout, page.layout)} · {page.rotation}°</small>
                  </div>
                  <PageVisual page={page} settings={layout} large />
                </article>
              ))}
            </div>
            <div className="modal-actions">
              <button className="secondary" onClick={() => setPreviewOpen(false)}>
                Close preview
              </button>
              <button
                className="primary"
                disabled={busy}
                onClick={() => {
                  setPreviewOpen(false);
                  void exportOnServer();
                }}
              >
                <Server size={15} /> Export {activeFormat.extension.toUpperCase()}
              </button>
              {canGenerateLocalPDF && (
                <button
                  className="secondary"
                  disabled={busy}
                  onClick={() => {
                    setPreviewOpen(false);
                    void generateLocalPDF();
                  }}
                >
                  <FileOutput size={15} /> Generate PDF
                </button>
              )}
            </div>
          </section>
        </div>
      )}
    </>
  );
}
