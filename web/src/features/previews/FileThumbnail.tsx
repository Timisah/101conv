import { useEffect, useState } from 'react';
import { Archive, File as FileIcon, FileText, Film, Image as ImageIcon, Music } from 'lucide-react';
import {
  batchCollagePlan,
  fileKindFor,
  isBrowserPreviewableImage,
  normalizeExt,
  previewDescriptorFor,
  type FileKind,
} from './filePreview';
import './previews.css';

function KindIcon({ kind, size = 18 }: { kind: FileKind; size?: number }) {
  switch (kind) {
    case 'image':
      return <ImageIcon size={size} aria-hidden="true" />;
    case 'video':
      return <Film size={size} aria-hidden="true" />;
    case 'audio':
      return <Music size={size} aria-hidden="true" />;
    case 'document':
      return <FileText size={size} aria-hidden="true" />;
    case 'archive':
      return <Archive size={size} aria-hidden="true" />;
    default:
      return <FileIcon size={size} aria-hidden="true" />;
  }
}

export function LocalFileThumb({ file }: { file: File }) {
  const previewable = isBrowserPreviewableImage(file.name, file.type);
  const [url, setUrl] = useState<string | null>(null);
  const [failed, setFailed] = useState(false);

  useEffect(() => {
    setFailed(false);
    if (!previewable || typeof URL === 'undefined' || typeof URL.createObjectURL !== 'function') {
      setUrl(null);
      return;
    }
    const objectUrl = URL.createObjectURL(file);
    setUrl(objectUrl);
    return () => {
      URL.revokeObjectURL(objectUrl);
    };
  }, [file, previewable]);

  const ext = normalizeExt(file.name);
  const kind = fileKindFor(ext);

  if (url && !failed) {
    return (
      <span className="file-thumb upload-thumb">
        <img
          src={url}
          alt={`Preview of ${file.name}`}
          loading="lazy"
          decoding="async"
          onError={() => setFailed(true)}
        />
      </span>
    );
  }

  return (
    <span className={`file-thumb upload-thumb fallback kind-${kind}`} aria-label={`${ext ? ext.toUpperCase() : 'File'} icon (no image preview)`}>
      <KindIcon kind={kind} size={18} />
      {ext && <small>{ext.toUpperCase().slice(0, 5)}</small>}
    </span>
  );
}

export function RemoteFileThumb({
  url,
  alt,
  format,
  badge,
  size = 'md',
}: {
  url: string | null;
  alt: string;
  format: string;
  badge?: 'Output' | 'Original' | null;
  size?: 'sm' | 'md' | 'tile';
}) {
  const [failed, setFailed] = useState(false);
  useEffect(() => {
    setFailed(false);
  }, [url]);

  const ext = normalizeExt(format);
  const kind = fileKindFor(ext);

  return (
    <span className={`file-thumb remote-thumb size-${size} ${!url || failed ? `fallback kind-${kind}` : ''}`}>
      {url && !failed ? (
        <img
          src={url}
          alt={alt}
          loading="lazy"
          decoding="async"
          onError={() => setFailed(true)}
        />
      ) : (
        <>
          <KindIcon kind={kind} size={size === 'tile' ? 15 : 18} />
          {ext && <small>{ext.toUpperCase().slice(0, 5)}</small>}
        </>
      )}
      {badge && size !== 'tile' && (
        <span className={`thumb-kind-badge ${badge === 'Output' ? 'output' : 'original'}`}>{badge}</span>
      )}
    </span>
  );
}

export type CollageItem = {
  index: number;
  uploadId: string;
  fileName?: string;
  jobId?: string;
  sourceFormat?: string;
  targetFormat?: string;
  state: string;
  originalState?: string;
  resultState?: string;
};

export function BatchCollage({ items, total }: { items: CollageItem[]; total: number }) {
  const { tiles, overflowCount } = batchCollagePlan(items, total);

  if (total <= 1) {
    const first = tiles[0];
    if (!first) {
      return <RemoteFileThumb url={null} alt="Batch file" format="" size="md" />;
    }
    const desc = previewDescriptorFor(first);
    return (
      <RemoteFileThumb
        url={desc.url}
        alt={`${desc.badge === 'Output' ? 'Processed output' : 'Original'} preview of ${first.fileName || 'file'}`}
        format={desc.format}
        badge={desc.badge}
        size="md"
      />
    );
  }

  const slotCount = overflowCount > 0 ? 4 : tiles.length;
  return (
    <div
      className={`batch-collage slots-${slotCount}`}
      aria-label={overflowCount > 0 ? `Batch preview: 3 files shown and ${overflowCount} more` : `Batch preview: ${tiles.length} files`}
    >
      {tiles.map(item => {
        const desc = previewDescriptorFor(item);
        return (
          <RemoteFileThumb
            key={item.index}
            url={desc.url}
            alt={`${desc.badge === 'Output' ? 'Processed output' : 'Original'} preview of ${item.fileName || `file ${item.index + 1}`}`}
            format={desc.format}
            size="tile"
          />
        );
      })}
      {overflowCount > 0 && (
        <span className="collage-more" aria-label={`${overflowCount} additional files`}>
          +{overflowCount}
        </span>
      )}
    </div>
  );
}
