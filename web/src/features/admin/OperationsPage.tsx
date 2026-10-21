import { useCallback, useEffect, useState } from 'react';
import { NavLink } from 'react-router-dom';
import { ArrowRight, CircleCheck, RefreshCw, TriangleAlert } from 'lucide-react';
import { ErrorState, Loading, PageHeading, StatusPill, type Tone } from '../../components/common';
import { bytes, explain, pretty } from '../format';
import { apiRequest } from '../session';
import './admin.css';

type WorkerAcceleration = { schemaVersion: number; hardwareConfigured: boolean; cpuFallback: boolean; accelerators: { id: string; vendor: string; api: string; device?: string; driver?: string; available: boolean; encode: boolean; decode: boolean; hdrPreserving: boolean; verification: string; reason: string }[] };
export type OperationsResponse = {
  core: string; readyToConvert: boolean; databaseConnected: boolean;
  workers: { id: string; lastSeen: string; lastActivity?: string; online: boolean; activeJobs: number; configuredConcurrency: number; batchConcurrency: number; acceleration?: WorkerAcceleration }[];
  jobs: Record<string, number>; expiredLeases: number;
  storage: { name: string; writable: boolean; issue?: string }[];
  resultFreeBytes: number; installedEngines: number; installedProfiles: number; usableProfiles: number;
  repositories: { id: string; name: string; status: string; trust: string; enabled: boolean; lastRefresh?: string; lastError?: string }[];
  recentCleanup: { category: string; createdAt: string }[]; issues: string[]; checkedAt: string;
};

const empty: OperationsResponse = { core: 'unknown', readyToConvert: false, databaseConnected: false, workers: [], jobs: {}, expiredLeases: 0, storage: [], resultFreeBytes: 0, installedEngines: 0, installedProfiles: 0, usableProfiles: 0, repositories: [], recentCleanup: [], issues: [], checkedAt: '' };
const JOB_STATES = ['queued', 'running', 'succeeded', 'failed', 'cancelled'];

// Where an administrator can act on a reported issue. Matching is on the
// wording Core reports; issues without a known fix simply have no link.
function remedy(issue: string): { to: string; label: string } | undefined {
  if (/profile|engine/i.test(issue)) return { to: '/marketplace', label: 'Browse the repository catalog' };
  if (/storage|writable|mount/i.test(issue)) return { to: '/storage', label: 'Open storage' };
  return undefined;
}

function Tile({ label, tone, value, hint, to, action }: { label: string; tone: Tone; value: string; hint?: string; to?: string; action?: string }) {
  return (
    <div className="ops-tile">
      <span className="ops-tile-label">{label}</span>
      <StatusPill tone={tone}>{value}</StatusPill>
      {hint && <small>{hint}</small>}
      {to && action && <NavLink className="text-link" to={to}>{action} <ArrowRight size={13} /></NavLink>}
    </div>
  );
}

