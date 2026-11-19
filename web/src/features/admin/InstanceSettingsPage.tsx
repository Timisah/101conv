import { useEffect, useState, type FormEvent } from 'react';
import { NavLink } from 'react-router-dom';
import {
  Activity,
  ArrowRight,
  Boxes,
  ChevronDown,
  CircleAlert,
  Clock3,
  ExternalLink,
  LockKeyhole,
  Plus,
  Power,
  RefreshCw,
  Settings2,
  ShieldCheck,
  Trash2,
  Upload,
  X,
} from 'lucide-react';
import { ErrorState, Loading, PageHeading, StatePill } from '../../components/common';
import type { Repo } from '../catalog/types';
import { explain, json } from '../format';
import { apiRequest } from '../session';
import { useData } from '../useData';
import './admin.css';
import { AnonymousAccessForm, type AnonymousSettings } from './AnonymousAccessForm';

export type UpdateStatus = {
  currentVersion: string;
  latestVersion?: string;
  updateAvailable: boolean;
  releaseName?: string;
  releaseUrl?: string;
  releaseNotes?: string;
  publishedAt?: string;
  checkedAt?: string;
  status: 'current' | 'update_available' | 'unavailable';
  error?: string;
};

type HealthSummary = {
  health: { status?: string } | null;
  ready: { status?: string; version?: string } | null;
};

const DEFAULT_GUEST_SETTINGS: AnonymousSettings = {
  enabled: false,
  allowConversion: true,
  allowCompression: true,
  maxFileBytes: 10 * 1024 * 1024,
  maxBatchFiles: 5,
  maxSimultaneousJobs: 5,
  uploadsPerMinute: 10,
  submissionsPerMinute: 5,
  globalUploadsPerMinute: 120,
  globalSubmissionsPerMinute: 60,
  maxGlobalJobs: 32,
  maxGlobalStorageBytes: 5 * 1024 * 1024 * 1024,
  resultExpirationHours: 24,
  sessionExpirationHours: 24,
  uploadExpirationHours: 24,
  failedExpirationHours: 24,
  metadataExpirationHours: 168,
};

const EMPTY_REPO_FORM = {
  id: '',
  name: '',
  url: '',
  kind: 'community',
  trustedRoot: '',
  fingerprint: '',
};

