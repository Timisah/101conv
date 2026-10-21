import { ShieldCheck } from 'lucide-react';

// Administrators hold every permission, so listing them all is noise; for other
// accounts the list stays available behind a one-line summary.
export function EffectivePermissions({ isAdmin, permissions }: { isAdmin: boolean; permissions?: Record<string, boolean> }) {
  const names = Object.entries(permissions || {}).filter(([, granted]) => granted).map(([name]) => name).sort();
  if (isAdmin) return <p className="effective-summary"><ShieldCheck size={14} /> Full administrator access</p>;
  if (!names.length) return <p className="effective-summary">No permissions granted</p>;
  return (
    <details className="effective-perms">
      <summary>{names.length} permission{names.length === 1 ? '' : 's'}</summary>
      <div className="effective-chips">{names.map(name => <span key={name}>{name}</span>)}</div>
    </details>
  );
}
