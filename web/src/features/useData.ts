import { useCallback, useEffect, useState } from 'react';
import { explain } from './format.ts';
import { apiRequest } from './session.ts';

export function mergeDataDefaults<T>(initial: T, incoming: T): T {
  if (
    initial !== null &&
    typeof initial === 'object' &&
    !Array.isArray(initial) &&
    incoming !== null &&
    typeof incoming === 'object' &&
    !Array.isArray(incoming)
  ) {
    const merged: Record<string, unknown> = { ...(initial as Record<string, unknown>) };
    for (const [key, value] of Object.entries(incoming as Record<string, unknown>)) {
      if (value !== null && value !== undefined) {
        merged[key] = value;
      }
    }
    return merged as T;
  }
  return incoming ?? initial;
}

export function useData<T>(path: string, initial: T, _auth = true) {
  const [data, setData] = useState<T>(initial);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const response = await apiRequest<T>(path);
      setData(mergeDataDefaults(initial, response));
      setError('');
    } catch (err) {
      setError(explain((err as Error).message));
    } finally {
      setLoading(false);
    }
  }, [path]);

  useEffect(() => {
    void load();
  }, [load]);

  return { data, setData, loading, error, refresh: load };
}
