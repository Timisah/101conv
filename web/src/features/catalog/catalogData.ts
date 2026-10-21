import type { Catalog, CatalogEntry, InstalledEngine, InstalledProfile, Package, Status } from './types';

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
  return { ...catalog, incompatibleProfiles: catalog.incompatibleProfiles || [] } as Catalog;
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
