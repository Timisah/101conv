import { useState } from 'react';
import { ArrowRight, ChevronDown, RefreshCw, Settings2 } from 'lucide-react';
import { ErrorState, Loading } from '../../components/common';
import { explain, json } from '../format';
import { apiRequest, type Session } from '../session';
import { useData } from '../useData';
import type { PreferenceMapping } from './types';

export function ProfilePreferences({
  session,
  notify,
}: {
  session: Session;
  notify: (message: string) => void;
}) {
  const { data, loading, error, refresh } = useData<{ preferences: PreferenceMapping[] }>(
    '/profile-preferences',
    { preferences: [] },
  );

  const can = (mode: 'convert' | 'compress') =>
    session.role === 'admin' ||
    Boolean(session.permissions?.[mode === 'convert' ? 'conversion.use' : 'compression.use']);

  const [mode, setMode] = useState<'convert' | 'compress'>(can('convert') ? 'convert' : 'compress');
  if (!can('convert') && !can('compress')) return null;

  const rows = (data.preferences || []).filter(row => row.mode === mode && can(row.mode));

  async function change(row: PreferenceMapping, profileId: string) {
    try {
      const path = `/profile-preferences/me/${row.mode}/${encodeURIComponent(row.sourceFormat)}/${encodeURIComponent(row.targetFormat)}`;
      if (profileId) {
        await apiRequest(path, { method: 'PUT', body: json({ profileId }) });
      } else {
        await apiRequest(path, { method: 'DELETE' });
      }
      notify(profileId ? 'Personal profile preference saved.' : 'Using the administrator default.');
      await refresh();
    } catch (err) {
      notify(explain((err as Error).message));
    }
  }

  return (
    <section className="admin-panel profile-preferences">
      <div className="panel-heading">
        <div>
          <h2>
            <Settings2 size={18} /> Preferences
          </h2>
          <p>
            Set a personal profile for an exact format mapping. Leave it inherited to use the administrator default.
          </p>
        </div>
        <button className="icon-btn" aria-label="Refresh preferences" onClick={() => void refresh()}>
          <RefreshCw size={15} />
        </button>
      </div>
      {can('convert') && can('compress') && (
        <nav className="tabbar preference-tabs" aria-label="Personal profile preference category">
          <button className={mode === 'convert' ? 'active' : ''} onClick={() => setMode('convert')}>
            Conversion profiles
          </button>
          <button className={mode === 'compress' ? 'active' : ''} onClick={() => setMode('compress')}>
            Compression profiles
          </button>
        </nav>
      )}
      {error ? (
        <ErrorState message={error} retry={refresh} />
      ) : loading ? (
        <Loading />
      ) : rows.length === 0 ? (
        <p className="muted-copy">No installed profiles provide mappings yet.</p>
      ) : (
        rows.map(row => {
          const saved = row.userProfileId || '';
          const unavailable = saved && !row.userAvailable;
          const effective = row.alternatives.find(choice => choice.id === row.effectiveProfileId);
          return (
            <article
              className="preference-row"
              key={`${row.mode}:${row.sourceFormat}:${row.targetFormat}`}
            >
              <div className="preference-mapping">
                <span>{row.mode === 'compress' ? 'Compress' : 'Convert'}</span>
                <b>
                  {row.sourceFormat.toUpperCase()} <ArrowRight size={13} /> {row.targetFormat.toUpperCase()}
                </b>
                <small>
                  {effective?.name || 'No compatible profile'} · {row.effectiveSource}
                </small>
              </div>
              <div className="select-wrap">
                <select
                  aria-label={`${row.mode} profile for ${row.sourceFormat} to ${row.targetFormat}`}
                  value={saved}
                  onChange={event => void change(row, event.target.value)}
                >
                  <option value="">Inherit administrator default</option>
                  {unavailable && (
                    <option value={saved} disabled>
                      Unavailable · {saved}
                    </option>
                  )}
                  {row.alternatives.map(choice => (
                    <option key={choice.id} value={choice.id}>
                      {choice.name} · v{choice.version}
                    </option>
                  ))}
                </select>
                <ChevronDown size={15} />
              </div>
            </article>
          );
        })
      )}
    </section>
  );
}
