import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import { explain } from '../format';
import { fetchBatch, fetchHistory, PAGE_SIZE } from './api';
import { emptyFilters, type Batch, type HistoryEntry, type HistoryFilters } from './types';

const FILTER_KEYS = Object.keys(emptyFilters) as (keyof HistoryFilters)[];
const isActive = (state: string) => state === 'queued' || state === 'running';

// Filters live in the URL so a filtered view can be bookmarked or shared and
// survives a reload; unknown or malformed values fall back to "no filter".
export function filtersFromParams(params: URLSearchParams): HistoryFilters {
  const pick = <T extends string>(key: string, allowed: readonly T[]) => (allowed as readonly string[]).includes(params.get(key) || '') ? (params.get(key) as T) : ('' as T);
  return {
    q: (params.get('q') || '').slice(0, 120),
    mode: pick('mode', ['convert', 'compress']),
    type: pick('type', ['job', 'batch']),
    status: pick('status', ['queued', 'running', 'succeeded', 'failed', 'cancelled', 'partial']),
    range: pick('range', ['24h', '7d', '30d']),
    owner: params.get('owner') || '',
  };
}

export function useHistory(canFilterOwners: boolean) {
  const [params, setParams] = useSearchParams();
  const filters = useMemo(() => {
    const parsed = filtersFromParams(params);
    return canFilterOwners ? parsed : { ...parsed, owner: '' };
  }, [params, canFilterOwners]);
  const filterKey = JSON.stringify(filters);
  const [offset, setOffset] = useState(0);
  const [entries, setEntries] = useState<HistoryEntry[]>([]);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [details, setDetails] = useState<Record<string, Batch>>({});
  const open = useRef(new Set<string>());
  const latest = useRef(0);
  const filtersRef = useRef(filters);
  filtersRef.current = filters;

  const load = useCallback(async (showSpinner: boolean) => {
    const ticket = ++latest.current;
    if (showSpinner) setLoading(true);
    try {
      const page = await fetchHistory(filtersRef.current, offset);
      const refreshed = await Promise.all([...open.current].map(id => fetchBatch(id).then(batch => [id, batch] as const).catch(() => null)));
      if (ticket !== latest.current) return;
      setEntries(page.entries);
      setTotal(page.total);
      setDetails(current => ({ ...current, ...Object.fromEntries(refreshed.filter(Boolean) as [string, Batch][]) }));
      setError('');
    } catch (e) {
      if (ticket === latest.current) setError(explain((e as Error).message));
    } finally {
      if (ticket === latest.current) setLoading(false);
    }
  }, [offset]);

  // A changed filter or page is a new query: show the spinner and drop stale rows' scroll.
  useEffect(() => { void load(true); }, [load, filterKey]);

  const hasActive = entries.some(entry => isActive(entry.job?.state || entry.batch?.state || ''));
  useEffect(() => {
    const timer = setInterval(() => { if (!document.hidden) void load(false); }, hasActive ? 5000 : 15000);
    return () => clearInterval(timer);
  }, [load, hasActive]);

  const setFilters = useCallback((patch: Partial<HistoryFilters>) => {
    const next = new URLSearchParams(params);
    for (const [key, value] of Object.entries(patch)) {
      if (value) next.set(key, value); else next.delete(key);
    }
    setOffset(0);
    setParams(next, { replace: true });
  }, [params, setParams]);

  const reset = useCallback(() => {
    const next = new URLSearchParams(params);
    FILTER_KEYS.forEach(key => next.delete(key));
    setOffset(0);
    setParams(next, { replace: true });
  }, [params, setParams]);

  const loadDetails = useCallback(async (id: string, isOpen: boolean) => {
    if (!isOpen) { open.current.delete(id); return; }
    open.current.add(id);
    try { const batch = await fetchBatch(id); setDetails(current => ({ ...current, [id]: batch })); } catch (e) { setError(explain((e as Error).message)); }
  }, []);

  const activeFilters = FILTER_KEYS.filter(key => filters[key]).length;
  return { filters, setFilters, reset, activeFilters, offset, setOffset, entries, total, loading, error, details, setDetails, loadDetails, refresh: () => load(false), pageSize: PAGE_SIZE };
}
