import { useEffect, useState } from 'react';
import { ChevronDown, HardDrive, RefreshCw } from 'lucide-react';
import { ErrorState, Loading, PageHeading } from '../../components/common';
import '../admin/admin.css';
import { bytes, explain, json, pretty } from '../format';
import { apiRequest } from '../session';
import { useData } from '../useData';

export type StorageAmount = {
  count: number;
  bytes: number;
};

export type RetentionRule = {
  mode: string;
  days?: number;
};

export type ScanCategory = {
  files: number;
  bytes: number;
  databaseFiles: number;
  databaseBytes: number;
  missingMetadata: number;
  unreferencedFiles: number;
  unreferencedBytes: number;
  missingSamples: string[];
  unreferencedSamples: string[];
  ageBuckets: Record<string, StorageAmount>;
};

export type StorageScan = {
  status: string;
  updatedAt: string;
  error?: string;
  categories: Record<string, ScanCategory>;
  owners: {
    ownerId: string;
    count: number;
    bytes: number;
    resultCount: number;
    resultBytes: number;
  }[];
};

export type StorageResponse = {
  overview: {
    databaseBytes: number;
    reservedInputBytes: number;
    originals: StorageAmount;
    results: StorageAmount;
    abandonedUploads: StorageAmount;
    failedOriginals: StorageAmount;
    canceledOriginals: StorageAmount;
    pendingCleanup: number;
    jobs: Record<string, number>;
    owners: { ownerId: string; count: number; bytes: number }[];
    capacity: { name: string; path: string; totalBytes?: number; freeBytes?: number; error?: string }[];
    workerTempLimitBytes: number;
    measurementDetails: Record<string, string>;
  };
  retention: {
    abandonedUploadDays?: number | null;
    unsubmittedUploads: RetentionRule;
    submittedOriginals: RetentionRule;
    successfulResults: RetentionRule;
    failedOriginals: RetentionRule;
    canceledOriginals: RetentionRule;
  };
  recentCleanup: {
    category: string;
    ownerId: string;
    fileId: string;
    reclaimedBytes: number;
    createdAt: string;
  }[];
  scan: StorageScan;
};

export type CleanupPreview = {
  report: { candidates: number; estimatedBytes: number };
  previewToken: string;
  expiresWithinMinutes: number;
  category?: string;
  days?: number;
};

const RETENTION_CATEGORIES = [
  { key: 'unsubmittedUploads', api: 'unsubmitted', label: 'Unsubmitted uploads' },
  { key: 'submittedOriginals', api: 'submitted_originals', label: 'Submitted originals' },
  { key: 'successfulResults', api: 'successful_results', label: 'Successful results' },
  { key: 'failedOriginals', api: 'failed_originals', label: 'Failed job originals' },
  { key: 'canceledOriginals', api: 'canceled_originals', label: 'Canceled job originals' },
] as const;

const EMPTY_STORAGE: StorageResponse = {
  overview: {
    databaseBytes: 0,
    reservedInputBytes: 0,
    originals: { count: 0, bytes: 0 },
    results: { count: 0, bytes: 0 },
    abandonedUploads: { count: 0, bytes: 0 },
    failedOriginals: { count: 0, bytes: 0 },
    canceledOriginals: { count: 0, bytes: 0 },
    pendingCleanup: 0,
    jobs: {},
    owners: [],
    capacity: [],
    workerTempLimitBytes: 1 << 30,
    measurementDetails: {},
  },
  retention: {
    abandonedUploadDays: null,
    unsubmittedUploads: { mode: 'keep' },
    submittedOriginals: { mode: 'keep' },
    successfulResults: { mode: 'keep' },
    failedOriginals: { mode: 'keep' },
    canceledOriginals: { mode: 'keep' },
  },
  recentCleanup: [],
  scan: { status: 'not_started', updatedAt: '', categories: {}, owners: [] },
};

