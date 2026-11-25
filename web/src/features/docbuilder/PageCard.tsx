import { useEffect, useState } from 'react';
import { ArrowDown, ArrowUp, Copy, File as FileIcon, GripVertical, LoaderCircle, RotateCw, Trash2 } from 'lucide-react';
import {
  layoutLabel,
  resolvePageGeometry,
  type DocumentLayoutSettings,
  type ImageDimensions,
} from '../documentLayout';
import { fileExtension, imageDimensions, isRasterName, quarterTurns } from './pdfLocal';
import type { PageDraft } from './types';

function useObjectURL(file?: File) {
  const [url, setUrl] = useState('');
  useEffect(() => {
    if (!file) {
      setUrl('');
      return;
    }
    const next = URL.createObjectURL(file);
    setUrl(next);
    return () => URL.revokeObjectURL(next);
  }, [file]);
  return url;
}

function useImageDimensions(file?: File) {
  const [dimensions, setDimensions] = useState<ImageDimensions>();
  useEffect(() => {
    if (!file || !isRasterName(file.name)) {
      setDimensions(undefined);
      return;
    }
    let live = true;
    setDimensions(undefined);
    void imageDimensions(file)
      .then(value => { if (live) setDimensions(value); })
      .catch(() => { if (live) setDimensions(undefined); });
    return () => { live = false; };
  }, [file]);
  return dimensions;
}

export function PageVisual({
  page,
  settings,
  large = false,
}: {
  page: PageDraft;
  settings: DocumentLayoutSettings;
  large?: boolean;
}) {
  const localUrl = useObjectURL(page.file);
  const dimensions = useImageDimensions(page.file);
  const ext = fileExtension(page.sourceName);

  // Local raster image with exact layout geometry preview.
  if (page.file && isRasterName(page.file.name)) {
    if (!dimensions) {
      return (
        <div className="document-file-placeholder">
          <LoaderCircle className="spin" size={large ? 28 : 18} />
          <span>Loading preview</span>
        </div>
      );
    }
    const geometry = resolvePageGeometry(dimensions, page.rotation, settings, page.layout);
    const rotated = quarterTurns(page.rotation) % 2 === 1;
    const unrotatedWidth = rotated ? geometry.drawHeight : geometry.drawWidth;
    const unrotatedHeight = rotated ? geometry.drawWidth : geometry.drawHeight;
    const imageStyle = {
      position: 'absolute' as const,
      left: `${((geometry.drawX + geometry.drawWidth / 2) / geometry.pageWidth) * 100}%`,
      top: `${((geometry.drawY + geometry.drawHeight / 2) / geometry.pageHeight) * 100}%`,
      width: `${(unrotatedWidth / geometry.pageWidth) * 100}%`,
      height: `${(unrotatedHeight / geometry.pageHeight) * 100}%`,
      maxWidth: 'none',
      maxHeight: 'none',
      transform: `translate(-50%, -50%) rotate(${page.rotation}deg)`,
      objectFit: 'fill' as const,
    };
    const surfaceClass = `document-page-surface${large ? ' large' : ''}${geometry.background === 'transparent' ? ' transparent' : ''}`;
    return (
      <div
        className={surfaceClass}
        style={{
          aspectRatio: `${geometry.pageWidth} / ${geometry.pageHeight}`,
          backgroundColor: geometry.background === 'black' ? '#000' : geometry.background === 'white' ? '#fff' : undefined,
        }}
      >
        <img className="document-layout-image" src={localUrl} alt={`Preview of ${page.sourceName}`} style={imageStyle} />
      </div>
    );
  }

  // Server-persisted image page (loaded from a saved project without local File).
  if (page.sourceKind === 'image' && page.upload?.id) {
    const thumbSrc = `/api/v1/uploads/${encodeURIComponent(page.upload.id)}/thumbnail`;
    return (
      <div className={`document-page-surface${large ? ' large' : ''}`}>
        <img
          className="document-layout-image"
          src={thumbSrc}
          alt={`Preview of ${page.sourceName}`}
          style={{ width: '100%', height: '100%', objectFit: 'contain', transform: `rotate(${page.rotation}deg)` }}
        />
      </div>
    );
  }

  // PDF page preview (iframe in large preview modal when local file is present; structured aspect badge in card).
  if (page.sourceKind === 'pdf' || ext === 'pdf') {
    if (large && localUrl) {
      return (
        <iframe
          className="document-preview-frame"
          src={`${localUrl}#page=${page.sourcePage}`}
          title={`Preview of ${page.sourceName} page ${page.sourcePage}`}
        />
      );
    }
    return (
      <div className="document-file-placeholder document-pdf-badge">
        <FileIcon size={large ? 30 : 18} />
        <b>PDF</b>
        <span>p.{page.sourcePage}{page.sourcePageCount > 1 ? `/${page.sourcePageCount}` : ''}</span>
      </div>
    );
  }

  return (
    <div className="document-file-placeholder">
      <FileIcon size={large ? 34 : 20} />
      <b>{ext.toUpperCase() || 'DOC'}</b>
      <span>{large ? 'Expanded on server export' : 'Office'}</span>
    </div>
  );
}

