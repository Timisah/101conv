import type { DocumentLayoutSettings, PageLayoutOverride } from '../documentLayout';

export type DocumentOutputFormat = 'pdf' | 'cbz' | 'zip' | 'tiff' | 'docx' | 'pptx' | 'odt' | 'odp';

export type DocumentFormatInfo = {
  id: DocumentOutputFormat;
  label: string;
  extension: string;
  mimeType: string;
  preservesNativePdf: boolean;
  rasterizesVector: boolean;
  fidelitySummary: string;
  fidelityWarning?: string;
};

export type StoredUpload = {
  id: string;
  fileName: string;
  extension: string;
  size: number;
};

export type SourcePageInfo = {
  pageNumber: number;
  widthPt: number;
  heightPt: number;
  rotation: number;
};

export type SourceInspection = {
  uploadId: string;
  fileName: string;
  extension: string;
  size: number;
  kind: 'image' | 'pdf' | 'office';
  pageCount: number;
  expandedOnExport?: boolean;
  pages: SourcePageInfo[];
};

export type PageDraft = {
  id: string;
  file?: File;
  upload?: StoredUpload;
  sourceName: string;
  sourceKind: 'image' | 'pdf' | 'office';
  sourcePage: number;
  sourcePageCount: number;
  sourceSize: number;
  widthPt?: number;
  heightPt?: number;
  rotation: number;
  layout?: PageLayoutOverride;
};

export type DocumentManifest = {
  schemaVersion: 1;
  projectId?: string;
  name: string;
  outputFormat: DocumentOutputFormat;
  layout?: DocumentLayoutSettings;
  pages: Array<{
    id: string;
    uploadId: string;
    sourcePage: number;
    rotation: number;
    layout?: PageLayoutOverride;
    sourceName?: string;
    sourceKind?: string;
    sourcePageCount?: number;
  }>;
};

export type DocumentProject = {
  id: string;
  ownerId?: string;
  isGuest: boolean;
  name: string;
  outputFormat: DocumentOutputFormat;
  pageCount: number;
  manifest: DocumentManifest;
  expiresAt?: string;
  createdAt: string;
  updatedAt: string;
};

export type ExportJob = {
  id: string;
  state: 'queued' | 'running' | 'succeeded' | 'failed' | 'cancelled';
  targetFormat: string;
  outputSize?: number;
  error?: string;
};

export const DEFAULT_DOCUMENT_FORMATS: DocumentFormatInfo[] = [
  {
    id: 'pdf',
    label: 'PDF Document',
    extension: 'pdf',
    mimeType: 'application/pdf',
    preservesNativePdf: true,
    rasterizesVector: false,
    fidelitySummary: 'Preserves native PDF vector text/graphics and embeds original JPEG/PNG bytes without needless resampling.',
  },
  {
    id: 'cbz',
    label: 'CBZ (Comic Book Archive)',
    extension: 'cbz',
    mimeType: 'application/vnd.comicbook+zip',
    preservesNativePdf: false,
    rasterizesVector: true,
    fidelitySummary: 'Ordered ZIP of page images preserving original JPEG/PNG bytes for unrotated raster pages.',
    fidelityWarning: 'PDF and Office pages are rendered as raster page images; selectable vector text is not preserved.',
  },
  {
    id: 'zip',
    label: 'Ordered Images ZIP',
    extension: 'zip',
    mimeType: 'application/zip',
    preservesNativePdf: false,
    rasterizesVector: true,
    fidelitySummary: 'Numbered page images in export order alongside a non-destructive manifest.json.',
    fidelityWarning: 'PDF and Office pages are rendered as PNG images; vector text is not preserved.',
  },
  {
    id: 'tiff',
    label: 'Multipage TIFF',
    extension: 'tiff',
    mimeType: 'image/tiff',
    preservesNativePdf: false,
    rasterizesVector: true,
    fidelitySummary: 'Single multipage TIFF file containing one uncompressed RGB page per input page.',
    fidelityWarning: 'All pages are stored as raster images; selectable PDF text is not preserved.',
  },
  {
    id: 'docx',
    label: 'Word Document (DOCX)',
    extension: 'docx',
    mimeType: 'application/vnd.openxmlformats-officedocument.wordprocessingml.document',
    preservesNativePdf: false,
    rasterizesVector: true,
    fidelitySummary: 'Paginated DOCX placing one page image per document page at its physical dimensions.',
    fidelityWarning: 'Paginated image assembly — does not convert scans or PDFs into editable flowing Word paragraphs.',
  },
  {
    id: 'pptx',
    label: 'PowerPoint Presentation (PPTX)',
    extension: 'pptx',
    mimeType: 'application/vnd.openxmlformats-officedocument.presentationml.presentation',
    preservesNativePdf: false,
    rasterizesVector: true,
    fidelitySummary: 'Paginated PPTX placing one page image per slide sized to match the page aspect ratio.',
    fidelityWarning: 'Paginated slide assembly — pages are embedded as slide images rather than editable text boxes.',
  },
  {
    id: 'odt',
    label: 'OpenDocument Text (ODT)',
    extension: 'odt',
    mimeType: 'application/vnd.oasis.opendocument.text',
    preservesNativePdf: false,
    rasterizesVector: true,
    fidelitySummary: 'Paginated ODT placing one page image per page at its physical dimensions.',
    fidelityWarning: 'Paginated image assembly — does not reconstruct editable flowing text.',
  },
  {
    id: 'odp',
    label: 'OpenDocument Presentation (ODP)',
    extension: 'odp',
    mimeType: 'application/vnd.oasis.opendocument.presentation',
    preservesNativePdf: false,
    rasterizesVector: true,
    fidelitySummary: 'Paginated ODP placing one page image per slide.',
    fidelityWarning: 'Paginated slide assembly — pages are embedded as slide images rather than editable text boxes.',
  },
];
