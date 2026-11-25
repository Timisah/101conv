import type { Catalog, CatalogCategory, CatalogEntry, InstalledEngine, InstalledProfile, Package, Status } from './types';

export const compareVersion = (a: string, b: string) => {
  const x = a.split(/[.+-]/).slice(0, 3).map(Number), y = b.split(/[.+-]/).slice(0, 3).map(Number);
  for (let i = 0; i < 3; i++) { const d = (x[i] || 0) - (y[i] || 0); if (d) return d; }
  return 0;
};

export const normalizeCatalogResponse = (value: unknown): Catalog | null => {
  if (!value || typeof value !== 'object') return null;
  const catalog = value as Partial<Catalog>;
  const validPackages = (packages: unknown): packages is Package[] => Array.isArray(packages) && packages.every(pkg => pkg && typeof pkg === 'object' && typeof pkg.id === 'string' && typeof pkg.name === 'string' && typeof pkg.version === 'string');
  if (!catalog.repository || typeof catalog.repository !== 'object' || !validPackages(catalog.engines) || !validPackages(catalog.profiles) || (catalog.incompatibleProfiles !== undefined && !Array.isArray(catalog.incompatibleProfiles))) return null;
  const bundles = Array.isArray(catalog.bundles)
    ? catalog.bundles.filter(b => b && typeof b === 'object' && typeof b.id === 'string' && typeof b.name === 'string' && Array.isArray(b.profileIds))
    : [];
  return { ...catalog, incompatibleProfiles: catalog.incompatibleProfiles || [], bundles } as Catalog;
};

export const catalogProfileType = (pkg: Package) => pkg.type || (pkg.compression || (pkg.rules || []).some((rule: any) => rule.compressionArguments?.length) ? 'legacy' : 'conversion');

// Install and update both act on the highest catalog version of a profile, so
// the catalog shows one card per profile rather than one per released version.
// Older releases are reported so the card can say they exist.
export function latestEntries(entries: CatalogEntry[]): (CatalogEntry & { olderVersions: string[] })[] {
  const groups = new Map<string, CatalogEntry[]>();
  for (const entry of entries) {
    const key = [entry.repoId, entry.kind, entry.pkg.id, entry.kind === 'engine' ? entry.pkg.version : ''].join('|');
    groups.set(key, [...(groups.get(key) || []), entry]);
  }
  return [...groups.values()].map(group => {
    const sorted = [...group].sort((a, b) => compareVersion(b.pkg.version, a.pkg.version));
    return { ...sorted[0], olderVersions: sorted.slice(1).map(entry => entry.pkg.version) };
  });
}

// "New" is a profile ID that is not installed. "Update available" is a higher
// version of an ID that is installed. A different profile is never an update.
export function statusOf(entry: CatalogEntry, profiles: InstalledProfile[], engines: InstalledEngine[]): Status {
  if (entry.kind === 'profile') {
    const installed = profiles.find(item => item.definition.id === entry.pkg.id);
    if (!installed) return { kind: 'new' };
    return compareVersion(entry.pkg.version, installed.definition.version) > 0
      ? { kind: 'update', installed: installed.definition.version, available: entry.pkg.version }
      : { kind: 'installed', version: installed.definition.version };
  }
  const exact = engines.find(item => item.definition.id === entry.pkg.id && item.definition.version === entry.pkg.version);
  if (exact) return { kind: 'installed', version: exact.definition.version };
  const others = engines.filter(item => item.definition.id === entry.pkg.id).map(item => item.definition.version).sort(compareVersion);
  return others.length ? { kind: 'engine-version', installed: others[others.length - 1] } : { kind: 'new' };
}

// Engines a profile needs that are not installed yet; installing the profile adds them.
export const missingEngines = (pkg: Package, engines: InstalledEngine[]) =>
  [...new Set((pkg.engines || []).map((req: any) => String(req.engineId || '')).filter(Boolean))].filter(id => !engines.some(item => item.definition.id === id));