export function OperationsPage() {
  const [data, setData] = useState<OperationsResponse>(empty);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const load = useCallback(async (spinner: boolean) => {
    if (spinner) setLoading(true);
    try { setData(await apiRequest<OperationsResponse>('/operations')); setError(''); }
    catch (e) { setError(explain((e as Error).message)); }
    finally { setLoading(false); }
  }, []);
  // Health changes while an administrator is fixing something; keep it current.
  useEffect(() => {
    void load(true);
    const timer = setInterval(() => { if (!document.hidden) void load(false); }, 15000);
    return () => clearInterval(timer);
  }, [load]);

  const online = data.workers.filter(worker => worker.online).length;
  const storageOk = data.storage.length > 0 && data.storage.every(item => item.writable);
  const totalJobs = Object.values(data.jobs).reduce((sum, count) => sum + count, 0);
  return (
    <>
      <PageHeading title="Operations" description="Is this instance healthy, and what needs attention?" action={<button className="secondary" onClick={() => void load(false)}><RefreshCw size={15} /> Refresh</button>} />
      {error ? <ErrorState message={error} retry={() => void load(true)} /> : loading ? <Loading /> : (
        <div className="ops-view">
          <section className={'ops-hero ' + (data.readyToConvert ? 'ok' : 'attention')} aria-live="polite">
            <span className="ops-hero-icon">{data.readyToConvert ? <CircleCheck size={24} /> : <TriangleAlert size={24} />}</span>
            <div>
              <h2>{data.readyToConvert ? 'Ready to convert' : 'Not ready to convert yet'}</h2>
              <p>{data.readyToConvert ? 'Core, the database, a Worker and at least one converter are all available.' : `${data.issues.length || 'Some'} ${data.issues.length === 1 ? 'thing needs' : 'things need'} attention before files can be converted.`}{data.checkedAt ? ` Checked ${new Date(data.checkedAt).toLocaleTimeString()}.` : ''}</p>
            </div>
          </section>
          <div className="ops-tiles">
            <Tile label="Core" tone={data.core === 'running' ? 'ok' : 'bad'} value={pretty(data.core)} />
            <Tile label="Database" tone={data.databaseConnected ? 'ok' : 'bad'} value={data.databaseConnected ? 'Connected' : 'Unavailable'} />
            <Tile label="Worker" tone={online > 0 ? 'ok' : 'bad'} value={online > 0 ? `${online} online` : 'Not connected'} hint={online > 0 ? undefined : 'No heartbeat in the last 30 seconds'} />
            <Tile label="Converters" tone={data.usableProfiles > 0 ? 'ok' : 'warn'} value={`${data.usableProfiles} usable`} hint={`${data.installedEngines} engines · ${data.installedProfiles} profiles installed`} to={data.usableProfiles > 0 ? undefined : '/marketplace'} action="Install a converter" />
            <Tile label="Storage" tone={storageOk ? 'ok' : 'bad'} value={storageOk ? 'Writable' : 'Needs attention'} hint={`${bytes(data.resultFreeBytes)} free for results`} to="/storage" action="Storage controls" />
          </div>
          {data.issues.length > 0 && (
            <section className="admin-panel ops-issues">
              <h2><TriangleAlert size={18} /> To fix</h2>
              <ul>{data.issues.map(issue => { const fix = remedy(issue); return <li key={issue}><span>{issue}</span>{fix && <NavLink className="text-link" to={fix.to}>{fix.label} <ArrowRight size={13} /></NavLink>}</li>; })}</ul>
            </section>
          )}
          <section className="admin-panel">
            <h2>Jobs</h2>
            {totalJobs === 0 ? <p className="muted-copy">No jobs have run yet.</p> : (
              <div className="ops-counts">{JOB_STATES.map(state => <div key={state}><b>{data.jobs[state] || 0}</b><span>{pretty(state)}</span></div>)}</div>
            )}
            {data.expiredLeases > 0 && <p className="ops-warning"><TriangleAlert size={15} /> {data.expiredLeases} expired lease{data.expiredLeases === 1 ? '' : 's'}: a Worker stopped mid-job. The job is retried automatically; check the Worker logs if this keeps growing.</p>}
          </section>
          <section className="admin-panel">
            <h2>Workers</h2>
            {data.workers.length ? (
              <div className="storage-list">{data.workers.map(worker => (
                <div key={worker.id}>
                  <b>Worker {worker.id.slice(0, 8)}</b>
                  <span><StatusPill tone={worker.online ? 'ok' : 'bad'}>{worker.online ? 'Online' : 'Offline'}</StatusPill></span>
                  <small>{worker.activeJobs} active job{worker.activeJobs === 1 ? '' : 's'} of {worker.configuredConcurrency} slot{worker.configuredConcurrency === 1 ? '' : 's'} · at most {worker.batchConcurrency} from one batch</small>
                  <small>Last heartbeat {new Date(worker.lastSeen).toLocaleString()}{worker.lastActivity ? ' · last activity ' + new Date(worker.lastActivity).toLocaleString() : ''}</small>
                  {worker.acceleration && <small>Acceleration: {worker.acceleration.accelerators.filter(item => item.available && item.vendor !== 'cpu').map(item => item.id).join(', ') || 'CPU only'}{worker.acceleration.hardwareConfigured ? ' · allowlisted hardware checked' : ' · no hardware allowlist configured'}</small>}
                </div>
              ))}</div>
            ) : <p className="muted-copy">No Worker has reported yet. Check the Worker container logs and its database connection.</p>}
          </section>
          <section className="admin-panel">
            <h2>Storage locations</h2>
            <div className="storage-list">{data.storage.map(item => (
              <div key={item.name}><b>{item.name}</b><span><StatusPill tone={item.writable ? 'ok' : 'bad'}>{item.writable ? 'Writable' : 'Not writable'}</StatusPill></span>{item.issue && <small>{item.issue}</small>}</div>
            ))}</div>
            <NavLink className="text-link" to="/storage">Open storage controls <ArrowRight size={14} /></NavLink>
          </section>
          <section className="admin-panel">
            <h2>Repositories</h2>
            {data.repositories.length ? (
              <div className="storage-list">{data.repositories.map(repo => (
                <div key={repo.id}>
                  <b>{repo.name}</b>
                  <span><StatusPill tone={!repo.enabled ? 'muted' : repo.trust === 'trusted' ? 'ok' : 'warn'}>{!repo.enabled ? 'Disabled' : repo.trust === 'trusted' ? 'Trusted' : 'Not approved'}</StatusPill></span>
                  {repo.lastError && <small>{repo.lastError}</small>}
                  <small>{pretty(repo.status)}{repo.lastRefresh ? ' · last refresh ' + new Date(repo.lastRefresh).toLocaleString() : ''}</small>
                </div>
              ))}</div>
            ) : <p className="muted-copy">No repositories configured.</p>}
            <NavLink className="text-link" to="/admin">Repository trust and settings <ArrowRight size={14} /></NavLink>
          </section>
        </div>
      )}
    </>
  );
}
