export type FileKind = 'image' | 'video' | 'audio' | 'document' | 'archive' | 'file';

const BROWSER_IMAGE_EXTS = new Set(['jpg', 'jpeg', 'png', 'webp', 'gif', 'avif', 'bmp', 'ico']);
const SERVER_THUMB_EXTS = new Set(['jpg', 'jpeg', 'png', 'gif']);
const IMAGE_EXTS = new Set([...BROWSER_IMAGE_EXTS, 'jxl', 'heic', 'heif', 'tif', 'tiff', 'ppm', 'pgm', 'pbm', 'svg']);
const VIDEO_EXTS = new Set(['mp4', 'mkv', 'webm', 'mov', 'avi', 'm4v', 'wmv', 'flv', 'ts', 'mpeg', 'mpg']);
const AUDIO_EXTS = new Set(['mp3', 'wav', 'flac', 'ogg', 'opus', 'm4a', 'aac', 'wma', 'aiff', 'alac']);
const DOC_EXTS = new Set(['pdf', 'doc', 'docx', 'odt', 'rtf', 'txt', 'xls', 'xlsx', 'ods', 'csv', 'ppt', 'pptx', 'odp']);
const ARCHIVE_EXTS = new Set(['zip', 'tar', 'gz', 'tgz', 'bz2', 'xz', '7z', 'rar', 'cbz']);

export function normalizeExt(nameOrExt: string): string {
  const trimmed = (nameOrExt || '').trim().toLowerCase();
  if (!trimmed) return '';
  if (trimmed.includes('.')) {
    return trimmed.split('.').pop() || '';
  }
  return trimmed.replace(/^\./, '');
}

export function isBrowserPreviewableImage(fileName: string, mimeType = ''): boolean {
  const mime = mimeType.trim().toLowerCase();
  if (mime === 'image/svg+xml') return false;
  if (mime.startsWith('image/') && (mime === 'image/jpeg' || mime === 'image/png' || mime === 'image/webp' || mime === 'image/gif' || mime === 'image/avif' || mime === 'image/bmp')) {
    return true;
  }
  return BROWSER_IMAGE_EXTS.has(normalizeExt(fileName));
}

export function isServerThumbnailFormat(ext: string): boolean {
  return SERVER_THUMB_EXTS.has(normalizeExt(ext));
}

export function fileKindFor(nameOrExt: string): FileKind {
  const ext = normalizeExt(nameOrExt);
  if (IMAGE_EXTS.has(ext)) return 'image';
  if (VIDEO_EXTS.has(ext)) return 'video';
  if (AUDIO_EXTS.has(ext)) return 'audio';
  if (DOC_EXTS.has(ext)) return 'document';
  if (ARCHIVE_EXTS.has(ext)) return 'archive';
  return 'file';
}

export function batchCollagePlan<T>(items: T[], total: number): { tiles: T[]; overflowCount: number } {
  const safeTotal = Math.max(0, total);
  if (safeTotal === 0) return { tiles: [], overflowCount: 0 };
  if (safeTotal <= 3) {
    return { tiles: items.slice(0, safeTotal), overflowCount: 0 };
  }
  return { tiles: items.slice(0, 3), overflowCount: safeTotal - 3 };
}

export type PreviewDescriptor = {
  url: string | null;
  badge: 'Output' | 'Original' | null;
  format: string;
};

export function previewDescriptorFor(item: {
  jobId?: string;
  uploadId?: string;
  sourceFormat?: string;
  targetFormat?: string;
  fileName?: string;
  state?: string;
  originalState?: string;
  resultState?: string;
}): PreviewDescriptor {
  const srcFmt = normalizeExt(item.sourceFormat || item.fileName || '');
  const dstFmt = normalizeExt(item.targetFormat || '');
  const hasResult = item.state === 'succeeded' && item.resultState === 'available' && !!item.jobId;
  if (hasResult) {
    return {
      url: isServerThumbnailFormat(dstFmt) ? `/api/v1/jobs/${encodeURIComponent(item.jobId!)}/thumbnail?kind=result` : null,
      badge: 'Output',
      format: dstFmt || srcFmt,
    };
  }
  const hasOriginal = item.originalState !== 'expired' && item.originalState !== 'missing';
  if (hasOriginal && isServerThumbnailFormat(srcFmt)) {
    if (item.jobId) {
      return {
        url: `/api/v1/jobs/${encodeURIComponent(item.jobId)}/thumbnail?kind=original`,
        badge: 'Original',
        format: srcFmt,
      };
    }
    if (item.uploadId) {
      return {
        url: `/api/v1/uploads/${encodeURIComponent(item.uploadId)}/thumbnail`,
        badge: 'Original',
        format: srcFmt,
      };
    }
  }
  return {
    url: null,
    badge: item.state === 'succeeded' ? null : 'Original',
    format: srcFmt || dstFmt,
  };
}