export function InstanceSettingsPage({
  notify,
  update,
  onRefreshUpdate,
}: {
  notify: (message: string) => void;
  update: UpdateStatus | null;
  onRefreshUpdate: () => Promise<void>;
}) {
  const { data: reposResp, loading, error, refresh } = useData<{ repositories: Repo[] }>('/repositories', {
    repositories: [],
  });
  const [health, setHealth] = useState<HealthSummary | null>(null);
  const [modal, setModal] = useState(false);
  const [form, setForm] = useState(EMPTY_REPO_FORM);

  const {
    data: sessionSettings,
    loading: sessionSettingsLoading,
    refresh: refreshSessionSettings,
  } = useData<{ sessionTimeoutMinutes: number }>('/settings/session', { sessionTimeoutMinutes: 720 });
  const [timeoutHours, setTimeoutHours] = useState(12);
  const [savingTimeout, setSavingTimeout] = useState(false);

  const {
    data: guestSettings,
    setData: setGuestSettings,
    loading: guestSettingsLoading,
  } = useData<AnonymousSettings>('/settings/anonymous', DEFAULT_GUEST_SETTINGS);
  const [savingGuest, setSavingGuest] = useState(false);

  useEffect(() => {
    if (sessionSettings.sessionTimeoutMinutes) {
      setTimeoutHours(Math.max(1, Math.round(sessionSettings.sessionTimeoutMinutes / 60)));
    }
  }, [sessionSettings.sessionTimeoutMinutes]);

  useEffect(() => {
    Promise.all([
      fetch('/healthz').then(response => response.json()),
      fetch('/readyz').then(response => response.json()),
    ])
      .then(([healthData, readyData]) => setHealth({ health: healthData, ready: readyData }))
      .catch(() => setHealth(null));
  }, []);

  async function saveSessionTimeout(event: FormEvent) {
    event.preventDefault();
    setSavingTimeout(true);
    try {
      await apiRequest<{ sessionTimeoutMinutes: number }>('/settings/session', {
        method: 'PUT',
        body: json({ sessionTimeoutMinutes: timeoutHours * 60 }),
      });
      notify('Global session timeout saved. It applies to new sign-ins.');
      await refreshSessionSettings();
    } catch (err) {
      notify(explain((err as Error).message));
    } finally {
      setSavingTimeout(false);
    }
  }

  async function saveGuestSettings(event: FormEvent) {
    event.preventDefault();
    setSavingGuest(true);
    try {
      const saved = await apiRequest<AnonymousSettings>('/settings/anonymous', {
        method: 'PUT',
        body: json(guestSettings),
      });
      setGuestSettings(saved);
      notify(
        saved.enabled
          ? 'Guest conversions are enabled. Visitors can continue from the home page.'
          : 'Guest conversions are disabled. Visitors will need to log in.',
      );
    } catch (err) {
      notify(explain((err as Error).message));
    } finally {
      setSavingGuest(false);
    }
  }

  async function repoAction(repo: Repo, action: 'enabled' | 'refresh' | 'remove') {
    try {
      if (action === 'remove') {
        if (!confirm('Remove repository ' + repo.name + '? Installed packages will remain installed.')) return;
        await apiRequest('/repositories/' + repo.id, { method: 'DELETE' });
      } else if (action === 'refresh') {
        await apiRequest('/repositories/' + repo.id + '/refresh', { method: 'POST' });
      } else {
        await apiRequest('/repositories/' + repo.id + '/enabled', {
          method: 'PATCH',
          body: json({ enabled: !repo.enabled }),
        });
      }
      notify(
        action === 'enabled'
          ? repo.enabled
            ? 'Repository disabled'
            : 'Repository enabled'
          : action === 'refresh'
            ? 'Repository metadata refreshed'
            : 'Repository removed.',
      );
      await refresh();
    } catch (err) {
      notify(explain((err as Error).message));
    }
  }

  async function trustOfficial(approved: boolean) {
    if (
      approved &&
      !confirm(
        'Enable installs from the built-in GitHub catalog without signed metadata? Package hashes are checked, but the publisher cannot be authenticated.',
      )
    ) {
      return;
    }
    try {
      await apiRequest('/repositories/101conv-official/trust', {
        method: 'PATCH',
        body: json({ approved }),
      });
      notify(approved ? 'Unsigned GitHub installs enabled.' : 'Unsigned GitHub install approval revoked.');
      await refresh();
    } catch (err) {
      notify(explain((err as Error).message));
    }
  }

  async function addRepository(event: FormEvent) {
    event.preventDefault();
    try {
      await apiRequest('/repositories', {
        method: 'POST',
        body: json({ ...form, approved: true }),
      });
      notify('Trusted repository added. Refresh its metadata to browse packages.');
      setModal(false);
      setForm(EMPTY_REPO_FORM);
      await refresh();
    } catch (err) {
      notify(explain((err as Error).message));
    }
  }

  return (
    <>
      <PageHeading
        eyebrow="ADMINISTRATION"
        title="Instance settings"
        description="Updates, repository trust, sign-in and guest access."
        action={
          <button className="secondary" onClick={() => void refresh()}>
            <RefreshCw size={15} /> Refresh
          </button>
        }
      />

      <div className="admin-grid">
        <section className="admin-panel">
          <div className="panel-heading">
            <div>
              <h2>
                <Activity size={18} /> System health
              </h2>
            </div>
            <StatePill state={health?.ready?.status || 'unknown'} />
          </div>
          <div className="health-row">
            <span>
              <i className={health?.health ? 'green-dot' : 'gray-dot'} /> Core API
            </span>
            <b>{health?.health?.status || 'Checking…'}</b>
          </div>
          <div className="health-row">
            <span>
              <i className={health?.ready ? 'green-dot' : 'gray-dot'} /> Readiness
            </span>
            <b>{health?.ready?.status || 'Checking…'}</b>
          </div>
          <div className="health-row">
            <span>
              <i className="gray-dot" /> Worker, converters and storage
            </span>
            <NavLink className="text-link" to="/operations">
              Open Operations <ArrowRight size={13} />
            </NavLink>
          </div>
          <div className="panel-foot">Version {health?.ready?.version || '—'}</div>
        </section>

        <section className="admin-panel update-panel" aria-live="polite">
          <div className="panel-heading">
            <div>
              <h2>
                <RefreshCw size={18} /> Application updates
              </h2>
              <p>The app never upgrades itself.</p>
            </div>
            <button className="secondary small" onClick={() => void onRefreshUpdate()}>
              <RefreshCw size={14} /> Check now
            </button>
          </div>
          <div className="health-row">
            <span>Current version</span>
            <b>{update?.currentVersion || health?.ready?.version || '—'}</b>
          </div>
          {update?.status === 'update_available' ? (
            <div className="update-callout" role="status">
              <div>
                <b>Version {update.latestVersion} is available</b>
                <span>{update.releaseName || 'A new stable release is available.'}</span>
              </div>
              {update.releaseUrl && (
                <a className="text-link" href={update.releaseUrl} target="_blank" rel="noreferrer">
                  View release <ExternalLink size={14} />
                </a>
              )}
              {update.releaseNotes && (
                <details>
                  <summary>Release notes</summary>
                  <p>{update.releaseNotes}</p>
                </details>
              )}
            </div>
          ) : update?.status === 'current' ? (
            <p className="muted-copy">
              Up to date at {update.latestVersion || update.currentVersion}. Checked{' '}
              {update.checkedAt ? new Date(update.checkedAt).toLocaleString() : 'just now'}.
            </p>
          ) : (
            <p className="muted-copy">
              {update?.error || 'Release check unavailable. Try again when the instance can reach the release feed.'}
            </p>
          )}
        </section>

        <section className="admin-panel trust-panel">
          <div className="panel-heading">
            <div>
              <h2>
                <ShieldCheck size={18} /> Repository trust
              </h2>
              <p>Which catalogs can install converters.</p>
            </div>
            <button className="primary small" onClick={() => setModal(true)}>
              <Plus size={14} /> Add trusted source
            </button>
          </div>
          {error && <ErrorState message={error} retry={refresh} />}
          {loading
            ? <Loading />
            : reposResp.repositories?.map(repo => (
                <article className="repo-card" key={repo.id}>
                  <div className="repo-icon">
                    <Boxes size={17} />
                  </div>
                  <div className="repo-main">
                    <div className="repo-title">
                      <b>{repo.name}</b>
                      <span className={'trust-label ' + (repo.trust === 'pinned' ? 'good' : 'muted')}>
                        {repo.trust === 'pinned' ? <ShieldCheck size={12} /> : <CircleAlert size={12} />}{' '}
                        {repo.trust === 'pinned'
                          ? 'Verified'
                          : repo.trust === 'approved_unsigned'
                            ? 'Admin-approved · unsigned'
                            : repo.trust === 'unavailable'
                              ? 'Not approved'
                              : repo.trust}
                      </span>
                    </div>
                    <small>
                      {repo.url || 'Bundled repository'} · {repo.kind}
                    </small>
                    <div className="repo-state">
                      {[
                        repo.lastRefresh ? 'Last refreshed ' + new Date(repo.lastRefresh).toLocaleString() : '',
                        repo.lastError,
                      ]
                        .filter(Boolean)
                        .join(' · ')}
                    </div>
                  </div>
                  <div className="repo-actions">
                    {repo.id === '101conv-official' ? (
                      <>
                        <button
                          className="icon-btn"
                          title="Refresh catalog metadata"
                          onClick={() => void repoAction(repo, 'refresh')}
                        >
                          <RefreshCw size={16} />
                        </button>
                        {repo.trust === 'pinned' ? (
                          <StatePill state="verified" />
                        ) : (
                          <button
                            className={repo.trust === 'approved_unsigned' ? 'secondary small' : 'primary small'}
                            onClick={() => void trustOfficial(repo.trust !== 'approved_unsigned')}
                          >
                            {repo.trust === 'approved_unsigned' ? 'Revoke trust' : 'Trust repository'}
                          </button>
                        )}
                      </>
                    ) : (
                      <>
                        <button
                          className="icon-btn"
                          title="Refresh metadata"
                          onClick={() => void repoAction(repo, 'refresh')}
                        >
                          <RefreshCw size={16} />
                        </button>
                        <button
                          className="icon-btn"
                          title={repo.enabled ? 'Disable repository' : 'Enable repository'}
                          onClick={() => void repoAction(repo, 'enabled')}
                        >
                          <Power size={16} />
                        </button>
                        <button
                          className="icon-btn danger-btn"
                          title="Remove repository"
                          onClick={() => void repoAction(repo, 'remove')}
                        >
                          <Trash2 size={16} />
                        </button>
                        <StatePill state={repo.enabled ? 'enabled' : 'disabled'} />
                      </>
                    )}
                  </div>
                </article>
              ))}
          <div className="trust-explainer">
            <LockKeyhole size={16} />
            <div>
              <b>Unsigned approval does not verify the publisher.</b>
              <span>
                The built-in source uses HTTPS and checks each downloaded package against the SHA-256 in its GitHub
                manifest. An attacker able to change both can replace them. Revoke approval here any time.
              </span>
            </div>
          </div>
        </section>
      </div>

      <section className="admin-panel">
        <div className="panel-heading">
          <div>
            <h2>
              <Clock3 size={18} /> Authentication session lifetime
            </h2>
          </div>
        </div>
        {sessionSettingsLoading ? (
          <Loading />
        ) : (
          <form className="session-timeout-form" onSubmit={saveSessionTimeout}>
            <label className="admin-field">
              <span>Session lifetime</span>
              <span className="admin-input">
                <input
                  type="number"
                  min={1}
                  max={720}
                  step={1}
                  required
                  value={timeoutHours}
                  onChange={event => setTimeoutHours(Number(event.target.value))}
                />
                <em>hours</em>
              </span>
              <small>1 hour to 30 days. Applies to new sign-ins only.</small>
            </label>
            <button className="primary" disabled={savingTimeout || timeoutHours < 1 || timeoutHours > 720}>
              {savingTimeout ? 'Saving…' : 'Save session timeout'}
            </button>
          </form>
        )}
      </section>

      <section className="admin-panel guest-settings">
        <div className="panel-heading">
          <div>
            <h2>
              <Upload size={18} /> Anonymous access
            </h2>
          </div>
          <label className="guest-toggle">
            <input
              type="checkbox"
              checked={guestSettings.enabled}
              onChange={event => setGuestSettings({ ...guestSettings, enabled: event.target.checked })}
            />{' '}
            Enabled
          </label>
        </div>
        {guestSettingsLoading ? (
          <Loading />
        ) : (
          <AnonymousAccessForm
            settings={guestSettings}
            onChange={setGuestSettings}
            onSubmit={saveGuestSettings}
            saving={savingGuest}
          />
        )}
      </section>

      <section className="admin-panel limitations">
        <h2>
          <Settings2 size={18} /> Administrator API
        </h2>
        <p>
          <code>ENGINE_ADMIN_TOKEN</code> stays on the server. For external services, create a regular account with a
          scoped API key.
        </p>
        <NavLink to="/converters" className="text-link">
          Manage installed converters <ArrowRight size={14} />
        </NavLink>
        <NavLink to="/profile-defaults" className="text-link">
          Set profile defaults <ArrowRight size={14} />
        </NavLink>
      </section>

      {modal && (
        <div
          className="modal-scrim"
          role="presentation"
          onMouseDown={event => {
            if (event.target === event.currentTarget) setModal(false);
          }}
        >
          <form className="modal" onSubmit={addRepository}>
            <div className="modal-top">
              <div>
                <p className="eyebrow">TRUSTED SOURCE</p>
                <h2>Add a repository</h2>
              </div>
              <button type="button" className="icon-btn" onClick={() => setModal(false)} aria-label="Close">
                <X size={18} />
              </button>
            </div>
            <div className="warning-box">
              <CircleAlert size={17} />
              <span>Verify the TUF root fingerprint using an independent trusted channel before pinning it.</span>
            </div>
            {(['id', 'name', 'url'] as const).map(key => (
              <label className="field-label" key={key}>
                {key === 'id' ? 'Repository ID' : key === 'name' ? 'Display name' : 'Repository URL'}
                <input
                  required
                  value={form[key]}
                  onChange={event => setForm({ ...form, [key]: event.target.value })}
                  placeholder={key === 'url' ? 'https://example.com/repository' : ''}
                />
              </label>
            ))}
            <label className="field-label">
              Repository type
              <div className="select-wrap">
                <select value={form.kind} onChange={event => setForm({ ...form, kind: event.target.value })}>
                  <option value="community">Community</option>
                  <option value="local">Local</option>
                </select>
                <ChevronDown size={15} />
              </div>
            </label>
            <label className="field-label">
              TUF trusted root (base64)
              <textarea
                required
                rows={4}
                value={form.trustedRoot}
                onChange={event => setForm({ ...form, trustedRoot: event.target.value })}
              />
            </label>
            <label className="field-label">
              SHA-256 fingerprint
              <input
                required
                value={form.fingerprint}
                onChange={event => setForm({ ...form, fingerprint: event.target.value })}
                placeholder="Verify independently before entering"
              />
            </label>
            <div className="modal-actions">
              <button className="secondary" type="button" onClick={() => setModal(false)}>
                Cancel
              </button>
              <button className="primary" type="submit">
                <ShieldCheck size={15} /> Pin repository root
              </button>
            </div>
          </form>
        </div>
      )}
    </>
  );
}
