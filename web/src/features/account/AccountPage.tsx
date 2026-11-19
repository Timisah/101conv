import { useState, type FormEvent } from 'react';
import { Activity, Copy, LockKeyhole, Plus, ShieldCheck, Trash2, X } from 'lucide-react';
import { AppearanceSettings } from '../../components/ThemePicker';
import { Loading, PageHeading } from '../../components/common';
import '../admin/admin.css';
import { maxFileQuotaText, quotaText, type AccessUser } from '../admin/quotas';
import { ProfilePreferences } from '../converters/ProfilePreferences';
import { bytes, explain, json } from '../format';
import { apiRequest, type Session } from '../session';
import { useData } from '../useData';

export type AccountUser = AccessUser;

export type UserAPIKey = {
  id: string;
  name: string;
  scopes: string[];
  createdAt: string;
  lastUsed?: string;
};

const EMPTY_ACCOUNT_USER: AccountUser = {
  id: '',
  username: '',
  role: 'user',
  roleId: 'custom',
  enabled: true,
  canConvert: false,
  canReadJobs: false,
  maxFileBytes: 0,
  monthlyJobs: 0,
  monthlyBytes: 0,
  createdAt: '',
  groups: [],
  groupIds: [],
  permissions: {},
  permissionOrigins: {},
  quotas: {
    maxFileBytes: { mode: 'inherit' },
    monthlyJobs: { mode: 'inherit' },
    monthlyBytes: { mode: 'inherit' },
  },
};

const API_KEY_SCOPES: [string, string][] = [
  ['conversion:upload', 'Upload files'],
  ['conversion:submit', 'Start conversions'],
  ['compression:submit', 'Start compression'],
  ['jobs:read', 'View jobs'],
  ['jobs:cancel', 'Cancel jobs'],
  ['results:download', 'Download results'],
];

