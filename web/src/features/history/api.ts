import { apiRequest } from '../session';
import type { Batch, HistoryFilters, HistoryPageData } from './types';

export const PAGE_SIZE = 25;
const rangeHours: Record<string, number> = { '24h': 24, '7d': 24 * 7, '30d': 24 * 30 };

// The time range is sent as an absolute lower bound so the server stays
// stateless; the same filters always produce the same query for a given instant.
export function historyQuery(filters: HistoryFilters, offset: number, now = Date.now()) {
  const params = new URLSearchParams({ limit: String(PAGE_SIZE), offset: String(offset) });
  if (filters.q.trim()) params.set('q', filters.q.trim());
  if (filters.mode) params.set('mode', filters.mode);
  if (filters.type) params.set('type', filters.type);
  if (filters.status) params.set('status', filters.status);
  if (filters.owner) params.set('owner', filters.owner);
  if (filters.range) params.set('from', new Date(now - rangeHours[filters.range] * 3600_000).toISOString());
  return params.toString();
}

export const fetchHistory = (filters: HistoryFilters, offset: number, signal?: AbortSignal) =>
  apiRequest<HistoryPageData>('/history?' + historyQuery(filters, offset), { signal });

export const fetchBatch = (id: string) => apiRequest<Batch>('/batches/' + id);

export const cancelJob = (id: string) => apiRequest('/jobs/' + id + '/cancel', { method: 'POST' });

export const cancelBatchItem = (batchId: string, index: number) =>
  apiRequest<Batch>(`/batches/${batchId}/items/${index}/cancel`, { method: 'POST' });

export const runBatchAction = (id: string, action: 'cancel' | 'retry' | 'resume', items?: number[]) =>
  apiRequest<Batch>('/batches/' + id + '/' + action, {
    method: 'POST',
    body: action === 'retry' ? JSON.stringify(items?.length ? { items } : {}) : undefined,
  });

async function saveResponse(response: Response, filename: string, fallback: string) {
  if (!response.ok) {
    const body = await response.json().catch(() => ({}));
    throw new Error(body?.error?.message || fallback);
  }
  const url = URL.createObjectURL(await response.blob());
  const anchor = document.createElement('a');
  anchor.href = url;
  anchor.download = filename;
  anchor.click();
  URL.revokeObjectURL(url);
}

export async function downloadJobResult(job: { id: string; mode?: string; targetFormat: string }) {
  const response = await fetch('/api/v1/jobs/' + job.id + '/result', { credentials: 'include' });
  await saveResponse(response, (job.mode === 'compress' ? 'compressed-' : 'converted-') + job.id + '.' + job.targetFormat, 'Result is not ready yet.');
}

export async function downloadBatchArchive(id: string) {
  const response = await fetch('/api/v1/batches/' + id + '/results.zip', { credentials: 'include' });
  await saveResponse(response, '101conv-batch-' + id + '.zip', 'Batch archive is unavailable.');
}
