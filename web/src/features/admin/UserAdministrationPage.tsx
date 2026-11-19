import { useMemo, useState, type FormEvent } from 'react';
import { Plus, Search, Trash2 } from 'lucide-react';
import { ErrorState, PageHeading } from '../../components/common';
import { explain, json } from '../format';
import { apiRequest } from '../session';
import { useData } from '../useData';
import './admin.css';
import {
  AccessUserRow,
  DEFAULT_PERMISSIONS,
  GroupPermissionRules,
  PERMISSION_LABELS,
  PermissionChecks,
  QuotaEditor,
} from './AccessControls';
import { EffectiveAccessModal, GroupMembersModal, UserQuotasModal } from './AccessModals';
import type { AccessGroup, AccessRole, AccessUser, UserQuotas } from './quotas';

const INHERITED_QUOTAS: UserQuotas = {
  maxFileBytes: { mode: 'inherit' },
  monthlyJobs: { mode: 'inherit' },
  monthlyBytes: { mode: 'inherit' },
};

const UNLIMITED_QUOTAS: UserQuotas = {
  maxFileBytes: { mode: 'unlimited' },
  monthlyJobs: { mode: 'unlimited' },
  monthlyBytes: { mode: 'unlimited' },
};

const TABS = [
  ['users', 'Users'],
  ['groups', 'Groups'],
  ['roles', 'Roles'],
  ['permissions', 'Permissions'],
] as const;