export function AccountPage({
  notify,
  session,
}: {
  notify: (message: string) => void;
  session: Session;
}) {
  const { data: userResponse, loading: userLoading } = useData<{
    user: AccountUser;
    usage?: { uploads: number; inputBytes: number; conversions: number };
  }>('/me', { user: EMPTY_ACCOUNT_USER });
  const {
    data: keyResponse,
    loading: keyLoading,
    refresh: refreshKeys,
  } = useData<{ keys: UserAPIKey[] }>('/me/api-keys', { keys: [] });

  const [name, setName] = useState('');
  const [scopes, setScopes] = useState<string[]>([
    'conversion:upload',
    'conversion:submit',
    'jobs:read',
    'jobs:cancel',
    'results:download',
  ]);
  const [newToken, setNewToken] = useState('');
  const [currentPassword, setCurrentPassword] = useState('');
  const [newPassword, setNewPassword] = useState('');

  const user = userResponse.user || EMPTY_ACCOUNT_USER;
  const keys = keyResponse.keys || [];

  const scopeAllowed = (scope: string): boolean => {
    if (scope === 'conversion:upload') {
      return Boolean(user.permissions?.['conversion.use'] || user.permissions?.['compression.use']);
    }
    if (scope === 'conversion:submit') return Boolean(user.permissions?.['conversion.use']);
    if (scope === 'compression:submit') return Boolean(user.permissions?.['compression.use']);
    if (scope === 'jobs:cancel') return Boolean(user.permissions?.['jobs.cancel']);
    return Boolean(user.permissions?.['jobs.read']);
  };

  const selectedScopes = scopes.filter(scopeAllowed);

  async function createKey(event: FormEvent) {
    event.preventDefault();
    try {
      const result = await apiRequest<{ key: { token: string } }>('/me/api-keys', {
        method: 'POST',
        body: json({ name, scopes: selectedScopes }),
      });
      setNewToken(result.key.token);
      setName('');
      notify('API key created. Copy it now; it will only be shown once.');
      await refreshKeys();
    } catch (err) {
      notify(explain((err as Error).message));
    }
  }

  async function revoke(key: UserAPIKey) {
    if (!confirm('Revoke API key “' + key.name + '”? Any service using it will stop working.')) return;
    try {
      await apiRequest('/me/api-keys/' + key.id, { method: 'DELETE' });
      notify('API key revoked.');
      await refreshKeys();
    } catch (err) {
      notify(explain((err as Error).message));
    }
  }

  async function changePassword(event: FormEvent) {
    event.preventDefault();
    try {
      await apiRequest('/me/password', {
        method: 'POST',
        body: json({ currentPassword, newPassword }),
      });
      setCurrentPassword('');
      setNewPassword('');
      notify('Password changed.');
    } catch (err) {
      notify(explain((err as Error).message));
    }
  }

  async function copyToken() {
    try {
      await navigator.clipboard.writeText(newToken);
      notify('API key copied to clipboard.');
    } catch {
      notify('Clipboard access was denied. Select and copy the key manually.');
    }
  }

  const toggleScope = (scope: string) =>
    setScopes(current =>
      current.includes(scope) ? current.filter(item => item !== scope) : [...current, scope],
    );

  return (
    <>
      <PageHeading
        eyebrow="YOUR ACCOUNT"
        title="Account & API keys"
        description="Manage your appearance, access, and credentials for connected services."
      />
      <AppearanceSettings />
      {session.role === 'admin' ? (
        <section className="admin-panel">
          <h2>
            <ShieldCheck size={18} /> Administrator access
          </h2>
          <p>
            The global administrator API credential is configured as <code>ENGINE_ADMIN_TOKEN</code> in your deployment
            environment. Keep it server-side; for automated clients, provide it through a secret store. User API keys
            are available to individual user accounts.
          </p>
        </section>
      ) : (
        <>
          {userLoading ? (
            <Loading />
          ) : (
            <section className="admin-panel">
              <h2>
                <Activity size={18} /> Permissions and monthly limits
              </h2>
              <div className="health-row">
                <span>Conversion access</span>
                <b>{user.permissions?.['conversion.use'] ? 'Allowed' : 'Disabled'}</b>
              </div>
              <div className="health-row">
                <span>Compression access</span>
                <b>{user.permissions?.['compression.use'] ? 'Allowed' : 'Disabled'}</b>
              </div>
              <div className="health-row">
                <span>Job history and results</span>
                <b>{user.permissions?.['jobs.read'] ? 'Allowed' : 'Disabled'}</b>
              </div>
              <div className="health-row">
                <span>Maximum file size</span>
                <b>{maxFileQuotaText(user.quotas?.maxFileBytes, user.maxFileBytes)}</b>
              </div>
              <div className="health-row">
                <span>Jobs this month</span>
                <b>
                  {userResponse.usage?.conversions || 0} /{' '}
                  {quotaText(user.quotas?.monthlyJobs, user.monthlyJobs)}
                </b>
              </div>
              <div className="health-row">
                <span>Input data this month</span>
                <b>
                  {bytes(userResponse.usage?.inputBytes || 0)} /{' '}
                  {quotaText(user.quotas?.monthlyBytes, user.monthlyBytes)}
                </b>
              </div>
            </section>
          )}

          <section className="admin-panel">
            <div className="panel-heading">
              <div>
                <h2>
                  <LockKeyhole size={18} /> API keys
                </h2>
                <p>Create a key for an automation or another service. The secret is shown once.</p>
              </div>
            </div>
            {newToken && (
              <div className="warning-box key-created">
                <div>
                  <b>Copy this key now</b>
                  <code>{newToken}</code>
                  <small>
                    Anyone with this value gets the permissions shown above. Do not put it in browser code or commit it
                    to Git.
                  </small>
                </div>
                <button className="secondary small" onClick={() => void copyToken()}>
                  <Copy size={14} /> Copy key
                </button>
                <button
                  className="icon-btn"
                  aria-label="Hide newly created key"
                  onClick={() => setNewToken('')}
                >
                  <X size={16} />
                </button>
              </div>
            )}
            <form className="inline-form" onSubmit={createKey}>
              <label className="field-label">
                Key name
                <input
                  required
                  maxLength={80}
                  value={name}
                  onChange={event => setName(event.target.value)}
                  placeholder="Home automation"
                />
              </label>
              <fieldset className="scope-list">
                <legend>Permissions</legend>
                {API_KEY_SCOPES.map(([scope, label]) => (
                  <label key={scope}>
                    <input
                      type="checkbox"
                      disabled={!scopeAllowed(scope)}
                      checked={scopeAllowed(scope) && scopes.includes(scope)}
                      onChange={() => toggleScope(scope)}
                    />
                    {label}
                  </label>
                ))}
              </fieldset>
              <button className="primary" disabled={!name.trim() || selectedScopes.length === 0}>
                <Plus size={15} /> Create API key
              </button>
            </form>
            {keyLoading ? (
              <Loading />
            ) : keys.length === 0 ? (
              <p className="muted-copy">No active API keys.</p>
            ) : (
              <div className="key-list">
                {keys.map(key => (
                  <article className="key-row" key={key.id}>
                    <div>
                      <b>{key.name}</b>
                      <small>{(key.scopes || []).join(' · ')}</small>
                      <small>
                        Created {new Date(key.createdAt).toLocaleString()}
                        {key.lastUsed ? ' · Last used ' + new Date(key.lastUsed).toLocaleString() : ''}
                      </small>
                    </div>
                    <button className="secondary small" onClick={() => void revoke(key)}>
                      <Trash2 size={14} /> Revoke
                    </button>
                  </article>
                ))}
              </div>
            )}
          </section>

          <section className="admin-panel">
            <h2>
              <LockKeyhole size={18} /> Change password
            </h2>
            <form className="password-form" onSubmit={changePassword}>
              <label className="field-label">
                Current password
                <input
                  type="password"
                  autoComplete="current-password"
                  required
                  value={currentPassword}
                  onChange={event => setCurrentPassword(event.target.value)}
                />
              </label>
              <label className="field-label">
                New password
                <input
                  type="password"
                  autoComplete="new-password"
                  minLength={5}
                  required
                  value={newPassword}
                  onChange={event => setNewPassword(event.target.value)}
                />
                <small>Five characters are accepted for compatibility; use a long passphrase.</small>
              </label>
              <button className="secondary">Update password</button>
            </form>
          </section>
        </>
      )}
      <ProfilePreferences session={session} notify={notify} />
    </>
  );
}
