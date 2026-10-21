import { ArrowDownToLine, ArrowRight, ChevronDown, X } from 'lucide-react';
import { StatePill } from '../../components/common';
import { bytes, explain, fileStatus, pretty } from '../format';
import type { Batch, HistoryEntry } from './types';

const operation = (mode?: string) => (mode === 'compress' ? 'Compression' : 'Conversion');
const plural = (count: number, word: string) => `${count} ${word}${count === 1 ? '' : 's'}`;
const when = (value: string) => new Date(value).toLocaleString();

function Savings({ saved, input, output }: { saved?: number; input?: number; output?: number }) {
  if (!output) return <>—</>;
  const percent = input && saved ? (saved / input) * 100 : 0;
  return (
    <>
      <span>{bytes(output)}</span>
      {saved ? <small className="job-savings">Saved {bytes(saved)} · {percent.toFixed(1)}%</small> : null}
    </>
  );
}

export type EntryActions = {
  cancelJob: (id: string) => void;
  downloadJob: (job: NonNullable<HistoryEntry['job']>) => void;
  downloadBatch: (id: string) => void;
  batchAction: (id: string, action: 'cancel' | 'retry' | 'resume', items?: number[]) => void;
  cancelItem: (batchId: string, index: number) => void;
};

export function JobEntry({ entry, actions }: { entry: HistoryEntry; actions: EntryActions }) {
  const job = entry.job!;
  return (
    <article className="history-entry job-entry">
      <div className="entry-main">
        <div className="job-format">
          <span className="mini-file">{job.sourceFormat}</span><ArrowRight size={14} /><span className="mini-file output">{job.targetFormat}</span>
        </div>
        {entry.fileName && <b className="entry-title" title={entry.fileName}>{entry.fileName}</b>}
        <small className="job-id">{operation(job.mode)} · {job.id}</small>
        <small className="job-id">Original · {fileStatus(job.originalState)}</small>
      </div>
      <div className="entry-profile" data-label="Profile"><b>{job.profileId}</b><small>v{job.profileVersion}</small></div>
      <div className="entry-status">
        <StatePill state={job.state} />
        {job.state === 'running' && <div className="table-progress" aria-label="Processing; exact progress unavailable"><span className="indeterminate-progress" /></div>}
        {job.state === 'failed' && job.error && <small className="error-hint" title={job.error}>{explain(job.error)}</small>}
      </div>
      <div className="entry-date" data-label="Created">{when(job.createdAt)}{entry.ownerName && <small>{entry.ownerName}</small>}</div>
      <div className="entry-size" data-label="Output size"><Savings saved={job.savedBytes} input={job.inputSize} output={job.outputSize} /></div>
      <div className="row-actions">
        {(job.state === 'queued' || job.state === 'running') && <button className="icon-btn" title="Cancel job" aria-label="Cancel job" onClick={() => actions.cancelJob(job.id)}><X size={16} /></button>}
        {job.state === 'succeeded' && job.resultState === 'available' && <button className="icon-btn download-btn" title="Download result" aria-label="Download result" onClick={() => actions.downloadJob(job)}><ArrowDownToLine size={17} /></button>}
        {job.state === 'succeeded' && job.resultState !== 'available' && <small className="error-hint">Result {fileStatus(job.resultState)}</small>}
      </div>
    </article>
  );
}

function BatchProgress({ batch }: { batch: Batch }) {
  const done = batch.succeeded + batch.failed + batch.cancelled;
  return (
    <div className="batch-progress" role="progressbar" aria-label={`${done} of ${batch.total} files finished`} aria-valuemin={0} aria-valuemax={batch.total} aria-valuenow={done}>
      <span className="ok" style={{ width: (batch.succeeded / batch.total) * 100 + '%' }} />
      <span className="bad" style={{ width: (batch.failed / batch.total) * 100 + '%' }} />
      <span className="skip" style={{ width: (batch.cancelled / batch.total) * 100 + '%' }} />
    </div>
  );
}

