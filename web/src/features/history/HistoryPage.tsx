import { useEffect, useState } from 'react';
import { NavLink } from 'react-router-dom';
import { ArrowLeft, ArrowRight, CircleAlert, Clock3, FileUp, LoaderCircle, RefreshCw } from 'lucide-react';
import { Empty, PageHeading } from '../../components/common';
import { explain } from '../format';
import type { Session } from '../session';
import { apiRequest } from '../session';
import { cancelBatchItem, cancelJob, downloadBatchArchive, downloadJobResult, runBatchAction } from './api';
import { BatchEntry, JobEntry, type EntryActions } from './HistoryEntries';
import { HistoryFilters, type OwnerOption } from './HistoryFilters';
import { useHistory } from './useHistory';
import './history.css';

// Jobs & history: one filterable stream of standalone files and grouped batches.
export function HistoryPage({ notify, session }: { notify: (message: string) => void; session: Session }) {
  const isAdmin = session.role === 'admin';
  const history = useHistory(isAdmin);
  const { filters, entries, total, offset, pageSize, details, setDetails } = history;
  const [retrySelection, setRetrySelection] = useState<Record<string, number[]>>({});
  const [owners, setOwners] = useState<OwnerOption[] | undefined>();

  useEffect(() => {
    if (!isAdmin) return;
    apiRequest<{ users: { id: string; username: string }[] }>('/users')
      .then(data => setOwners(data.users.map(user => ({ id: user.id, name: user.username }))))
      .catch(() => setOwners(undefined));
  }, [isAdmin]);

  const attempt = async (work: () => Promise<unknown>, done?: string) => {
    try {
      await work();
      if (done) notify(done);
      await history.refresh();
    } catch (error) {
      notify(explain((error as Error).message));
    }
  };
  const remember = (batch: unknown, id: string) => setDetails(current => ({ ...current, [id]: batch as (typeof current)[string] }));
  const actions: EntryActions = {
    cancelJob: id => void attempt(() => cancelJob(id), 'Cancellation requested.'),
    downloadJob: job => void downloadJobResult(job).catch(error => notify(explain((error as Error).message))),
    downloadBatch: id => void downloadBatchArchive(id).catch(error => notify(explain((error as Error).message))),
    batchAction: (id, action, items) => void attempt(async () => {
      remember(await runBatchAction(id, action, items), id);
      setRetrySelection(current => ({ ...current, [id]: [] }));
    }, action === 'retry' ? 'Selected failed items queued again.' : action === 'cancel' ? 'Batch cancellation requested.' : 'Pending items resumed.'),
    cancelItem: (batchId, index) => void attempt(async () => remember(await cancelBatchItem(batchId, index), batchId)),
  };
  const selectRetry = (id: string, index: number, selected: boolean) =>
    setRetrySelection(current => ({ ...current, [id]: selected ? [...(current[id] || []), index] : (current[id] || []).filter(value => value !== index) }));

  const filtered = history.activeFilters > 0;
  const description = session.guest
    ? `Temporary guest jobs · results expire after ${session.guestResultExpirationHours || 24} hours.`
    : 'Everything you have converted or compressed, single files and batches together.';
  return (
    <>
      <PageHeading eyebrow="WORKSPACE / HISTORY" title="Jobs & history" description={description} action={<button className="secondary" onClick={() => void history.refresh()}><RefreshCw size={15} /> Refresh</button>} />
      <HistoryFilters filters={filters} activeCount={history.activeFilters} owners={isAdmin ? owners : undefined} onChange={history.setFilters} onReset={history.reset} />
      {history.error && <div className="banner error"><CircleAlert size={18} /><span>{history.error}</span><button className="text-btn" onClick={() => void history.refresh()}>Retry</button></div>}
      <p className="history-count" role="status" aria-live="polite">{history.loading ? 'Loading…' : `${total} ${total === 1 ? 'result' : 'results'}${filtered ? (total === 1 ? ' matches your filters' : ' match your filters') : ''}`}</p>
      <div className="history-list">
        {history.loading && entries.length === 0 ? <div className="loading-row"><LoaderCircle className="spin" /> Loading jobs…</div>
          : entries.length === 0 ? (
            <Empty icon={Clock3} title={filtered ? 'Nothing matches these filters' : 'No jobs yet'} detail={filtered ? 'Try a different search or clear a filter.' : 'Your conversions and compressions will appear here.'}
              action={filtered ? <button className="secondary" onClick={history.reset}>Reset filters</button> : <NavLink className="secondary" to="/convert"><FileUp size={15} /> Convert a file</NavLink>} />
          ) : entries.map(entry => entry.type === 'batch' && entry.batch
            ? <BatchEntry key={'batch-' + entry.batch.id} entry={entry} detail={details[entry.batch.id]} retrySelection={retrySelection[entry.batch.id] || []} onToggle={history.loadDetails} onSelectRetry={selectRetry} actions={actions} />
            : entry.job ? <JobEntry key={'job-' + entry.job.id} entry={entry} actions={actions} /> : null)}
      </div>
      {entries.length > 0 && (
        <nav className="pagination" aria-label="History pages">
          <span>Showing {offset + 1}–{Math.min(offset + entries.length, total)} of {total}</span>
          <div>
            <button className="secondary small" disabled={offset === 0} onClick={() => history.setOffset(Math.max(0, offset - pageSize))}><ArrowLeft size={14} /> Previous</button>
            <button className="secondary small" disabled={offset + entries.length >= total} onClick={() => history.setOffset(offset + pageSize)}>Next <ArrowRight size={14} /></button>
          </div>
        </nav>
      )}
    </>
  );
}