export function StoragePage({ notify }: { notify: (message: string) => void }) {
  const { data, loading, error, refresh } = useData<StorageResponse>('/storage', EMPTY_STORAGE);
  const [rules, setRules] = useState<Record<string, RetentionRule>>({});
  const [days, setDays] = useState<Record<string, string>>({});
  const [preview, setPreview] = useState<CleanupPreview | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    const next: Record<string, RetentionRule> = {
      unsubmittedUploads: data.retention.unsubmittedUploads || {
        mode: data.retention.abandonedUploadDays ? 'after_days' : 'manual',
        days: data.retention.abandonedUploadDays || undefined,
      },
      submittedOriginals: data.retention.submittedOriginals || { mode: 'keep' },
      successfulResults: data.retention.successfulResults || { mode: 'keep' },
      failedOriginals: data.retention.failedOriginals || { mode: 'keep' },
      canceledOriginals: data.retention.canceledOriginals || { mode: 'keep' },
    };
    setRules(next);
    setDays(
      Object.fromEntries(Object.entries(next).map(([key, value]) => [key, String(value.days || 30)])),
    );
  }, [data.retention]);

  useEffect(() => {
    if (data.scan.status !== 'running') return;
    const timer = setInterval(() => void refresh(), 2500);
    return () => clearInterval(timer);
  }, [data.scan.status, refresh]);

  async function previewCleanup(category: string, dayCount: number) {
    if ((dayCount < 1 || dayCount > 3650) && !(category === 'successful_originals' && dayCount === 0)) {
      return;
    }
    setBusy(true);
    try {
      const response = await apiRequest<CleanupPreview>(
        `/storage/cleanup/preview?category=${category}&days=${dayCount}`,
      );
      setPreview({ ...response, category, days: dayCount });
    } catch (err) {
      notify(explain((err as Error).message));
    } finally {
      setBusy(false);
    }
  }

  async function saveRule(key: string, category: string) {
    const rule = rules[key] || { mode: 'keep' };
    const expectedCategory = rule.mode === 'after_success' ? 'successful_originals' : category;
    const expectedDays = rule.mode === 'after_success' ? 0 : Number(days[key] || 30);
    if (
      (rule.mode === 'after_days' || rule.mode === 'after_success') &&
      (!preview || preview.category !== expectedCategory || preview.days !== expectedDays)
    ) {
      notify('Preview this category before saving its deletion policy.');
      return;
    }
    setBusy(true);
    try {
      await apiRequest('/storage/retention', {
        method: 'PUT',
        body: json({
          [key]: rule.mode === 'after_days' ? { mode: rule.mode, days: expectedDays } : { mode: rule.mode },
          ...(preview ? { previewToken: preview.previewToken } : {}),
        }),
      });
      setPreview(null);
      await refresh();
      notify('Retention policy saved.');
    } catch (err) {
      notify(explain((err as Error).message));
    } finally {
      setBusy(false);
    }
  }

  async function apply() {
    if (
      !preview ||
      !confirm(
        `Delete up to 100 ${preview.category?.replaceAll('_', ' ')} files? Preview found ${preview.report.candidates} candidates (about ${bytes(preview.report.estimatedBytes)} recorded). This cannot be undone.`,
      )
    ) {
      return;
    }
    setBusy(true);
    try {
      const result = await apiRequest<{ report: { deleted: number; reclaimedBytes: number } }>(
        '/storage/cleanup',
        {
          method: 'POST',
          body: json({
            category: preview.category,
            days: preview.days,
            previewToken: preview.previewToken,
          }),
        },
      );
      notify(`Deleted ${result.report.deleted}; reclaimed ${bytes(result.report.reclaimedBytes)}.`);
      setPreview(null);
      await refresh();
    } catch (err) {
      notify(explain((err as Error).message));
    } finally {
      setBusy(false);
    }
  }

  async function startScan() {
    setBusy(true);
    try {
      await apiRequest('/storage/scan', { method: 'POST' });
      await refresh();
    } catch (err) {
      notify(explain((err as Error).message));
    } finally {
      setBusy(false);
    }
  }

  const overview = data.overview;
  const metricCards: [string, StorageAmount][] = [
    ['Original uploads', overview.originals],
    ['Results', overview.results],
    ['Abandoned uploads', overview.abandonedUploads],
    ['Failed job originals', overview.failedOriginals],
    ['Canceled job originals', overview.canceledOriginals],
  ];

  return (
    <>
      <PageHeading
        title="Storage"
        description="Recorded file usage, available capacity, and cleanup controls."
        action={
          <button className="secondary" onClick={() => void refresh()}>
            <RefreshCw size={15} /> Refresh
          </button>
        }
      />
      {error ? (
        <ErrorState message={error} retry={refresh} />
      ) : loading ? (
        <Loading />
      ) : (
        <div className="storage-view">
          <section className="admin-panel">
            <h2>
              <HardDrive size={18} /> Files and jobs
            </h2>
            <p className="muted-copy">
              File sizes below come from PostgreSQL records and are estimates. Capacity is measured inside the Core
              container.
            </p>
            <div className="storage-metrics">
              <article>
                <span>PostgreSQL database</span>
                <b>{bytes(overview.databaseBytes)}</b>
                <small>Measured by PostgreSQL</small>
              </article>
              <article>
                <span>Queued/running inputs</span>
                <b>{bytes(overview.reservedInputBytes)}</b>
                <small>Recorded input sizes; not output or scratch estimates</small>
              </article>
              {metricCards.map(([label, value]) => (
                <article key={label}>
                  <span>{label}</span>
                  <b>{bytes(value.bytes)}</b>
                  <small>
                    {value.count} file{value.count === 1 ? '' : 's'}
                  </small>
                </article>
              ))}
            </div>
            <p className="muted-copy">
              Jobs:{' '}
              {Object.entries(overview.jobs)
                .map(([state, count]) => `${state} ${count}`)
                .join(' · ') || 'none'}{' '}
              · Cleanup pending: {overview.pendingCleanup}. Worker temporary workspace: up to{' '}
              {bytes(overview.workerTempLimitBytes)}; live use is unavailable to Core. Queued/running inputs are shown
              separately as a minimum reserved amount, not a prediction of scratch or output size.
            </p>
          </section>

          <section className="admin-panel">
            <div className="panel-heading">
              <div>
                <h2>Filesystem inventory</h2>
                <p>
                  Read-only scan of configured uploads, results, and managed engines. Progress is saved and can resume
                  after a Core restart.
                </p>
              </div>
              <button
                className="secondary"
                disabled={busy || data.scan.status === 'running'}
                onClick={() => void startScan()}
              >
                {data.scan.status === 'running'
                  ? 'Scanning…'
                  : data.scan.status === 'incomplete' || data.scan.status === 'interrupted'
                    ? 'Resume scan'
                    : data.scan.status === 'complete'
                      ? 'Scan again'
                      : 'Start scan'}
              </button>
            </div>
            <p className="muted-copy">
              Status: {pretty(data.scan.status)}
              {data.scan.updatedAt ? ' · Updated ' + new Date(data.scan.updatedAt).toLocaleString() : ''}
              {data.scan.error ? ' · ' + data.scan.error : ''}. This scan never deletes files. Worker temporary usage
              remains unavailable to Core.
            </p>
            {Object.keys(data.scan.categories || {}).length > 0 && (
              <div className="storage-list">
                {(
                  [
                    ['originals', 'Original uploads'],
                    ['results', 'Results'],
                    ['engines', 'Engine packages'],
                  ] as const
                ).map(([key, label]) => {
                  const item = data.scan.categories[key];
                  if (!item) return null;
                  return (
                    <div key={key}>
                      <b>{label}</b>
                      <span>
                        {item.files} files · {bytes(item.bytes)} measured
                      </span>
                      {key !== 'engines' && (
                        <small>
                          Database: {item.databaseFiles} files · {bytes(item.databaseBytes)} recorded ·{' '}
                          {item.missingMetadata} referenced files missing · {item.unreferencedFiles} unreferenced files
                          ({bytes(item.unreferencedBytes)})
                        </small>
                      )}
                      <small>
                        Modified age:{' '}
                        {Object.entries(item.ageBuckets || {})
                          .map(([age, value]) => `${age}: ${value.count} · ${bytes(value.bytes)}`)
                          .join(' · ') || 'no files'}
                        .
                      </small>
                    </div>
                  );
                })}
              </div>
            )}
            {data.scan.status === 'complete' && (
              <details className="scan-owners">
                <summary>Inspect file discrepancies</summary>
                <div className="storage-list">
                  {(
                    [
                      ['originals', 'Original uploads'],
                      ['results', 'Results'],
                    ] as const
                  ).map(([key, label]) => {
                    const item = data.scan.categories[key];
                    if (!item) return null;
                    return (
                      <div key={key}>
                        <b>{label}</b>
                        <span>
                          {item.missingSamples?.length || 0} missing examples ·{' '}
                          {item.unreferencedSamples?.length || 0} unreferenced examples
                        </span>
                        {(item.missingSamples || []).map(path => (
                          <small key={'m' + path}>Missing: {path}</small>
                        ))}
                        {(item.unreferencedSamples || []).map(path => (
                          <small key={'u' + path}>Unreferenced: {path}</small>
                        ))}
                      </div>
                    );
                  })}
                </div>
                <p className="muted-copy">
                  Examples are relative paths inside managed storage. Review files before using manual cleanup;
                  reconciliation never removes them.
                </p>
              </details>
            )}
            {(data.scan.owners || []).length > 0 && (
              <details className="scan-owners">
                <summary>Measured originals and results by user</summary>
                <div className="storage-list">
                  {(data.scan.owners || []).map(owner => (
                    <div key={owner.ownerId}>
                      <b>{owner.ownerId}</b>
                      <span>
                        Originals {bytes(owner.bytes)} · Results {bytes(owner.resultBytes)}
                      </span>
                    </div>
                  ))}
                </div>
              </details>
            )}
          </section>

          <section className="admin-panel">
            <h2>Available capacity</h2>
            <div className="storage-list">
              {overview.capacity.map(item => (
                <div key={item.name}>
                  <b>{item.name}</b>
                  <span>
                    {item.error || `${bytes(item.freeBytes || 0)} available of ${bytes(item.totalBytes || 0)}`}
                  </span>
                  <small>{item.path} · container path</small>
                </div>
              ))}
            </div>
            <p className="muted-copy">
              These mounts can share the same host filesystem; their available space must not be added together.
              Installed engine size and database disk use are not measured here.
            </p>
          </section>

          <section className="admin-panel">
            <h2>File retention</h2>
            <p className="muted-copy">
              Retention changes can permanently delete files. Defaults keep data. Preview a category before enabling
              timed deletion; cleanup is limited to 100 files per run. Job history remains after file expiry.
            </p>
            <div className="retention-policy-list">
              {RETENTION_CATEGORIES.map(item => {
                const rule = rules[item.key] || { mode: 'keep' };
                const previewCategory = rule.mode === 'after_success' ? 'successful_originals' : item.api;
                const count = Number(days[item.key] || 30);
                const expectedDays = rule.mode === 'after_success' ? 0 : count;
                return (
                  <div className="retention-policy-row" key={item.key}>
                    <div>
                      <b>{item.label}</b>
                      <small>
                        {item.key === 'submittedOriginals'
                          ? 'After success requires a registered output verified on disk.'
                          : 'Expired files remain marked in history and are no longer downloadable.'}
                      </small>
                    </div>
                    <div className="retention-controls">
                      <div className="select-wrap">
                        <select
                          aria-label={item.label + ' policy'}
                          value={rule.mode}
                          onChange={event => {
                            setRules(current => ({
                              ...current,
                              [item.key]: {
                                mode: event.target.value,
                                ...(event.target.value === 'after_days' ? { days: count } : {}),
                              },
                            }));
                            setPreview(null);
                          }}
                        >
                          <option value="keep">Keep indefinitely</option>
                          <option value="manual">Manual cleanup</option>
                          {item.key === 'submittedOriginals' && (
                            <option value="after_success">Delete after verified success</option>
                          )}
                          <option value="after_days">Keep for a number of days</option>
                        </select>
                        <ChevronDown size={15} />
                      </div>
                      {rule.mode === 'after_days' && (
                        <input
                          aria-label={item.label + ' days'}
                          className="compression-number"
                          type="number"
                          min="1"
                          max="3650"
                          value={days[item.key] || '30'}
                          onChange={event => {
                            setDays(current => ({ ...current, [item.key]: event.target.value }));
                            setRules(current => ({
                              ...current,
                              [item.key]: { mode: 'after_days', days: Number(event.target.value) },
                            }));
                            setPreview(null);
                          }}
                        />
                      )}
                      <button
                        className="secondary small"
                        disabled={busy}
                        onClick={() => void previewCleanup(previewCategory, expectedDays)}
                      >
                        Preview
                      </button>
                      <button
                        className="secondary small"
                        disabled={
                          busy ||
                          ((rule.mode === 'after_days' || rule.mode === 'after_success') &&
                            (!preview ||
                              preview.category !== previewCategory ||
                              preview.days !== expectedDays))
                        }
                        onClick={() => void saveRule(item.key, item.api)}
                      >
                        Save
                      </button>
                      {preview?.category === previewCategory && preview.days === expectedDays && (
                        <button
                          className="primary small"
                          disabled={busy || !preview.report.candidates}
                          onClick={() => void apply()}
                        >
                          Clean up
                        </button>
                      )}
                    </div>
                  </div>
                );
              })}
            </div>
            {preview && (
              <p role="status">
                Preview: {preview.report.candidates} candidates, about {bytes(preview.report.estimatedBytes)} based on
                recorded sizes. Preview expires in 10 minutes.
              </p>
            )}
          </section>

          <section className="admin-panel">
            <h2>Recent cleanup</h2>
            {data.recentCleanup.length ? (
              <div className="storage-list">
                {data.recentCleanup.map(event => (
                  <div key={`${event.fileId}:${event.createdAt}`}>
                    <b>{event.category.replaceAll('_', ' ')}</b>
                    <span>{bytes(event.reclaimedBytes)} reclaimed</span>
                    <small>
                      {new Date(event.createdAt).toLocaleString()} · owner {event.ownerId}
                    </small>
                  </div>
                ))}
              </div>
            ) : (
              <p className="muted-copy">No cleanup events yet.</p>
            )}
          </section>

          {overview.owners.length > 0 && (
            <section className="admin-panel">
              <h2>Originals by owner</h2>
              <div className="storage-list">
                {overview.owners.map(owner => (
                  <div key={owner.ownerId}>
                    <b>{owner.ownerId}</b>
                    <span>{bytes(owner.bytes)}</span>
                    <small>
                      {owner.count} uploaded file{owner.count === 1 ? '' : 's'}
                    </small>
                  </div>
                ))}
              </div>
            </section>
          )}
        </div>
      )}
    </>
  );
}
