import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from 'react';
import { explain } from '../format';
import { apiRequest } from '../session';
import { entryKey, latestEntries, normalizeCatalogResponse, statusOf } from './catalogData';
import type { Catalog, CatalogEntry, InstalledEngine, InstalledProfile, Repo } from './types';

const CACHE_KEY = '101conv-catalog-cache';
const SEEN_KEY = '101conv-catalog-seen';
const CACHE_MS = 30 * 60 * 1000;

const readStorage = <T,>(storage: Storage | undefined, key: string): T | undefined => { try { const raw = storage?.getItem(key); return raw ? (JSON.parse(raw) as T) : undefined; } catch { return undefined; } };
const writeStorage = (storage: Storage | undefined, key: string, value: unknown) => { try { storage?.setItem(key, JSON.stringify(value)); } catch { /* storage may be blocked */ } };
const safeSession = () => { try { return window.sessionStorage; } catch { return undefined; } };
const safeLocal = () => { try { return window.localStorage; } catch { return undefined; } };

type CatalogValue = {
  loading: boolean; error: string; repos: Repo[]; catalogs: Record<string, Catalog>;
  installedProfiles: InstalledProfile[]; installedEngines: InstalledEngine[];
  entries: ReturnType<typeof latestEntries>;
  updates: ReturnType<typeof latestEntries>;
  unseenNew: ReturnType<typeof latestEntries>;
  seenKeys: Set<string>;
  actionableCount: number;
  refresh: (force?: boolean) => Promise<void>;
  refreshInstalled: () => Promise<void>;
  markSeen: () => void;
};

const empty: CatalogValue = { loading: false, error: '', repos: [], catalogs: {}, installedProfiles: [], installedEngines: [], entries: [], updates: [], unseenNew: [], seenKeys: new Set(), actionableCount: 0, refresh: async () => {}, refreshInstalled: async () => {}, markSeen: () => {} };
const Context = createContext<CatalogValue>(empty);
export const useCatalog = () => useContext(Context);

const browsable = (repo: Repo) => repo.enabled && (repo.trust === 'pinned' || repo.id === '101conv-official');

// Loads what an administrator needs to know about converter availability once
// per session: installed profiles and engines (local, cheap) and the browsable
// catalogs (a network fetch, cached for 30 minutes unless refresh is forced).
export function CatalogProvider({ enabled, children }: { enabled: boolean; children: ReactNode }) {
  const [state, setState] = useState({ loading: enabled, error: '', repos: [] as Repo[], catalogs: {} as Record<string, Catalog>, installedProfiles: [] as InstalledProfile[], installedEngines: [] as InstalledEngine[] });
  const [seen, setSeen] = useState<Set<string>>(() => new Set(readStorage<string[]>(safeLocal(), SEEN_KEY) || []));

  const refreshInstalled = useCallback(async () => {
    const [profiles, engines] = await Promise.all([
      apiRequest<{ profiles: InstalledProfile[] }>('/profiles').catch(() => ({ profiles: [] as InstalledProfile[] })),
      apiRequest<{ engines: InstalledEngine[] }>('/engines').catch(() => ({ engines: [] as InstalledEngine[] })),
    ]);
    setState(current => ({ ...current, installedProfiles: profiles.profiles || [], installedEngines: engines.engines || [] }));
  }, []);

  const refresh = useCallback(async (force = false) => {
    if (!enabled) return;
    setState(current => ({ ...current, loading: true, error: '' }));
    try {
      const [{ repositories }] = await Promise.all([apiRequest<{ repositories: Repo[] }>('/repositories'), refreshInstalled()]);
      const repos = repositories || [];
      const cached = force ? undefined : readStorage<{ at: number; catalogs: Record<string, Catalog> }>(safeSession(), CACHE_KEY);
      let catalogs: Record<string, Catalog> = {};
      let error = '';
      if (cached && Date.now() - cached.at < CACHE_MS) {
        catalogs = cached.catalogs;
      } else {
        await Promise.all(repos.filter(browsable).map(async repo => {
          try {
            const catalog = normalizeCatalogResponse(await apiRequest<unknown>('/repositories/' + repo.id + '/catalog'));
            if (!catalog) throw new Error('The repository returned an invalid catalog response. Refresh the catalog or update Core.');
            catalogs[repo.id] = catalog;
          } catch (e) { error = explain((e as Error).message) || 'Could not load repository catalog.'; }
        }));
        if (!error) writeStorage(safeSession(), CACHE_KEY, { at: Date.now(), catalogs });
      }
      setState(current => ({ ...current, loading: false, error, repos, catalogs }));
    } catch (e) {
      setState(current => ({ ...current, loading: false, error: explain((e as Error).message) }));
    }
  }, [enabled, refreshInstalled]);

  useEffect(() => { if (enabled) void refresh(false); }, [enabled, refresh]);

  const entries = useMemo(() => {
    const raw: CatalogEntry[] = Object.entries(state.catalogs).filter(([id]) => state.repos.some(repo => repo.id === id && browsable(repo))).flatMap(([repoId, catalog]) => [
      ...catalog.profiles.map(pkg => ({ repoId, repo: catalog.repository, kind: 'profile' as const, pkg })),
      ...catalog.engines.map(pkg => ({ repoId, repo: catalog.repository, kind: 'engine' as const, pkg })),
    ]);
    return latestEntries(raw);
  }, [state.catalogs, state.repos]);

  const updates = useMemo(() => entries.filter(entry => entry.kind === 'profile' && statusOf(entry, state.installedProfiles, state.installedEngines).kind === 'update'), [entries, state.installedProfiles, state.installedEngines]);

  // The first time an administrator ever loads the catalog, everything is
  // marked as already seen so an existing catalog does not read as "new".
  useEffect(() => {
    if (!enabled || !entries.length || readStorage(safeLocal(), SEEN_KEY) !== undefined) return;
    const initial = entries.filter(entry => entry.kind === 'profile').map(entryKey);
    writeStorage(safeLocal(), SEEN_KEY, initial);
    setSeen(new Set(initial));
  }, [enabled, entries]);

  const unseenNew = useMemo(() => entries.filter(entry => entry.kind === 'profile' && !seen.has(entryKey(entry)) && statusOf(entry, state.installedProfiles, state.installedEngines).kind === 'new'), [entries, seen, state.installedProfiles, state.installedEngines]);

  const markSeen = useCallback(() => {
    const next = new Set(seen);
    entries.filter(entry => entry.kind === 'profile').forEach(entry => next.add(entryKey(entry)));
    if (next.size === seen.size) return;
    writeStorage(safeLocal(), SEEN_KEY, [...next]);
    setSeen(next);
  }, [entries, seen]);

  const value: CatalogValue = { ...state, entries, updates, unseenNew, seenKeys: seen, actionableCount: updates.length + unseenNew.length, refresh, refreshInstalled, markSeen };
  return <Context.Provider value={enabled ? value : empty}>{children}</Context.Provider>;
}