export const entryKey = (entry: { repoId: string; pkg: Package }) => `${entry.repoId}|${entry.pkg.id}|${entry.pkg.version}`;

export function uniqueFormats(pkg: Package): { sources: string[]; targets: string[] } {
  const norm = (values: unknown) =>
    [...new Set((Array.isArray(values) ? values : []).map(v => String(v || '').trim().replace(/^\./, '').toLowerCase()).filter(Boolean))];
  const sources = norm((pkg.rules || []).flatMap((r: any) => r.sources || []));
  const targets = norm((pkg.rules || []).flatMap((r: any) => r.targets || []));
  return { sources, targets };
}

export function packageCapabilities(pkg: Package): string[] {
  const fromEngines = (pkg.engines || []).flatMap((req: any) => Array.isArray(req.capabilities) ? req.capabilities : []);
  const direct = Array.isArray(pkg.capabilities) ? pkg.capabilities : [];
  return [...new Set([...direct, ...fromEngines].map(c => String(c || '').trim().toLowerCase()).filter(Boolean))];
}

export function searchableText(entry: CatalogEntry): string {
  const { sources, targets } = uniqueFormats(entry.pkg);
  const caps = packageCapabilities(entry.pkg);
  return [
    entry.pkg.name,
    entry.pkg.id,
    entry.pkg.description || '',
    entry.repo.name,
    ...sources,
    ...targets,
    ...caps,
  ].join(' ').toLowerCase();
}

const IMAGE_EXTS = new Set(['jpg', 'jpeg', 'png', 'webp', 'avif', 'jxl', 'gif', 'bmp', 'tif', 'tiff', 'heic', 'heif', 'ppm', 'pgm', 'pbm', 'svg', 'ico']);
const AUDIO_EXTS = new Set(['mp3', 'wav', 'flac', 'ogg', 'opus', 'm4a', 'aac', 'wma', 'aiff', 'alac', 'ac3']);
const VIDEO_EXTS = new Set(['mp4', 'mkv', 'webm', 'mov', 'avi', 'm4v', 'wmv', 'flv', 'ts', 'mpeg', 'mpg', '3gp']);
const DOC_EXTS = new Set(['pdf', 'doc', 'docx', 'odt', 'rtf', 'txt', 'xls', 'xlsx', 'ods', 'csv', 'ppt', 'pptx', 'odp', 'html', 'epub']);
const ARCHIVE_EXTS = new Set(['zip', 'tar', 'gz', 'tgz', 'bz2', 'xz', '7z', 'rar', 'cbz']);

export function categoriesOf(entry: CatalogEntry): Exclude<CatalogCategory, 'all'>[] {
  const found = new Set<Exclude<CatalogCategory, 'all'>>();
  const { sources, targets } = uniqueFormats(entry.pkg);
  for (const ext of [...sources, ...targets]) {
    if (IMAGE_EXTS.has(ext)) found.add('images');
    if (AUDIO_EXTS.has(ext)) found.add('audio');
    if (VIDEO_EXTS.has(ext)) found.add('video');
    if (DOC_EXTS.has(ext)) found.add('documents');
    if (ARCHIVE_EXTS.has(ext)) found.add('archives');
  }
  const caps = packageCapabilities(entry.pkg).join(' ') + ' ' + entry.pkg.name.toLowerCase() + ' ' + (entry.pkg.description || '').toLowerCase();
  if (/\b(image|jpeg|jxl|png|webp|avif)\b/.test(caps)) found.add('images');
  if (/\b(audio|mp3|flac|opus|aac|wav)\b/.test(caps)) found.add('audio');
  if (/\b(video|mp4|mkv|webm|h264|hevc|av1)\b/.test(caps)) found.add('video');
  if (/\b(pdf|document|office|libreoffice|poppler|docx|pptx|xlsx)\b/.test(caps)) found.add('documents');
  if (/\b(archive|zip|tar|7z|cbz)\b/.test(caps)) found.add('archives');
  return [...found];
}
