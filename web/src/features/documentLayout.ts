export type LayoutMode = 'automatic' | 'fit' | 'fill';
export type PageSize = 'auto' | 'a4' | 'letter' | 'custom';
export type PageBackground = 'white' | 'transparent' | 'black';

export type DocumentLayoutSettings = {
  mode: LayoutMode;
  pageSize: PageSize;
  customWidthMm: number;
  customHeightMm: number;
  marginMm: number;
  background: PageBackground;
  positionX: number;
  positionY: number;
};

export type PageLayoutOverride = Partial<DocumentLayoutSettings>;

export type ImageDimensions = { width: number; height: number };

export type PageGeometry = {
  pageWidth: number;
  pageHeight: number;
  imageWidth: number;
  imageHeight: number;
  drawX: number;
  drawY: number;
  drawWidth: number;
  drawHeight: number;
  mode: LayoutMode;
  background: PageBackground;
};

export const POINTS_PER_MM = 72 / 25.4;
export const A4_PAGE_MM = { width: 210, height: 297 } as const;
export const LETTER_PAGE_MM = { width: 215.9, height: 279.4 } as const;
export const DEFAULT_LAYOUT: DocumentLayoutSettings = {
  mode: 'automatic',
  pageSize: 'auto',
  customWidthMm: A4_PAGE_MM.width,
  customHeightMm: A4_PAGE_MM.height,
  marginMm: 0,
  background: 'white',
  positionX: 0.5,
  positionY: 0.5,
};

const AUTO_DPI = 150;
const AUTO_MIN_LONG_EDGE_PT = 144;
const AUTO_MAX_LONG_EDGE_PT = 1080;
const MAX_MARGIN_MM = 50;
const MIN_CUSTOM_MM = 25;
const MAX_CUSTOM_MM = 1000;

const clamp = (value: number, min: number, max: number) => Math.min(max, Math.max(min, value));
const finite = (value: number, fallback: number) => Number.isFinite(value) ? value : fallback;

export function mergeLayoutSettings(base: DocumentLayoutSettings, override?: PageLayoutOverride): DocumentLayoutSettings {
  const next = { ...base, ...(override || {}) };
  return {
    mode: next.mode === 'fit' || next.mode === 'fill' ? next.mode : 'automatic',
    pageSize: next.pageSize === 'a4' || next.pageSize === 'letter' || next.pageSize === 'custom' ? next.pageSize : 'auto',
    customWidthMm: clamp(finite(next.customWidthMm, A4_PAGE_MM.width), MIN_CUSTOM_MM, MAX_CUSTOM_MM),
    customHeightMm: clamp(finite(next.customHeightMm, A4_PAGE_MM.height), MIN_CUSTOM_MM, MAX_CUSTOM_MM),
    marginMm: clamp(finite(next.marginMm, 0), 0, MAX_MARGIN_MM),
    background: next.background === 'transparent' || next.background === 'black' ? next.background : 'white',
    positionX: clamp(finite(next.positionX, 0.5), 0, 1),
    positionY: clamp(finite(next.positionY, 0.5), 0, 1),
  };
}

export function orientedDimensions(dimensions: ImageDimensions, rotation: number): ImageDimensions {
  const quarterTurns = ((Math.round(rotation / 90) % 4) + 4) % 4;
  return quarterTurns % 2 === 0 ? dimensions : { width: dimensions.height, height: dimensions.width };
}

function automaticPageSize(dimensions: ImageDimensions) {
  const width = Math.max(1, finite(dimensions.width, 1));
  const height = Math.max(1, finite(dimensions.height, 1));
  const baseScale = POINTS_PER_MM * 25.4 / AUTO_DPI;
  let pageWidth = width * baseScale;
  let pageHeight = height * baseScale;
  const longEdge = Math.max(pageWidth, pageHeight);
  if (longEdge > AUTO_MAX_LONG_EDGE_PT) {
    const scale = AUTO_MAX_LONG_EDGE_PT / longEdge;
    pageWidth *= scale;
    pageHeight *= scale;
  } else if (longEdge < AUTO_MIN_LONG_EDGE_PT) {
    const scale = AUTO_MIN_LONG_EDGE_PT / longEdge;
    pageWidth *= scale;
    pageHeight *= scale;
  }
  return { width: pageWidth, height: pageHeight };
}

function fixedPageSize(settings: DocumentLayoutSettings, dimensions: ImageDimensions) {
  const sourceLandscape = dimensions.width > dimensions.height;
  const base = settings.pageSize === 'letter' ? LETTER_PAGE_MM : settings.pageSize === 'custom'
    ? { width: settings.customWidthMm, height: settings.customHeightMm }
    : A4_PAGE_MM;
  const pageLandscape = base.width > base.height;
  const swap = sourceLandscape !== pageLandscape;
  return {
    width: (swap ? base.height : base.width) * POINTS_PER_MM,
    height: (swap ? base.width : base.height) * POINTS_PER_MM,
  };
}

export function resolvePageGeometry(dimensions: ImageDimensions, rotation: number, settings: DocumentLayoutSettings, override?: PageLayoutOverride): PageGeometry {
  const effective = mergeLayoutSettings(settings, override);
  const image = orientedDimensions(dimensions, rotation);
  // A page-level Fit/Fill override must still work when the document-level
  // mode is Automatic; an omitted paper size then falls back to A4.
  const automatic = effective.mode === 'automatic';
  const page = automatic ? automaticPageSize(image) : fixedPageSize(effective, image);
  const margin = Math.max(0, Math.min(effective.marginMm * POINTS_PER_MM, Math.min(page.width, page.height) / 2 - 1));
  const contentWidth = Math.max(1, page.width - margin * 2);
  const contentHeight = Math.max(1, page.height - margin * 2);
  const containScale = Math.min(contentWidth / image.width, contentHeight / image.height);
  const coverScale = Math.max(contentWidth / image.width, contentHeight / image.height);
  const scale = effective.mode === 'fill' && !automatic ? coverScale : containScale;
  const drawWidth = image.width * scale;
  const drawHeight = image.height * scale;
  return {
    pageWidth: page.width,
    pageHeight: page.height,
    imageWidth: image.width,
    imageHeight: image.height,
    drawX: margin + (contentWidth - drawWidth) * effective.positionX,
    drawY: margin + (contentHeight - drawHeight) * effective.positionY,
    drawWidth,
    drawHeight,
    mode: effective.mode,
    background: effective.background,
  };
}

export function layoutLabel(settings: DocumentLayoutSettings, override?: PageLayoutOverride) {
  const effective = mergeLayoutSettings(settings, override);
  if (effective.mode === 'automatic') return 'Automatic';
  return `${effective.mode === 'fit' ? 'Fit' : 'Fill'} · ${effective.pageSize === 'letter' ? 'Letter' : effective.pageSize === 'custom' ? 'Custom' : 'A4'}`;
}

export function backgroundRGB(background: PageBackground) {
  if (background === 'black') return [0, 0, 0] as const;
  return [1, 1, 1] as const;
}