export function BatchEntry({ entry, detail, retrySelection, onToggle, onSelectRetry, actions }: {
  entry: HistoryEntry;
  detail?: Batch;
  retrySelection: number[];
  onToggle: (id: string, open: boolean) => void;
  onSelectRetry: (id: string, index: number, selected: boolean) => void;
  actions: EntryActions;
}) {
  const batch = entry.batch!;
  const items = detail?.items || batch.items;
  const more = batch.total - 1;
  return (
    <details className="history-entry batch-history-card" onToggle={event => onToggle(batch.id, event.currentTarget.open)}>
      <summary>
        <span className="batch-summary-main">
          <b>{operation(batch.mode)} batch</b>
          {entry.fileName && <span className="batch-file-hint" title={entry.fileName}>{entry.fileName}{more > 0 ? ` and ${plural(more, 'other')}` : ''}</span>}
          <small>{plural(batch.total, 'file')} · {when(batch.createdAt)}{entry.ownerName ? ` · ${entry.ownerName}` : ''}</small>
          <small className="batch-id">ID {batch.id}</small>
          {entry.totals && entry.totals.outputBytes > 0 && <small>{entry.totals.savedBytes ? `Saved ${bytes(entry.totals.savedBytes)} across successful files` : `${bytes(entry.totals.outputBytes)} output`}</small>}
        </span>
        <span className="batch-summary-counts">
          <span className="batch-state-line"><StatePill state={batch.state} /><ChevronDown className="batch-chevron" size={16} aria-hidden="true" /></span>
          <small>{batch.succeeded} done · {batch.failed} failed · {batch.cancelled} canceled</small>
        </span>
        <BatchProgress batch={batch} />
      </summary>
      <div className="batch-card-actions">
        {(batch.state === 'queued' || batch.state === 'running') && <button className="secondary small" onClick={event => { event.preventDefault(); actions.batchAction(batch.id, 'cancel'); }}>Cancel batch</button>}
        {batch.failed > 0 && <button className="secondary small" onClick={event => { event.preventDefault(); actions.batchAction(batch.id, 'retry'); }}>Retry failed</button>}
        {retrySelection.length > 0 && <button className="secondary small" onClick={event => { event.preventDefault(); actions.batchAction(batch.id, 'retry', retrySelection); }}>Retry selected ({retrySelection.length})</button>}
        {items.some(item => item.state === 'pending' || item.state === 'submitting') && <button className="secondary small" onClick={event => { event.preventDefault(); actions.batchAction(batch.id, 'resume'); }}>Resume submission</button>}
        {batch.succeeded > 0 && <button className="secondary small" onClick={event => { event.preventDefault(); actions.downloadBatch(batch.id); }}>Download successful files (.zip)</button>}
      </div>
      <div className="batch-item-list">
        {!detail ? <div className="batch-details-loading">Open to load item details…</div> : (detail.items || []).map(item => (
          <article className="batch-item-row" key={item.index}>
            <div className="batch-item-number">
              {item.state === 'failed' && <input type="checkbox" aria-label={`Select failed item ${item.index + 1} to retry`} checked={retrySelection.includes(item.index)} onChange={event => onSelectRetry(batch.id, item.index, event.target.checked)} />}
              <span>{String(item.index + 1).padStart(2, '0')}</span>
            </div>
            <div className="batch-item-content">
              <b>{item.fileName || `File ${item.index + 1}`}</b>
              <small>{item.job ? `${item.job.sourceFormat.toUpperCase()} → ${item.job.targetFormat.toUpperCase()}` : item.uploadId}</small>
              {item.job && <small>{item.job.profileId} · v{item.job.profileVersion}</small>}
              {item.job && <small className="job-id">Job · {item.job.id}</small>}
              {item.job?.outputSize ? <small>{bytes(item.job.outputSize)}{item.job.savedBytes ? ` · saved ${bytes(item.job.savedBytes)} (${item.job.savingsPercent?.toFixed(1)}%)` : ''}</small> : null}
              {item.job?.state === 'succeeded' && item.job.resultState !== 'available' && <small className="error-hint">Result {fileStatus(item.job.resultState)}</small>}
              {item.error && <p className="error-hint">{explain(item.error)}</p>}
              {item.attempts && item.attempts.length > 0 && (
                <details className="batch-attempt-details">
                  <summary>{plural(item.attempts.length, 'execution attempt')}</summary>
                  {item.attempts.map(attempt => (
                    <div key={attempt.number}>
                      <b>Attempt {attempt.number} · {pretty(attempt.state)}</b>
                      <small>{when(attempt.startedAt)}{attempt.finishedAt ? ' · finished ' + when(attempt.finishedAt) : ''}</small>
                      {attempt.error && <small>{explain(attempt.error)}</small>}
                    </div>
                  ))}
                </details>
              )}
            </div>
            <StatePill state={item.state} />
            <div className="row-actions">
              {(item.state === 'queued' || item.state === 'running') && <button className="icon-btn" aria-label="Cancel this file" onClick={() => actions.cancelItem(batch.id, item.index)}><X size={16} /></button>}
              {item.job?.state === 'succeeded' && item.job.resultState === 'available' && <button className="icon-btn download-btn" aria-label="Download this result" onClick={() => actions.downloadJob(item.job!)}><ArrowDownToLine size={17} /></button>}
            </div>
          </article>
        ))}
      </div>
    </details>
  );
}
