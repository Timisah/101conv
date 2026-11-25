import { BarChart3, RefreshCw, ShieldCheck } from 'lucide-react';
import { Empty, ErrorState, Loading, PageHeading } from '../../components/common';
import { bytes } from '../format';
import { useData } from '../useData';

type AnalyticsOverview = {
  trackingSince: string;
  backfilledJobs: number;
  processedInputBytes: number;
  processedOutputBytes: number;
  savedBytes: number;
  savingsPercent: number;
  totalItems: number;
  succeededItems: number;
  failedItems: number;
  cancelledItems: number;
  convertItems: number;
  compressItems: number;
  standaloneJobs: number;
  batchItems: number;
  batchExecutions: number;
  last30DaysInputBytes: number;
  last30DaysSucceeded: number;
};

type UserStats = {
  ownerId: string;
  displayName: string;
  category: 'user' | 'guest' | 'deleted';
  processedInputBytes: number;
  processedOutputBytes: number;
  savedBytes: number;
  savingsPercent: number;
  totalItems: number;
  succeededItems: number;
  failedItems: number;
  cancelledItems: number;
  convertItems: number;
  compressItems: number;
  standaloneJobs: number;
  batchItems: number;
  batchExecutions: number;
};

const EMPTY_OVERVIEW: AnalyticsOverview = {
  trackingSince: '',
  backfilledJobs: 0,
  processedInputBytes: 0,
  processedOutputBytes: 0,
  savedBytes: 0,
  savingsPercent: 0,
  totalItems: 0,
  succeededItems: 0,
  failedItems: 0,
  cancelledItems: 0,
  convertItems: 0,
  compressItems: 0,
  standaloneJobs: 0,
  batchItems: 0,
  batchExecutions: 0,
  last30DaysInputBytes: 0,
  last30DaysSucceeded: 0,
};

export function AnalyticsPage() {
  const {
    data: overview,
    loading: loadingOverview,
    error: overviewError,
    refresh: refreshOverview,
  } = useData<AnalyticsOverview>('/analytics/overview', EMPTY_OVERVIEW);
  const {
    data: usersData,
    loading: loadingUsers,
    error: usersError,
    refresh: refreshUsers,
  } = useData<{ users: UserStats[]; total: number }>('/analytics/users?limit=100', {
    users: [],
    total: 0,
  });

  const refreshAll = () => {
    void refreshOverview();
    void refreshUsers();
  };

  const sinceLabel = overview.trackingSince
    ? new Date(overview.trackingSince).toLocaleDateString()
    : 'initial migration';

  return (
    <>
      <PageHeading
        eyebrow="ADMINISTRATION"
        title="Processing analytics"
        description="Durable lifetime processing totals across the server and per account, preserved independently of file and history retention."
        action={
          <button className="secondary" type="button" onClick={refreshAll}>
            <RefreshCw size={15} /> Refresh
          </button>
        }
      />

      {overviewError && <ErrorState message={overviewError} retry={refreshOverview} />}
      {loadingOverview ? (
        <Loading />
      ) : (
        <>
          <section className="inline-callout" aria-label="Server processing summary">
            <BarChart3 size={18} />
            <span>
              <b>
                This server has processed {bytes(overview.processedInputBytes)} across{' '}
                {overview.succeededItems.toLocaleString()} completed item
                {overview.succeededItems === 1 ? '' : 's'}.
              </b>{' '}
              Tracked from {sinceLabel}
              {overview.backfilledJobs > 0
                ? ` (backfilled from ${overview.backfilledJobs.toLocaleString()} retained job${overview.backfilledJobs === 1 ? '' : 's'}; earlier purged history is unavailable)`
                : ''}
              . Counts completed processing items only; retries, failed attempts, and ZIP downloads are never double-counted.
            </span>
          </section>

          <div className="ops-grid" style={{ marginTop: 14 }}>
            <article className="ops-tile">
              <small>Processed input</small>
              <strong>{bytes(overview.processedInputBytes)}</strong>
              <span>Last 30 days: {bytes(overview.last30DaysInputBytes)}</span>
            </article>
            <article className="ops-tile">
              <small>Output generated</small>
              <strong>{bytes(overview.processedOutputBytes)}</strong>
              <span>
                {overview.savedBytes > 0
                  ? `Saved ${bytes(overview.savedBytes)} (${overview.savingsPercent.toFixed(1)}%)`
                  : 'No net size reduction yet'}
              </span>
            </article>
            <article className="ops-tile">
              <small>Completed items</small>
              <strong>{overview.succeededItems.toLocaleString()}</strong>
              <span>
                {overview.failedItems.toLocaleString()} failed · {overview.cancelledItems.toLocaleString()} canceled
              </span>
            </article>
            <article className="ops-tile">
              <small>Convert vs Compress</small>
              <strong>
                {overview.convertItems.toLocaleString()} / {overview.compressItems.toLocaleString()}
              </strong>
              <span>
                {overview.standaloneJobs.toLocaleString()} standalone · {overview.batchExecutions.toLocaleString()}{' '}
                batch{overview.batchExecutions === 1 ? '' : 'es'} ({overview.batchItems.toLocaleString()} items)
              </span>
            </article>
          </div>
        </>
      )}

      <div className="section-intro" style={{ marginTop: 22 }}>
        <div>
          <h2>Usage by account</h2>
          <p>
            Lifetime totals per user. Guest sessions are aggregated together, deleted accounts are anonymized, and no
            filenames or document contents are stored in analytics.
          </p>
        </div>
      </div>

      {usersError && <ErrorState message={usersError} retry={refreshUsers} />}
      {loadingUsers ? (
        <Loading />
      ) : !usersData.users.length ? (
        <Empty
          icon={BarChart3}
          title="No completed processing yet"
          detail="Lifetime counters update automatically as jobs reach a terminal state."
        />
      ) : (
        <div className="card-grid">
          {usersData.users.map(user => (
            <article className="converter-card" key={user.ownerId}>
              <div className="converter-card-top">
                <span className="converter-glyph">
                  <BarChart3 size={18} />
                </span>
                <span className="mini-badge">
                  {user.category === 'guest' ? 'Aggregated' : user.category === 'deleted' ? 'Anonymized' : 'Account'}
                </span>
              </div>
              <h3>{user.displayName}</h3>
              <p>
                <b>{bytes(user.processedInputBytes)}</b> processed → {bytes(user.processedOutputBytes)} output
                {user.savedBytes > 0 ? ` (saved ${bytes(user.savedBytes)} · ${user.savingsPercent.toFixed(1)}%)` : ''}
              </p>
              <div className="card-meta">
                <span>
                  {user.succeededItems.toLocaleString()} done · {user.failedItems.toLocaleString()} failed ·{' '}
                  {user.cancelledItems.toLocaleString()} canceled
                </span>
              </div>
              <div className="card-meta">
                <span>
                  Convert: {user.convertItems.toLocaleString()} · Compress: {user.compressItems.toLocaleString()}
                </span>
                <span>
                  {user.batchExecutions.toLocaleString()} batch{user.batchExecutions === 1 ? '' : 'es'}
                </span>
              </div>
            </article>
          ))}
        </div>
      )}

      <div className="inline-callout" style={{ marginTop: 16 }}>
        <ShieldCheck size={16} />
        <span>
          <b>Administrator only.</b> Processing analytics are enforced server-side and are never accessible to standard
          users or guests.
        </span>
      </div>
    </>
  );
}
