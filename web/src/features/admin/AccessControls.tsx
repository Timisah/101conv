import { StatePill } from '../../components/common';
import { bytes } from '../format';
import { EffectivePermissions } from './EffectivePermissions';
import {
  maxFileQuotaText,
  quotaText,
  type AccessRole,
  type AccessUser,
  type QuotaLimit,
  type UserQuotas,
} from './quotas';

export const DEFAULT_PERMISSIONS = [
  'conversion.use',
  'compression.use',
  'jobs.read',
  'jobs.cancel',
  'converters.manage',
  'repositories.manage',
  'users.manage',
  'groups.manage',
  'roles.manage',
  'api_keys.manage',
];

export const PERMISSION_LABELS: Record<string, string> = {
  'conversion.use': 'Convert files',
  'compression.use': 'Compress files',
  'jobs.read': 'View jobs and results',
  'jobs.cancel': 'Cancel jobs',
  'converters.manage': 'Manage engines and profiles',
  'repositories.manage': 'Manage repositories',
  'users.manage': 'Administer user accounts',
  'groups.manage': 'Manage groups and memberships',
  'roles.manage': 'Manage reusable roles',
  'api_keys.manage': 'Manage API credentials',
};

export function AccessUserRow({
  user,
  roles,
  selected,
  onSelect,
  onUpdate,
  onInspect,
  onQuota,
}: {
  user: AccessUser;
  roles: AccessRole[];
  selected: boolean;
  onSelect: (checked: boolean) => void;
  onUpdate: (change: Record<string, unknown>) => void;
  onInspect: () => void;
  onQuota: () => void;
}) {
  const roleName = roles.find(role => role.id === user.roleId)?.name || user.roleId || 'Custom';

  return (
    <article className="access-user-row">
      <label className="user-select">
        <input
          type="checkbox"
          checked={selected}
          onChange={event => onSelect(event.target.checked)}
          aria-label={'Select ' + user.username}
        />
      </label>
      <div className="access-user-main">
        <div className="access-user-title">
          <b>{user.username}</b>
          <StatePill state={user.enabled ? 'enabled' : 'disabled'} />
        </div>
        <small>
          {user.role === 'admin' ? 'Administrator' : roleName} · {user.groups?.join(', ') || 'No groups'} · Joined{' '}
          {new Date(user.createdAt).toLocaleDateString()}
        </small>
        <EffectivePermissions isAdmin={user.role === 'admin'} permissions={user.permissions} />
        <div className="usage-summary">
          <span>Max file {maxFileQuotaText(user.quotas?.maxFileBytes, user.maxFileBytes)}</span>
          <span>
            Jobs {user.usage?.jobs || 0} / {quotaText(user.quotas?.monthlyJobs, user.monthlyJobs)}
          </span>
          <span>
            Data {bytes(user.usage?.inputBytes || 0)} / {quotaText(user.quotas?.monthlyBytes, user.monthlyBytes)}
          </span>
        </div>
      </div>
      <div className="access-user-actions">
        <label>
          <input
            type="checkbox"
            checked={user.enabled}
            onChange={event => onUpdate({ enabled: event.target.checked })}
          />{' '}
          Enabled
        </label>
        <select
          aria-label={'Role for ' + user.username}
          value={user.role === 'admin' ? '__admin' : user.roleId || 'custom'}
          onChange={event =>
            event.target.value === '__admin'
              ? onUpdate({ role: 'admin' })
              : onUpdate({ role: 'user', roleId: event.target.value })
          }
        >
          <option value="__admin">Administrator</option>
          <option value="custom">Custom</option>
          {roles.map(role => (
            <option key={role.id} value={role.id}>
              {role.name}
            </option>
          ))}
        </select>
        <button className="secondary small" onClick={onQuota}>
          Quotas
        </button>
        <button className="secondary small" onClick={onInspect}>
          Permissions
        </button>
      </div>
    </article>
  );
}

const QUOTA_FIELDS: [keyof UserQuotas, string, 'bytes' | 'jobs'][] = [
  ['maxFileBytes', 'Max file size', 'bytes'],
  ['monthlyJobs', 'Jobs per month', 'jobs'],
  ['monthlyBytes', 'Uploaded data per month', 'bytes'],
];

export function QuotaEditor({
  value,
  onChange,
  allowInherit = true,
}: {
  value: UserQuotas;
  onChange: (quotas: UserQuotas) => void;
  allowInherit?: boolean;
}) {
  return (
    <div className="quota-editor">
      {QUOTA_FIELDS.map(([key, label, unit]) => {
        const quota = value?.[key] || { mode: allowInherit ? 'inherit' : 'unlimited' };
        return (
          <div className="quota-edit-row" key={key}>
            <label>
              {label}
              <select
                value={quota.mode}
                onChange={event =>
                  onChange({
                    ...value,
                    [key]: {
                      mode: event.target.value as QuotaLimit['mode'],
                      ...(event.target.value === 'limited' ? { value: quota.value || 1 } : {}),
                    },
                  })
                }
              >
                {allowInherit && <option value="inherit">Inherit from groups</option>}
                <option value="unlimited">Unlimited</option>
                <option value="limited">Limited</option>
              </select>
            </label>
            {quota.mode === 'limited' && (
              <label>
                Limit ({unit === 'bytes' ? 'MB' : 'jobs'})
                <input
                  type="number"
                  min={1}
                  max={key === 'maxFileBytes' ? 1024 : undefined}
                  value={
                    unit === 'bytes'
                      ? Math.max(1, Math.round((quota.value || 1) / 1024 / 1024))
                      : quota.value || 1
                  }
                  onChange={event =>
                    onChange({
                      ...value,
                      [key]: {
                        mode: 'limited',
                        value: Number(event.target.value) * (unit === 'bytes' ? 1024 * 1024 : 1),
                      },
                    })
                  }
                />
              </label>
            )}
          </div>
        );
      })}
    </div>
  );
}

export function PermissionChecks({
  permissions,
  labels,
  value,
  onChange,
}: {
  permissions: string[];
  labels: Record<string, string>;
  value: Record<string, boolean>;
  onChange: (next: Record<string, boolean>) => void;
}) {
  return (
    <fieldset className="access-permission-checks">
      <legend>Permissions</legend>
      {permissions.map(permission => (
        <label key={permission}>
          <input
            type="checkbox"
            checked={Boolean(value[permission])}
            onChange={event => onChange({ ...value, [permission]: event.target.checked })}
          />
          {labels[permission] || permission}
        </label>
      ))}
    </fieldset>
  );
}

export function GroupPermissionRules({
  permissions,
  labels,
  value,
  onChange,
}: {
  permissions: string[];
  labels: Record<string, string>;
  value: Record<string, boolean>;
  onChange: (next: Record<string, boolean>) => void;
}) {
  return (
    <fieldset className="access-permission-checks">
      <legend>Group permission rules</legend>
      {permissions.map(permission => (
        <label className="group-policy-row" key={permission}>
          <span>{labels[permission] || permission}</span>
          <select
            aria-label={labels[permission] || permission}
            value={value[permission] === undefined ? 'inherit' : String(value[permission])}
            onChange={event => {
              const next = { ...value };
              if (event.target.value === 'inherit') {
                delete next[permission];
              } else {
                next[permission] = event.target.value === 'true';
              }
              onChange(next);
            }}
          >
            <option value="inherit">Inherit</option>
            <option value="true">Allow</option>
            <option value="false">Deny</option>
          </select>
        </label>
      ))}
    </fieldset>
  );
}
