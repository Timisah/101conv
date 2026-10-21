import { NavLink } from 'react-router-dom';
import { StatusPill } from '../../components/common';
import { catalogProfileType } from './catalogData';
import { useCatalog } from './CatalogContext';
import type { Status } from './types';

// One vocabulary for converter state, used by the catalog, converter
// management and the navigation badge: New, Update available, Installed.
export function StatusTag({ status }: { status: Status }) {
  switch (status.kind) {
    case 'new': return <StatusPill tone="info">New · not installed</StatusPill>;
    case 'update': return <StatusPill tone="warn">Update available v{status.installed} → v{status.available}</StatusPill>;
    case 'installed': return <StatusPill tone="ok">Installed v{status.version}</StatusPill>;
    case 'engine-version': return <StatusPill tone="muted">Other version installed (v{status.installed})</StatusPill>;
  }
}

// Shown on an installed profile in converter management when the catalog
// offers a higher version of that same profile.
export function UpdateAvailableLink({ profileId }: { profileId: string }) {
  const { updates } = useCatalog();
  const update = updates.find(entry => entry.pkg.id === profileId);
  if (!update) return null;
  const tab = catalogProfileType(update.pkg) === 'compression' ? 'compression' : 'conversion';
  return (
    <NavLink className="update-link" to={`/marketplace?tab=${tab}&focus=${encodeURIComponent(profileId)}`} aria-label={`Review update to version ${update.pkg.version}`}>
      <StatusPill tone="warn">Update available → v{update.pkg.version}</StatusPill>
    </NavLink>
  );
}
