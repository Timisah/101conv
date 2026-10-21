import { useState } from 'react';
import { NavLink } from 'react-router-dom';
import { ArrowDownToLine, ArrowRight, Check, CircleAlert, LoaderCircle, X } from 'lucide-react';
import { StatePill } from '../../components/common';
import { bytes, explain, fileStatus } from '../format';
import { downloadBatchArchive, downloadJobResult } from '../history/api';
import type { Batch, Job } from '../history/types';
import { isBatchActive, useBatchProgress } from './useBatchProgress';
import './results.css';

const verb = (mode: string) => (mode === 'compress' ? 'Compressing' : 'Converting');
const plural = (count: number, word: string) => `${count} ${word}${count === 1 ? '' : 's'}`;

function summary(batch: Batch) {
  const done = batch.succeeded + batch.failed + batch.cancelled;
  if (isBatchActive(batch)) return `${done} of ${plural(batch.total, 'file')} finished`;
  if (batch.succeeded === batch.total) return batch.total === 1 ? 'Finished' : `All ${batch.total} files finished`;
  if (batch.succeeded === 0) return batch.cancelled === batch.total ? 'Canceled' : 'Nothing could be processed';
  return `${batch.succeeded} of ${batch.total} succeeded · ${batch.failed + batch.cancelled} did not`;
}

// Progress and results for one submission, shown where it was started so the
// user never has to leave the workspace to collect the output.
export function SubmissionTracker({ batchId, onDismiss, notify }: { batchId: string; onDismiss: () => void; notify: (message: string) => void }) {
  const { batch, error, retry } = useBatchProgress(batchId);
  const [busy, setBusy] = useState('');

  const run = async (key: string, work: () => Promise<void>) => {
    if (busy) return; // one download at a time, so a double click cannot fetch twice
    setBusy(key);
    try { await work(); } catch (e) { notify(explain((e as Error).message)); } finally { setBusy(''); }
  };

  if (error) {
    return <section className="submission-tracker error" role="status"><CircleAlert size={18} /><div><b>Couldn’t load this submission’s progress</b><span>{error}</span></div><button className="text-btn" onClick={retry}>Retry</button></section>;
  }
  if (!batch) return <section className="submission-tracker" role="status" aria-busy="true"><LoaderCircle className="spin" size={18} /><div><b>Starting…</b></div></section>;

  const active = isBatchActive(batch);
  const single = batch.total === 1;
  const only = batch.items[0];
  const downloadable = (job?: Job) => job?.state === 'succeeded' && job.resultState === 'available';
  const failedItems = batch.items.filter(item => item.state === 'failed');
  const done = batch.succeeded + batch.failed + batch.cancelled;

  return (
    <section className={'submission-tracker' + (active ? ' active' : '')} role="status" aria-live="polite">
      <header>
        <span className="tracker-icon">{active ? <LoaderCircle className="spin" size={18} /> : batch.succeeded > 0 ? <Check size={18} /> : <CircleAlert size={18} />}</span>
        <div className="tracker-title">
          <b>{active ? `${verb(batch.mode)} ${single && only?.fileName ? only.fileName : plural(batch.total, 'file')}…` : summary(batch)}</b>
          <span>{active ? summary(batch) : single && only?.fileName ? only.fileName : plural(batch.total, 'file')}</span>
        </div>
        <StatePill state={batch.state} />
        <button className="icon-btn quiet" aria-label="Dismiss this summary" onClick={onDismiss}><X size={16} /></button>
      </header>
      {!single && (
        <div className="batch-progress" role="progressbar" aria-label={`${done} of ${batch.total} files finished`} aria-valuemin={0} aria-valuemax={batch.total} aria-valuenow={done}>
          <span className="ok" style={{ width: (batch.succeeded / batch.total) * 100 + '%' }} />
          <span className="bad" style={{ width: (batch.failed / batch.total) * 100 + '%' }} />
          <span className="skip" style={{ width: (batch.cancelled / batch.total) * 100 + '%' }} />
        </div>
      )}
      {single && only?.state === 'failed' && only.error && <p className="error-hint">{explain(only.error)}</p>}
      {single && only?.job?.state === 'succeeded' && !downloadable(only.job) && <p className="error-hint">Result {fileStatus(only.job.resultState)}.</p>}
      {single && downloadable(only?.job) && only?.job && (
        <div className="tracker-actions">
          <button className="primary" disabled={Boolean(busy)} onClick={() => void run('job', () => downloadJobResult(only.job!))}>{busy === 'job' ? <LoaderCircle className="spin" size={16} /> : <ArrowDownToLine size={16} />} Download result</button>
          {only.job.outputSize ? <span className="tracker-size">{bytes(only.job.outputSize)}{only.job.savedBytes ? ` · saved ${bytes(only.job.savedBytes)} (${only.job.savingsPercent?.toFixed(1)}%)` : ''}</span> : null}
        </div>
      )}
      {!single && !active && batch.succeeded > 0 && (
        <div className="tracker-actions">
          <button className="primary" disabled={Boolean(busy)} onClick={() => void run('zip', () => downloadBatchArchive(batch.id))}>{busy === 'zip' ? <LoaderCircle className="spin" size={16} /> : <ArrowDownToLine size={16} />} Download all successful files (.zip)</button>
        </div>
      )}
      {!single && batch.items.length > 0 && (
        <details className="tracker-files">
          <summary>Files ({batch.items.length}){failedItems.length > 0 ? ` · ${failedItems.length} need attention` : ''}</summary>
          <ul>
            {batch.items.map(item => (
              <li key={item.index}>
                <div><b title={item.fileName}>{item.fileName || `File ${item.index + 1}`}</b>{item.error && <small className="error-hint">{explain(item.error)}</small>}{item.job?.state === 'succeeded' && !downloadable(item.job) && <small className="error-hint">Result {fileStatus(item.job.resultState)}</small>}</div>
                <StatePill state={item.state} />
                {downloadable(item.job) && <button className="icon-btn download-btn" aria-label={`Download ${item.fileName || 'file ' + (item.index + 1)}`} disabled={Boolean(busy)} onClick={() => void run('item' + item.index, () => downloadJobResult(item.job!))}><ArrowDownToLine size={17} /></button>}
              </li>
            ))}
          </ul>
        </details>
      )}
      <footer><NavLink className="text-link" to="/jobs">Open full history <ArrowRight size={13} /></NavLink></footer>
    </section>
  );
}
