import { useState } from 'react';
import { NavLink, useLocation } from 'react-router-dom';
import {
  ArrowRight,
  Boxes,
  ChevronDown,
  HardDrive,
  Plus,
  RefreshCw,
  ShieldCheck,
  Trash2,
  Zap,
} from 'lucide-react';
import { Empty, ErrorState, Loading, PageHeading, StatePill } from '../../components/common';
import { UpdateAvailableLink } from '../catalog/StatusTag';
import { compareVersion } from '../catalog/catalogData';
import { explain, json } from '../format';
import { apiRequest } from '../session';
import { useData } from '../useData';
import type { Engine, Profile } from './types';

function EnginePicker({
  profile,
  notify,
  refresh,
}: {
  profile: Profile;
  notify: (message: string) => void;
  refresh: () => void;
}) {
  const [engines, setEngines] = useState<Engine[]>([]);
  const [open, setOpen] = useState(false);

  async function show() {
    try {
      const data = await apiRequest<{ engines: Engine[] }>('/engines');
      setEngines(data.engines);
      setOpen(!open);
    } catch (err) {
      notify(explain((err as Error).message));
    }
  }

  async function select(engineId: string, version: string) {
    try {
      await apiRequest('/profiles/' + profile.definition.id + '/engines/' + engineId, {
        method: 'PUT',
        body: json({ version }),
      });
      notify('Compatible engine version selected.');
      setOpen(false);
      refresh();
    } catch (err) {
      notify(explain((err as Error).message));
    }
  }

  return (
    <div className="engine-picker">
      <button className="text-btn" onClick={() => void show()}>
        Compatible engine versions <ChevronDown size={12} />
      </button>
      {open && (
        <div className="engine-picker-options">
          {profile.definition.engines.map(requirement => {
            const candidates = engines.filter(
              engine =>
                engine.definition.id === requirement.engineId &&
                (!requirement.versions.minimum ||
                  compareVersion(engine.definition.version, requirement.versions.minimum) >= 0) &&
                (!requirement.versions.maximumExclusive ||
                  compareVersion(engine.definition.version, requirement.versions.maximumExclusive) < 0) &&
                (requirement.capabilities || []).every(capability =>
                  (engine.definition.capabilities || []).includes(capability),
                ),
            );
            return (
              <div key={requirement.engineId}>
                <b>{requirement.engineId}</b>
                {candidates.length ? (
                  candidates.map(engine => (
                    <button
                      key={engine.definition.version}
                      onClick={() => void select(requirement.engineId, engine.definition.version)}
                    >
                      {engine.definition.version}
                      {profile.state.resolvedEngines?.[requirement.engineId] === engine.definition.version
                        ? ' · selected'
                        : ''}
                    </button>
                  ))
                ) : (
                  <small>No compatible installed versions</small>
                )}
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}

function ProfileVersionPicker({
  active,
  retained,
  notify,
  refresh,
}: {
  active: Profile;
  retained: Profile[];
  notify: (message: string) => void;
  refresh: () => void;
}) {
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState('');

  if (!retained.length) return null;

  async function activate(version: string) {
    setBusy('activate:' + version);
    try {
      await apiRequest('/profiles/' + active.definition.id + '/active', {
        method: 'PUT',
        body: json({ version }),
      });
      notify(active.definition.name + ' switched to v' + version + '.');
      setOpen(false);
      refresh();
    } catch (err) {
      notify(explain((err as Error).message));
    } finally {
      setBusy('');
    }
  }

  async function removeVersion(version: string) {
    if (
      !confirm(
        'Remove retained version v' +
          version +
          ' of “' +
          active.definition.name +
          '”? Versions pinned by automations or presets cannot be removed.',
      )
    ) {
      return;
    }
    setBusy('remove:' + version);
    try {
      await apiRequest('/profiles/' + active.definition.id + '/versions/' + encodeURIComponent(version), {
        method: 'DELETE',
      });
      notify('Retained version v' + version + ' removed.');
      refresh();
    } catch (err) {
      notify(explain((err as Error).message));
    } finally {
      setBusy('');
    }
  }

  return (
    <div className="engine-picker">
      <button className="text-btn" type="button" onClick={() => setOpen(!open)}>
        Installed versions ({retained.length + 1}) <ChevronDown size={12} />
      </button>
      {open && (
        <div className="engine-picker-options">
          <div>
            <b>v{active.definition.version} · active</b>
          </div>
          {retained.map(item => (
            <div key={item.definition.version}>
              <b>v{item.definition.version} · retained</b>
              <button
                type="button"
                disabled={busy !== ''}
                onClick={() => void activate(item.definition.version)}
              >
                {busy === 'activate:' + item.definition.version ? 'Switching…' : 'Set active (roll back)'}
              </button>
              <button
                type="button"
                disabled={busy !== ''}
                onClick={() => void removeVersion(item.definition.version)}
              >
                {busy === 'remove:' + item.definition.version ? 'Removing…' : 'Remove'}
              </button>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

export function ConvertersPage({ notify }: { notify: (message: string) => void }) {
  const location = useLocation();
  const requested = new URLSearchParams(location.search).get('tab');
  const tab = requested === 'engines' || requested === 'compression' ? requested : 'conversion';

  const {
    data: profiles,
    loading: loadingProfiles,
    error: profileError,
    refresh: refreshProfiles,
  } = useData<{ profiles: Profile[] }>('/profiles?includeRetained=true', { profiles: [] });
  const {
    data: engines,
    loading: loadingEngines,
    error: engineError,
    refresh: refreshEngines,
  } = useData<{ engines: Engine[] }>('/engines', { engines: [] });

  async function enable(profile: Profile) {
    try {
      await apiRequest('/profiles/' + profile.definition.id + '/enabled', {
        method: 'PATCH',
        body: json({ enabled: !profile.state.enabled }),
      });
      notify(profile.definition.name + (profile.state.enabled ? ' disabled' : ' enabled'));
      await refreshProfiles();
    } catch (err) {
      notify(explain((err as Error).message));
    }
  }

  async function removeProfile(profile: Profile) {
    if (!confirm('Remove profile “' + profile.definition.name + '”? This does not remove its engine.')) return;
    try {
      await apiRequest('/profiles/' + profile.definition.id, { method: 'DELETE' });
      notify('Profile removed.');
      await refreshProfiles();
    } catch (err) {
      notify(explain((err as Error).message));
    }
  }

  async function removeEngine(engine: Engine) {
    if (
      !confirm(
        'Remove ' +
          engine.definition.name +
          ' ' +
          engine.definition.version +
          '? Profiles using this engine may become unavailable.',
      )
    ) {
      return;
    }
    try {
      await apiRequest('/engines/' + engine.definition.id + '/' + engine.definition.version, {
        method: 'DELETE',
      });
      notify('Engine removed.');
      await refreshEngines();
    } catch (err) {
      notify(explain((err as Error).message));
    }
  }

  const allProfiles = profiles.profiles || [];
  const groupedProfiles = new Map<string, { active: Profile; retained: Profile[] }>();
  for (const item of allProfiles) {
    const existing = groupedProfiles.get(item.definition.id);
    if (!existing) {
      groupedProfiles.set(item.definition.id, { active: item, retained: [] });
    } else if (item.state.active && !existing.active.state.active) {
      existing.retained.push(existing.active);
      existing.active = item;
    } else {
      existing.retained.push(item);
    }
  }
  const activeGroups = Array.from(groupedProfiles.values());
  const visibleProfiles = activeGroups.filter(
    group => (group.active.definition.type === 'compression' ? 'compression' : 'conversion') === tab,
  );

  return (
    <>
      <PageHeading
        eyebrow="CONVERTER SETUP"
        title="Manage converters"
        description="Profiles define one workflow each. Engines are installed once and shared by conversion and compression profiles."
        action={
          <NavLink className="primary" to="/marketplace?tab=conversion">
            <Plus size={16} /> Browse repository catalog
          </NavLink>
        }
      />
      <div className="converter-concepts" aria-label="Converter components">
        <article>
          <span className="concept-number">1</span>
          <div>
            <b>Conversion profile</b>
            <small>Formats and conversion rules</small>
          </div>
        </article>
        <ArrowRight size={15} />
        <article>
          <span className="concept-number">2</span>
          <div>
            <b>Compression profile</b>
            <small>Compression controls and rules</small>
          </div>
        </article>
        <ArrowRight size={15} />
        <article>
          <span className="concept-number">3</span>
          <div>
            <b>Shared engine</b>
            <small>Installed once; reused by profiles</small>
          </div>
        </article>
      </div>

      <nav className="tabbar" aria-label="Converter management">
        <NavLink
          aria-current={tab === 'conversion' ? 'page' : undefined}
          className={() => (tab === 'conversion' ? 'active' : '')}
          to="/converters?tab=conversion"
        >
          Conversion profiles{' '}
          <span>{activeGroups.filter(item => item.active.definition.type !== 'compression').length}</span>
        </NavLink>
        <NavLink
          aria-current={tab === 'compression' ? 'page' : undefined}
          className={() => (tab === 'compression' ? 'active' : '')}
          to="/converters?tab=compression"
        >
          Compression profiles{' '}
          <span>{activeGroups.filter(item => item.active.definition.type === 'compression').length}</span>
        </NavLink>
        <NavLink
          aria-current={tab === 'engines' ? 'page' : undefined}
          className={() => (tab === 'engines' ? 'active' : '')}
          to="/converters?tab=engines"
        >
          Engines <span>{engines.engines?.length || 0}</span>
        </NavLink>
      </nav>

      {tab !== 'engines' ? (
        <>
          <div className="section-intro">
            <div>
              <h2>Installed {tab} profiles</h2>
              <p>
                {tab === 'compression'
                  ? 'Compression profiles expose size and quality controls.'
                  : 'Conversion profiles describe supported input and output formats.'}{' '}
                Engines are shared between these profile types.
              </p>
            </div>
            <button className="icon-btn" onClick={() => void refreshProfiles()} aria-label="Refresh profiles">
              <RefreshCw size={16} />
            </button>
          </div>
          {profileError && <ErrorState message={profileError} retry={refreshProfiles} />}
          {loadingProfiles ? (
            <Loading />
          ) : !visibleProfiles.length ? (
            <Empty
              icon={Boxes}
              title={'No ' + tab + ' profiles installed yet'}
              detail="Browse the repository catalog to find a profile for this workflow."
            >
              <NavLink className="secondary" to={'/marketplace?tab=' + tab}>
                Browse {tab} profiles <ArrowRight size={15} />
              </NavLink>
            </Empty>
          ) : (
            <div className="card-grid">
              {visibleProfiles.map(({ active: profile, retained }) => (
                <article className="converter-card" key={profile.definition.id}>
                  <div className="converter-card-top">
                    <span className="converter-glyph">
                      <Zap size={18} />
                    </span>
                    <StatePill state={profile.state.enabled ? 'enabled' : 'disabled'} />
                    <button
                      className="icon-btn quiet more-btn"
                      aria-label={'Remove ' + profile.definition.name}
                      onClick={() => void removeProfile(profile)}
                    >
                      <Trash2 size={16} />
                    </button>
                  </div>
                  <h3>{profile.definition.name}</h3>
                  <UpdateAvailableLink profileId={profile.definition.id} />
                  <p>{profile.definition.description || 'Profile for ' + profile.definition.id + '.'}</p>
                  <div className="format-chips">
                    {Array.from(new Set(profile.definition.rules.flatMap(rule => rule.sources)))
                      .slice(0, 6)
                      .map(source => (
                        <span key={'s' + source}>{source.toUpperCase()}</span>
                      ))}
                    <ArrowRight size={12} />
                    {Array.from(new Set(profile.definition.rules.flatMap(rule => rule.targets)))
                      .slice(0, 6)
                      .map(target => (
                        <span key={'t' + target}>{target.toUpperCase()}</span>
                      ))}
                  </div>
                  <div className="card-meta">
                    <span>
                      <Boxes size={14} />
                      {profile.definition.engines.map(engine => engine.engineId).join(', ')}
                    </span>
                    <span>v{profile.definition.version}</span>
                  </div>
                  <ProfileVersionPicker
                    active={profile}
                    retained={retained}
                    notify={notify}
                    refresh={refreshProfiles}
                  />
                  <EnginePicker profile={profile} notify={notify} refresh={refreshProfiles} />
                  <div className="card-bottom">
                    <span className="license">{profile.definition.license || 'License not specified'}</span>
                    <button
                      className="toggle"
                      aria-label={(profile.state.enabled ? 'Disable' : 'Enable') + ' ' + profile.definition.name}
                      aria-pressed={profile.state.enabled}
                      onClick={() => void enable(profile)}
                    >
                      <i />
                    </button>
                  </div>
                </article>
              ))}
            </div>
          )}
        </>
      ) : (
        <>
          <div className="section-intro">
            <div>
              <h2>Installed engines</h2>
              <p>Each engine is installed once and can power conversion and compression profiles.</p>
            </div>
            <button className="icon-btn" onClick={() => void refreshEngines()} aria-label="Refresh engines">
              <RefreshCw size={16} />
            </button>
          </div>
          {engineError && <ErrorState message={engineError} retry={refreshEngines} />}
          {loadingEngines ? (
            <Loading />
          ) : !engines.engines?.length ? (
            <Empty
              icon={HardDrive}
              title="No engines installed"
              detail="Install an engine from the repository catalog or register a local engine."
            />
          ) : (
            <div className="engine-list">
              {engines.engines.map(engine => (
                <article className="engine-card" key={engine.definition.id + '-' + engine.definition.version}>
                  <div className="engine-symbol">
                    <Boxes size={20} />
                  </div>
                  <div className="engine-main">
                    <div className="engine-title">
                      <h3>{engine.definition.name}</h3>
                      <span className="version-tag">v{engine.definition.version}</span>
                      {engine.installation.managed ? (
                        <span className="mini-badge trusted">
                          <ShieldCheck size={12} /> Managed
                        </span>
                      ) : (
                        <span className="mini-badge">Manual</span>
                      )}
                    </div>
                    <p>{engine.definition.description || engine.definition.id + ' converter engine.'}</p>
                    <div className="capabilities">
                      {(engine.definition.capabilities || []).map(capability => (
                        <span key={capability}>{capability}</span>
                      ))}
                    </div>
                    <small className="engine-path">
                      Installed location ·{' '}
                      {engine.installation.root ||
                        Object.values(engine.installation.executablePaths || {})[0] ||
                        'Path managed by system'}
                    </small>
                  </div>
                  <button
                    className="icon-btn danger-btn"
                    aria-label={'Remove ' + engine.definition.name}
                    onClick={() => void removeEngine(engine)}
                  >
                    <Trash2 size={17} />
                  </button>
                </article>
              ))}
            </div>
          )}
        </>
      )}

      <div className="inline-callout">
        <ShieldCheck size={16} />
        <span>
          <b>Administration only.</b> Regular users only see enabled profiles in Convert and Compress. They cannot add,
          install, or remove profiles or engines. One engine installation is reused across multiple profiles.
        </span>
      </div>
    </>
  );
}
