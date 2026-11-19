import { ShieldCheck } from 'lucide-react';
import { bytes } from '../format';
import { QuotaEditor } from './AccessControls';
import type { AccessGroup, AccessRole, AccessUser, UserQuotas } from './quotas';

export function GroupMembersModal({
  group,
  users,
  memberIDs,
  onChangeMembers,
  onSave,
  onClose,
}: {
  group: AccessGroup;
  users: AccessUser[];
  memberIDs: string[];
  onChangeMembers: (ids: string[]) => void;
  onSave: () => Promise<void>;
  onClose: () => void;
}) {
  return (
    <div
      className="modal-scrim"
      role="presentation"
      onMouseDown={event => {
        if (event.target === event.currentTarget) onClose();
      }}
    >
      <section className="modal access-modal">
        <div className="modal-top">
          <div>
            <p className="eyebrow">GROUP MEMBERSHIP</p>
            <h2>{group.name}</h2>
          </div>
          <button className="icon-btn" aria-label="Close" onClick={onClose}>
            ×
          </button>
        </div>
        <div className="member-picker">
          {users.map(user => (
            <label key={user.id}>
              <input
                type="checkbox"
                checked={memberIDs.includes(user.id)}
                onChange={event =>
                  onChangeMembers(
                    event.target.checked
                      ? [...memberIDs, user.id]
                      : memberIDs.filter(id => id !== user.id),
                  )
                }
              />
              <span>{user.username}</span>
              <small>{user.enabled ? 'Enabled' : 'Disabled'}</small>
            </label>
          ))}
        </div>
        <div className="modal-actions">
          <button className="secondary" onClick={onClose}>
            Cancel
          </button>
          <button className="primary" onClick={() => void onSave()}>
            <ShieldCheck size={14} /> Save {memberIDs.length} members
          </button>
        </div>
      </section>
    </div>
  );
}

export function EffectiveAccessModal({
  user,
  roles,
  permissions,
  labels,
  onOverride,
  onClose,
}: {
  user: AccessUser;
  roles: AccessRole[];
  permissions: string[];
  labels: Record<string, string>;
  onOverride: (permission: string, value: boolean | null) => Promise<void>;
  onClose: () => void;
}) {
  const roleLabel =
    user.role === 'admin'
      ? 'Administrator'
      : roles.find(role => role.id === user.roleId)?.name || user.roleId || 'Custom';

  return (
    <div
      className="modal-scrim"
      role="presentation"
      onMouseDown={event => {
        if (event.target === event.currentTarget) onClose();
      }}
    >
      <section className="modal access-modal">
        <div className="modal-top">
          <div>
            <p className="eyebrow">EFFECTIVE ACCESS</p>
            <h2>{user.username}</h2>
            <p>
              Role: {roleLabel} · Groups: {user.groups?.join(', ') || 'None'}
            </p>
          </div>
          <button className="icon-btn" aria-label="Close" onClick={onClose}>
            ×
          </button>
        </div>
        <div className="effective-permissions">
          {permissions.map(name => {
            const hasOverride = user.permissionOrigins?.[name]?.some(
              origin => origin.source === 'user override',
            );
            return (
              <article key={name}>
                <b>{labels[name] || name}</b>
                <span className={user.permissions?.[name] ? 'permission-allow' : 'permission-deny'}>
                  {user.permissions?.[name] ? 'Allowed' : 'Denied'}
                </span>
                <small>
                  {(user.permissionOrigins?.[name] || [])
                    .map(origin => `${origin.source}: ${origin.effect ? 'allow' : 'deny'}`)
                    .join(' · ') || 'No grant source'}
                </small>
                <select
                  aria-label={'Override ' + name}
                  value={hasOverride ? String(Boolean(user.permissions?.[name])) : 'inherit'}
                  onChange={event => {
                    const value =
                      event.target.value === 'inherit' ? null : event.target.value === 'true';
                    void onOverride(name, value);
                  }}
                >
                  <option value="inherit">Inherit</option>
                  <option value="true">Allow override</option>
                  <option value="false">Deny override</option>
                </select>
              </article>
            );
          })}
        </div>
        <div className="modal-actions">
          <button className="secondary" onClick={onClose}>
            Close
          </button>
        </div>
      </section>
    </div>
  );
}

export function UserQuotasModal({
  user,
  draft,
  busy,
  onChange,
  onSave,
  onClose,
}: {
  user: AccessUser;
  draft: UserQuotas;
  busy: boolean;
  onChange: (next: UserQuotas) => void;
  onSave: () => Promise<void>;
  onClose: () => void;
}) {
  return (
    <div
      className="modal-scrim"
      role="presentation"
      onMouseDown={event => {
        if (event.target === event.currentTarget) onClose();
      }}
    >
      <section className="modal access-modal">
        <div className="modal-top">
          <div>
            <p className="eyebrow">ACCOUNT QUOTAS</p>
            <h2>{user.username}</h2>
            <p>
              Current month: {user.usage?.jobs || 0} jobs and {bytes(user.usage?.inputBytes || 0)} uploaded.
            </p>
          </div>
          <button className="icon-btn" aria-label="Close" onClick={onClose}>
            ×
          </button>
        </div>
        <QuotaEditor value={draft} onChange={onChange} />
        <div className="modal-actions">
          <button className="secondary" onClick={onClose}>
            Cancel
          </button>
          <button className="primary" disabled={busy} onClick={() => void onSave()}>
            Save quotas
          </button>
        </div>
      </section>
    </div>
  );
}