const formatBytes = (size: number) =>
  size <= 0 ? '' : size < 1024 * 1024 ? `${Math.max(1, Math.round(size / 1024))} KB` : `${(size / 1024 / 1024).toFixed(1)} MB`;

export function PageCard({
  page,
  index,
  total,
  settings,
  busy,
  isDragging,
  isDragOver,
  onMove,
  onRotate,
  onDuplicate,
  onRemove,
  onChangeLayout,
  onDragStart,
  onDragOver,
  onDrop,
  onDragEnd,
}: {
  page: PageDraft;
  index: number;
  total: number;
  settings: DocumentLayoutSettings;
  busy: boolean;
  isDragging: boolean;
  isDragOver: boolean;
  onMove: (delta: number) => void;
  onRotate: () => void;
  onDuplicate: () => void;
  onRemove: () => void;
  onChangeLayout: (value: string) => void;
  onDragStart: () => void;
  onDragOver: (e: React.DragEvent) => void;
  onDrop: () => void;
  onDragEnd: () => void;
}) {
  const sizeText = formatBytes(page.sourceSize);
  const pageTag = page.sourcePageCount > 1 ? `Page ${page.sourcePage} of ${page.sourcePageCount}` : '';
  const metaParts = [sizeText, pageTag, `${page.rotation}° rotation`, page.upload ? 'source prepared' : ''].filter(Boolean);

  return (
    <li
      className={`document-page-card${isDragging ? ' is-dragging' : ''}${isDragOver ? ' is-drag-over' : ''}`}
      draggable={!busy}
      onDragStart={onDragStart}
      onDragOver={onDragOver}
      onDrop={onDrop}
      onDragEnd={onDragEnd}
    >
      <div className="document-page-lead">
        <span className="document-drag-handle" aria-hidden="true" title="Drag to reorder">
          <GripVertical size={14} />
        </span>
        <div className="document-page-number">{index + 1}</div>
      </div>
      <div className="document-page-preview">
        <PageVisual page={page} settings={settings} />
      </div>
      <div className="document-page-info">
        <strong title={page.sourceName}>{page.sourceName}</strong>
        <span>{metaParts.join(' · ')}</span>
        <label className="page-layout-control">
          Layout
          <select
            aria-label={`Layout for ${page.sourceName}`}
            value={page.layout?.mode || 'inherit'}
            onChange={event => onChangeLayout(event.target.value)}
          >
            <option value="inherit">Document default</option>
            <option value="automatic">Automatic</option>
            <option value="fit">Fit to page</option>
            <option value="fill">Fill page</option>
          </select>
          <small>{layoutLabel(settings, page.layout)}</small>
        </label>
      </div>
      <div className="document-page-controls">
        <button className="icon-btn" aria-label={`Move ${page.sourceName} up`} disabled={index === 0 || busy} onClick={() => onMove(-1)}>
          <ArrowUp size={16} />
        </button>
        <button className="icon-btn" aria-label={`Move ${page.sourceName} down`} disabled={index === total - 1 || busy} onClick={() => onMove(1)}>
          <ArrowDown size={16} />
        </button>
        <button className="icon-btn" aria-label={`Rotate ${page.sourceName}`} disabled={busy} onClick={onRotate}>
          <RotateCw size={16} />
        </button>
        <button className="icon-btn" aria-label={`Duplicate ${page.sourceName}`} disabled={busy} onClick={onDuplicate}>
          <Copy size={16} />
        </button>
        <button className="icon-btn quiet" aria-label={`Remove ${page.sourceName}`} disabled={busy} onClick={onRemove}>
          <Trash2 size={16} />
        </button>
      </div>
    </li>
  );
}
