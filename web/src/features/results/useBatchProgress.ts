import { useCallback, useEffect, useState } from 'react';
import { explain } from '../format';
import { fetchBatch } from '../history/api';
import type { Batch } from '../history/types';

const POLL_MS = 2000;
const MAX_FAILURES = 3;

// A batch is finished once no item can still change state.
export const isBatchActive = (batch?: Batch) =>
  !batch || batch.state === 'queued' || batch.state === 'running' || batch.items.some(item => item.state === 'pending' || item.state === 'submitting');

// Follows one batch until it finishes. Polling stops as soon as the batch is
// terminal, waits while the tab is hidden, gives up after repeated failures,
// and is cancelled on unmount, so a submission never leaves a timer running.
export function useBatchProgress(batchId: string) {
  const [batch, setBatch] = useState<Batch>();
  const [error, setError] = useState('');
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    let cancelled = false;
    let failures = 0;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const wait = (ms: number) => { timer = setTimeout(() => void step(), ms); };
    const step = async () => {
      if (document.hidden) { wait(POLL_MS); return; }
      try {
        const next = await fetchBatch(batchId);
        if (cancelled) return;
        failures = 0;
        setError('');
        setBatch(next);
        if (isBatchActive(next)) wait(POLL_MS);
      } catch (e) {
        if (cancelled) return;
        failures += 1;
        if (failures >= MAX_FAILURES) { setError(explain((e as Error).message)); return; }
        wait(POLL_MS * 2);
      }
    };
    void step();
    return () => { cancelled = true; if (timer) clearTimeout(timer); };
  }, [batchId, attempt]);

  const retry = useCallback(() => { setError(''); setAttempt(value => value + 1); }, []);
  return { batch, error, retry };
}