export function UserAdministrationPage({ notify }: { notify: (message: string) => void }) {
  const { data: userData, error, refresh } = useData<{ users: AccessUser[] }>('/users', { users: [] });
  const { data: groupData, refresh: refreshGroups } = useData<{ groups: AccessGroup[] }>('/groups', {
    groups: [],
  });
  const { data: roleData, refresh: refreshRoles } = useData<{
    roles: AccessRole[];
    permissions: string[];
  }>('/roles', { roles: [], permissions: [] });

  const [view, setView] = useState<'users' | 'groups' | 'roles' | 'permissions'>('users');
  const [query, setQuery] = useState('');
  const [groupQuery, setGroupQuery] = useState('');
  const [groupSort, setGroupSort] = useState('name');
  const [groupPage, setGroupPage] = useState(0);
  const [enabledFilter, setEnabledFilter] = useState('all');
  const [sortBy, setSortBy] = useState('name');
  const [page, setPage] = useState(0);
  const [selected, setSelected] = useState<string[]>([]);
  const [selectedGroups, setSelectedGroups] = useState<string[]>([]);
  const [inspect, setInspect] = useState<AccessUser | null>(null);
  const [quotaUser, setQuotaUser] = useState<AccessUser | null>(null);
  const [quotaDraft, setQuotaDraft] = useState<UserQuotas>(INHERITED_QUOTAS);
  const [busy, setBusy] = useState(false);

  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [newRole, setNewRole] = useState('converter');
  const [createOpen, setCreateOpen] = useState(false);

  const [groupName, setGroupName] = useState('');
  const [groupDescription, setGroupDescription] = useState('');
  const [groupRole, setGroupRole] = useState('');
  const [groupPermissions, setGroupPermissions] = useState<Record<string, boolean>>({});
  const [groupQuota, setGroupQuota] = useState<UserQuotas>(INHERITED_QUOTAS);
  const [groupEdit, setGroupEdit] = useState<AccessGroup | null>(null);
  const [memberGroup, setMemberGroup] = useState<AccessGroup | null>(null);
  const [memberIDs, setMemberIDs] = useState<string[]>([]);

  const [roleEdit, setRoleEdit] = useState<AccessRole | null>(null);
  const [roleName, setRoleName] = useState('');
  const [roleDescription, setRoleDescription] = useState('');
  const [rolePermissions, setRolePermissions] = useState<Record<string, boolean>>({});

  const [bulkRole, setBulkRole] = useState('');
  const [bulkGroup, setBulkGroup] = useState('');
  const [bulkPermission, setBulkPermission] = useState('');
  const [bulkPermissionEffect, setBulkPermissionEffect] = useState('true');
  const [bulkQuota, setBulkQuota] = useState(false);
  const [quota, setQuota] = useState<UserQuotas>(UNLIMITED_QUOTAS);

  const users = userData.users || [];
  const groups = groupData.groups || [];
  const roles = roleData.roles || [];
  const permissions = roleData.permissions?.length ? roleData.permissions : DEFAULT_PERMISSIONS;

  const filtered = useMemo(
    () =>
      users
        .filter(
          user =>
            (!query || user.username.toLowerCase().includes(query.toLowerCase())) &&
            (enabledFilter === 'all' || String(user.enabled) === (enabledFilter === 'enabled' ? 'true' : 'false')),
        )
        .sort((a, b) =>
          sortBy === 'newest'
            ? (b.createdAt || '').localeCompare(a.createdAt || '')
            : sortBy === 'status'
              ? Number(b.enabled) - Number(a.enabled) || a.username.localeCompare(b.username)
              : a.username.localeCompare(b.username),
        ),
    [users, query, enabledFilter, sortBy],
  );

  const pageSize = 12;
  const pages = Math.max(1, Math.ceil(filtered.length / pageSize));
  const visible = filtered.slice(page * pageSize, (page + 1) * pageSize);

  const filteredGroups = useMemo(
    () =>
      groups
        .filter(
          group =>
            !groupQuery ||
            group.name.toLowerCase().includes(groupQuery.toLowerCase()) ||
            (group.description || '').toLowerCase().includes(groupQuery.toLowerCase()),
        )
        .sort((a, b) =>
          groupSort === 'members'
            ? b.memberCount - a.memberCount || a.name.localeCompare(b.name)
            : a.name.localeCompare(b.name),
        ),
    [groups, groupQuery, groupSort],
  );

  const groupPageSize = 12;
  const groupPages = Math.max(1, Math.ceil(filteredGroups.length / groupPageSize));
  const visibleGroups = filteredGroups.slice(groupPage * groupPageSize, (groupPage + 1) * groupPageSize);

  const reload = async () => {
    await Promise.all([refresh(), refreshGroups(), refreshRoles()]);
  };

  const mutate = async (path: string, method: string, body?: unknown) => {
    setBusy(true);
    try {
      const result = await apiRequest<unknown>(path, {
        method,
        ...(body ? { body: json(body) } : {}),
      });
      notify('Access settings saved.');
      await reload();
      return result;
    } catch (err) {
      notify(explain((err as Error).message));
      return null;
    } finally {
      setBusy(false);
    }
  };

  async function create(event: FormEvent) {
    event.preventDefault();
    const out = await mutate('/users', 'POST', {
      username,
      password,
      roleId: newRole,
      quotas: {
        maxFileBytes: { mode: 'limited', value: 1024 * 1024 * 1024 },
        monthlyJobs: { mode: 'unlimited' },
        monthlyBytes: { mode: 'unlimited' },
      },
    });
    if (out) {
      setUsername('');
      setPassword('');
      setCreateOpen(false);
      notify('User created. Share the initial password privately.');
    }
  }

  async function bulk(body: Record<string, unknown>, label: string) {
    if (!selected.length) return;
    if (selected.length > 500) {
      notify('Bulk updates are limited to 500 users at a time. Narrow your filters or selection.');
      return;
    }
    if (!confirm(`${label} for ${selected.length} selected user${selected.length === 1 ? '' : 's'}?`)) return;
    setBusy(true);
    try {
      const result = await apiRequest<{
        succeeded: string[];
        failed: { userId: string; message: string }[];
      }>('/users/bulk', {
        method: 'POST',
        body: json({ userIds: selected, ...body }),
      });
      const failed = result.failed || [];
      notify(
        `${result.succeeded.length} updated${
          failed.length ? `; ${failed.length} failed: ${failed.map(item => item.userId).join(', ')}` : ''
        }.`,
      );
      setSelected([]);
      await reload();
    } catch (err) {
      notify(explain((err as Error).message));
    } finally {
      setBusy(false);
    }
  }

  async function saveGroup(event: FormEvent) {
    event.preventDefault();
    const body = {
      id: groupEdit?.id,
      name: groupName,
      description: groupDescription,
      roleId: groupRole || undefined,
      permissions: groupPermissions,
      quotas: groupQuota,
    };
    const result = await mutate(
      groupEdit ? `/groups/${groupEdit.id}` : '/groups',
      groupEdit ? 'PUT' : 'POST',
      body,
    );
    if (result) {
      setGroupName('');
      setGroupDescription('');
      setGroupRole('');
      setGroupPermissions({});
      setGroupQuota(INHERITED_QUOTAS);
      setGroupEdit(null);
    }
  }

  async function saveRole(event: FormEvent) {
    event.preventDefault();
    const body = {
      id: roleEdit?.id || roleName.toLowerCase().replace(/[^a-z0-9]+/g, '-'),
      name: roleName,
      description: roleDescription,
      permissions: rolePermissions,
    };
    const result = await mutate(
      roleEdit ? `/roles/${roleEdit.id}` : '/roles',
      roleEdit ? 'PUT' : 'POST',
      body,
    );
    if (result) {
      setRoleEdit(null);
      setRoleName('');
      setRoleDescription('');
      setRolePermissions({});
    }
  }

  function openMembers(group: AccessGroup) {
    setMemberGroup(group);
    setMemberIDs(users.filter(user => user.groupIds?.includes(group.id)).map(user => user.id));
  }

  async function bulkDeleteGroups() {
    const targets = selectedGroups.filter(id => filteredGroups.some(group => group.id === id));
    if (
      !targets.length ||
      !confirm(
        `Delete ${targets.length} selected group${targets.length === 1 ? '' : 's'}? Memberships will be removed, while users and job history remain.`,
      )
    ) {
      return;
    }
    const failed: string[] = [];
    for (const id of targets) {
      try {
        await apiRequest('/groups/' + id, { method: 'DELETE' });
      } catch {
        failed.push(id);
      }
    }
    setSelectedGroups([]);
    notify(
      `${targets.length - failed.length} groups deleted${
        failed.length ? `; ${failed.length} failed: ${failed.join(', ')}` : ''
      }.`,
    );
    await reload();
  }

  const inspectedUser = inspect
    ? users.find(item => item.id === inspect.id) || inspect
    : null;

  return (
    <>
      <PageHeading
        title="Users & access"
        description="Manage accounts, reusable access policies, group membership, and explicit quotas."
      />
      <section className="admin-panel access-panel">
        <div className="access-tabs" role="tablist" aria-label="Access administration">
          {TABS.map(([id, label]) => (
            <button
              key={id}
              role="tab"
              aria-selected={view === id}
              className={view === id ? 'active' : ''}
              onClick={() => setView(id)}
            >
              {label}
              {id === 'users'
                ? ` (${users.length})`
                : id === 'groups'
                  ? ` (${groups.length})`
                  : id === 'roles'
                    ? ` (${roles.length})`
                    : ''}
            </button>
          ))}
        </div>

        {error && <ErrorState message={error} retry={reload} />}

        {view === 'users' && (
          <>
            <div className="access-toolbar">
              <label className="search-field">
                <Search size={16} />
                <input
                  aria-label="Search users"
                  placeholder="Search users"
                  value={query}
                  onChange={event => {
                    setQuery(event.target.value);
                    setPage(0);
                  }}
                />
              </label>
              <select
                aria-label="Filter by account state"
                value={enabledFilter}
                onChange={event => {
                  setEnabledFilter(event.target.value);
                  setPage(0);
                }}
              >
                <option value="all">All accounts</option>
                <option value="enabled">Enabled</option>
                <option value="disabled">Disabled</option>
              </select>
              <select
                aria-label="Sort users"
                value={sortBy}
                onChange={event => setSortBy(event.target.value)}
              >
                <option value="name">Name A–Z</option>
                <option value="newest">Newest</option>
                <option value="status">Enabled first</option>
              </select>
              <button className="primary small" onClick={() => setCreateOpen(!createOpen)}>
                <Plus size={14} /> Add user
              </button>
            </div>

            {createOpen && (
              <form className="access-create" onSubmit={create}>
                <label className="field-label">
                  Username
                  <input
                    required
                    minLength={3}
                    maxLength={64}
                    pattern="[A-Za-z0-9][A-Za-z0-9._-]{2,63}"
                    value={username}
                    onChange={event => setUsername(event.target.value)}
                    autoComplete="off"
                  />
                </label>
                <label className="field-label">
                  Initial password
                  <input
                    type="password"
                    required
                    minLength={5}
                    maxLength={256}
                    value={password}
                    onChange={event => setPassword(event.target.value)}
                    autoComplete="new-password"
                  />
                  <small>Five characters are accepted for compatibility; use a long passphrase.</small>
                </label>
                <label className="field-label">
                  Starting role
                  <select value={newRole} onChange={event => setNewRole(event.target.value)}>
                    {roles.map(role => (
                      <option key={role.id} value={role.id}>
                        {role.name}
                      </option>
                    ))}
                  </select>
                </label>
                <div className="access-row-actions">
                  <button className="primary" disabled={busy}>
                    <Plus size={14} /> Create user
                  </button>
                  <button className="secondary" type="button" onClick={() => setCreateOpen(false)}>
                    Cancel
                  </button>
                </div>
              </form>
            )}

            {selected.length > 0 && (
              <div className="bulk-toolbar">
                <b>{selected.length} selected</b>
                <button
                  className="secondary small"
                  disabled={busy}
                  onClick={() => void bulk({ enabled: true }, 'Enable accounts')}
                >
                  Enable
                </button>
                <button
                  className="secondary small"
                  disabled={busy}
                  onClick={() => void bulk({ enabled: false }, 'Disable accounts')}
                >
                  Disable
                </button>
                <select
                  aria-label="Bulk role"
                  value={bulkRole}
                  onChange={event => setBulkRole(event.target.value)}
                >
                  <option value="">Set role…</option>
                  <option value="__admin">Administrator</option>
                  {roles.map(role => (
                    <option key={role.id} value={role.id}>
                      {role.name}
                    </option>
                  ))}
                </select>
                <button
                  className="secondary small"
                  disabled={!bulkRole || busy}
                  onClick={() =>
                    void bulk(
                      bulkRole === '__admin' ? { role: 'admin' } : { role: 'user', roleId: bulkRole },
                      'Change role',
                    )
                  }
                >
                  Apply role
                </button>
                <select
                  aria-label="Bulk group"
                  value={bulkGroup}
                  onChange={event => setBulkGroup(event.target.value)}
                >
                  <option value="">Choose group…</option>
                  {groups.map(group => (
                    <option key={group.id} value={group.id}>
                      {group.name}
                    </option>
                  ))}
                </select>
                <button
                  className="secondary small"
                  disabled={!bulkGroup || busy}
                  onClick={() => void bulk({ groupAction: 'add', groupId: bulkGroup }, 'Assign group')}
                >
                  Add group
                </button>
                <button
                  className="secondary small"
                  disabled={!bulkGroup || busy}
                  onClick={() => void bulk({ groupAction: 'remove', groupId: bulkGroup }, 'Remove group')}
                >
                  Remove group
                </button>
                <select
                  aria-label="Permission to change"
                  value={bulkPermission}
                  onChange={event => setBulkPermission(event.target.value)}
                >
                  <option value="">Permission…</option>
                  {permissions.map(permission => (
                    <option key={permission} value={permission}>
                      {PERMISSION_LABELS[permission]}
                    </option>
                  ))}
                </select>
                <select
                  aria-label="Permission effect"
                  value={bulkPermissionEffect}
                  onChange={event => setBulkPermissionEffect(event.target.value)}
                >
                  <option value="true">Allow</option>
                  <option value="false">Deny</option>
                  <option value="inherit">Inherit</option>
                </select>
                <button
                  className="secondary small"
                  disabled={!bulkPermission || busy}
                  onClick={() =>
                    void bulk(
                      {
                        permissionOverrides: {
                          [bulkPermission]:
                            bulkPermissionEffect === 'inherit' ? null : bulkPermissionEffect === 'true',
                        },
                      },
                      'Change permissions',
                    )
                  }
                >
                  Apply permission
                </button>
                <button className="secondary small" onClick={() => setBulkQuota(!bulkQuota)}>
                  Quotas
                </button>
                <button className="text-btn" onClick={() => setSelected([])}>
                  Clear
                </button>
              </div>
            )}

            {bulkQuota && selected.length > 0 && (
              <div className="quota-bulk">
                <QuotaEditor value={quota} onChange={setQuota} allowInherit={false} />
                <button
                  className="primary small"
                  disabled={busy}
                  onClick={() => void bulk({ quotas: quota }, 'Set quotas')}
                >
                  Apply quotas to {selected.length}
                </button>
              </div>
            )}

            <div className="select-all-row">
              <label>
                <input
                  type="checkbox"
                  checked={filtered.length > 0 && selected.length === filtered.length}
                  onChange={event => setSelected(event.target.checked ? filtered.map(user => user.id) : [])}
                />{' '}
                Select all {filtered.length} filtered users
              </label>
              <span>{filtered.length} matching</span>
            </div>

            {users.length === 0 ? (
              <p className="muted-copy">No user accounts yet.</p>
            ) : filtered.length === 0 ? (
              <p className="muted-copy">No users match these filters.</p>
            ) : (
              <div className="access-user-list">
                {visible.map(user => (
                  <AccessUserRow
                    key={user.id}
                    user={user}
                    roles={roles}
                    selected={selected.includes(user.id)}
                    onSelect={checked =>
                      setSelected(current =>
                        checked ? [...current, user.id] : current.filter(id => id !== user.id),
                      )
                    }
                    onUpdate={async change => {
                      await mutate('/users/' + user.id, 'PATCH', change);
                    }}
                    onInspect={() => setInspect(user)}
                    onQuota={() => {
                      setQuotaUser(user);
                      setQuotaDraft(user.quotas || INHERITED_QUOTAS);
                    }}
                  />
                ))}
              </div>
            )}

            <div className="pagination">
              <button
                className="secondary small"
                disabled={page === 0}
                onClick={() => setPage(Math.max(0, page - 1))}
              >
                Previous
              </button>
              <span>
                Page {page + 1} of {pages}
              </span>
              <button
                className="secondary small"
                disabled={page + 1 >= pages}
                onClick={() => setPage(Math.min(pages - 1, page + 1))}
              >
                Next
              </button>
            </div>
          </>
        )}

        {view === 'groups' && (
          <div className="access-view-grid">
            <div>
              <div className="access-toolbar">
                <h3>Groups</h3>
                <button
                  className="primary small"
                  onClick={() => {
                    setGroupEdit(null);
                    setGroupName('');
                    setGroupDescription('');
                    setGroupRole('');
                    setGroupPermissions({});
                    setGroupQuota(INHERITED_QUOTAS);
                  }}
                >
                  <Plus size={14} /> New group
                </button>
              </div>
              <div className="access-toolbar">
                <label className="search-field">
                  <Search size={16} />
                  <input
                    aria-label="Search groups"
                    placeholder="Search groups"
                    value={groupQuery}
                    onChange={event => {
                      setGroupQuery(event.target.value);
                      setGroupPage(0);
                    }}
                  />
                </label>
                <select
                  aria-label="Sort groups"
                  value={groupSort}
                  onChange={event => setGroupSort(event.target.value)}
                >
                  <option value="name">Name A–Z</option>
                  <option value="members">Most members</option>
                </select>
              </div>
              <div className="select-all-row">
                <label>
                  <input
                    type="checkbox"
                    checked={
                      filteredGroups.length > 0 &&
                      filteredGroups.every(group => selectedGroups.includes(group.id))
                    }
                    onChange={event =>
                      setSelectedGroups(
                        event.target.checked
                          ? Array.from(new Set([...selectedGroups, ...filteredGroups.map(group => group.id)]))
                          : selectedGroups.filter(id => !filteredGroups.some(group => group.id === id)),
                      )
                    }
                  />{' '}
                  Select all {filteredGroups.length} filtered groups
                </label>
                <span>{filteredGroups.length} matching</span>
              </div>
              {selectedGroups.length > 0 && (
                <div className="bulk-toolbar">
                  <b>{selectedGroups.length} selected</b>
                  <button className="secondary small" disabled={busy} onClick={() => void bulkDeleteGroups()}>
                    Delete selected groups
                  </button>
                  <button className="text-btn" onClick={() => setSelectedGroups([])}>
                    Clear
                  </button>
                </div>
              )}
              {visibleGroups.map(group => (
                <article className="access-group-row" key={group.id}>
                  <label>
                    <input
                      type="checkbox"
                      checked={selectedGroups.includes(group.id)}
                      onChange={event =>
                        setSelectedGroups(current =>
                          event.target.checked
                            ? [...current, group.id]
                            : current.filter(id => id !== group.id),
                        )
                      }
                      aria-label={'Select ' + group.name}
                    />
                  </label>
                  <div>
                    <b>{group.name}</b>
                    <small>
                      {group.description || 'No description'} · {group.memberCount} members ·{' '}
                      {group.roleId
                        ? roles.find(role => role.id === group.roleId)?.name
                        : 'Custom permissions'}
                    </small>
                  </div>
                  <div>
                    <button className="secondary small" onClick={() => openMembers(group)}>
                      Members
                    </button>
                    <button
                      className="secondary small"
                      onClick={() => {
                        setGroupEdit(group);
                        setGroupName(group.name);
                        setGroupDescription(group.description || '');
                        setGroupRole(group.roleId || '');
                        setGroupPermissions(group.permissions || {});
                        setGroupQuota(group.quotas || INHERITED_QUOTAS);
                      }}
                    >
                      Edit
                    </button>
                    <button
                      className="icon-btn danger-btn"
                      aria-label={'Delete ' + group.name}
                      onClick={() => {
                        if (
                          confirm(
                            `Delete group ${group.name}? Memberships will be removed, users and jobs remain.`,
                          )
                        ) {
                          void mutate('/groups/' + group.id, 'DELETE');
                        }
                      }}
                    >
                      <Trash2 size={14} />
                    </button>
                  </div>
                </article>
              ))}
              <div className="pagination">
                <button
                  className="secondary small"
                  disabled={groupPage === 0}
                  onClick={() => setGroupPage(Math.max(0, groupPage - 1))}
                >
                  Previous
                </button>
                <span>
                  Page {groupPage + 1} of {groupPages}
                </span>
                <button
                  className="secondary small"
                  disabled={groupPage + 1 >= groupPages}
                  onClick={() => setGroupPage(Math.min(groupPages - 1, groupPage + 1))}
                >
                  Next
                </button>
              </div>
            </div>

            <div className="access-editor">
              <h3>{groupEdit ? 'Edit group' : 'Create a group'}</h3>
              <form onSubmit={saveGroup}>
                <label className="field-label">
                  Name
                  <input
                    required
                    maxLength={100}
                    value={groupName}
                    onChange={event => setGroupName(event.target.value)}
                  />
                </label>
                <label className="field-label">
                  Description
                  <input
                    maxLength={500}
                    value={groupDescription}
                    onChange={event => setGroupDescription(event.target.value)}
                  />
                </label>
                <label className="field-label">
                  Reusable role
                  <select value={groupRole} onChange={event => setGroupRole(event.target.value)}>
                    <option value="">No role</option>
                    {roles.map(role => (
                      <option key={role.id} value={role.id}>
                        {role.name}
                      </option>
                    ))}
                  </select>
                </label>
                <p className="muted-copy">
                  Group rules can inherit from the role, allow, or deny each permission. A deny in any group wins over
                  other group grants.
                </p>
                <GroupPermissionRules
                  permissions={permissions}
                  labels={PERMISSION_LABELS}
                  value={groupPermissions}
                  onChange={setGroupPermissions}
                />
                <QuotaEditor value={groupQuota} onChange={setGroupQuota} />
                <button className="primary" disabled={!groupName.trim() || busy}>
                  Save group
                </button>
              </form>
            </div>
          </div>
        )}

        {view === 'roles' && (
          <div className="access-view-grid">
            <div>
              <div className="access-toolbar">
                <h3>Reusable roles</h3>
                <button
                  className="primary small"
                  onClick={() => {
                    setRoleEdit(null);
                    setRoleName('');
                    setRoleDescription('');
                    setRolePermissions({});
                  }}
                >
                  <Plus size={14} /> New role
                </button>
              </div>
              {roles.map(role => (
                <article className="access-group-row" key={role.id}>
                  <div>
                    <b>
                      {role.name}
                      {role.builtIn ? ' · built in' : ''}
                    </b>
                    <small>
                      {role.description || 'No description'} ·{' '}
                      {Object.values(role.permissions || {}).filter(Boolean).length} permissions
                    </small>
                  </div>
                  <div>
                    <button
                      className="secondary small"
                      disabled={role.builtIn}
                      onClick={() => {
                        setRoleEdit(role);
                        setRoleName(role.name);
                        setRoleDescription(role.description || '');
                        setRolePermissions(role.permissions || {});
                      }}
                    >
                      Edit
                    </button>
                    {!role.builtIn && (
                      <button
                        className="icon-btn danger-btn"
                        aria-label={'Delete ' + role.name}
                        onClick={() => {
                          if (confirm(`Delete role ${role.name}?`)) {
                            void mutate('/roles/' + role.id, 'DELETE');
                          }
                        }}
                      >
                        <Trash2 size={14} />
                      </button>
                    )}
                  </div>
                </article>
              ))}
            </div>

            <div className="access-editor">
              <h3>{roleEdit ? 'Edit role' : 'Create a role'}</h3>
              <form onSubmit={saveRole}>
                <label className="field-label">
                  Name
                  <input
                    required
                    maxLength={100}
                    value={roleName}
                    onChange={event => setRoleName(event.target.value)}
                  />
                </label>
                <label className="field-label">
                  Description
                  <input
                    maxLength={500}
                    value={roleDescription}
                    onChange={event => setRoleDescription(event.target.value)}
                  />
                </label>
                <PermissionChecks
                  permissions={permissions}
                  labels={PERMISSION_LABELS}
                  value={rolePermissions}
                  onChange={setRolePermissions}
                />
                <button className="primary" disabled={!roleName.trim() || busy}>
                  Save role
                </button>
              </form>
            </div>
          </div>
        )}

        {view === 'permissions' && (
          <div className="permission-reference">
            <p>
              Permissions are evaluated on the server. Role and group grants combine; a denial in any group wins. An
              individual allow or deny override takes precedence over all inherited policies.
            </p>
            {permissions.map(permission => (
              <article key={permission}>
                <code>{permission}</code>
                <span>{PERMISSION_LABELS[permission] || permission}</span>
              </article>
            ))}
          </div>
        )}

        {memberGroup && (
          <GroupMembersModal
            group={memberGroup}
            users={users}
            memberIDs={memberIDs}
            onChangeMembers={setMemberIDs}
            onSave={async () => {
              const out = await mutate(`/groups/${memberGroup.id}/members`, 'PUT', {
                userIds: memberIDs,
              });
              if (out !== null) setMemberGroup(null);
            }}
            onClose={() => setMemberGroup(null)}
          />
        )}

        {inspectedUser && (
          <EffectiveAccessModal
            user={inspectedUser}
            roles={roles}
            permissions={permissions}
            labels={PERMISSION_LABELS}
            onOverride={async (permission, value) => {
              await mutate('/users/' + inspectedUser.id, 'PATCH', {
                permissionOverrides: { [permission]: value },
              });
            }}
            onClose={() => setInspect(null)}
          />
        )}

        {quotaUser && (
          <UserQuotasModal
            user={quotaUser}
            draft={quotaDraft}
            busy={busy}
            onChange={setQuotaDraft}
            onSave={async () => {
              const saved = await mutate('/users/' + quotaUser.id, 'PATCH', { quotas: quotaDraft });
              if (saved) setQuotaUser(null);
            }}
            onClose={() => setQuotaUser(null)}
          />
        )}
      </section>
    </>
  );
}
