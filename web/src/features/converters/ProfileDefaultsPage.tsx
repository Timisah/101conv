import { useState } from 'react';
import { ArrowRight, ChevronDown, RefreshCw, Search, Settings2 } from 'lucide-react';
import { Empty, ErrorState, Loading, PageHeading } from '../../components/common';
import { explain, json } from '../format';
import { apiRequest } from '../session';
import { useData } from '../useData';
import type { PreferenceMapping } from './types';

export function ProfileDefaultsPage({ notify }: { notify: (message: string) => void }) {
  const [mode, setMode] = useState<'convert' | 'compress'>('convert');
  const [query, setQuery] = useState('');
  const { data, loading, error, refresh } = useData<{ preferences: PreferenceMapping[] }>(
    '/profile-preferences/global?mode=' + mode,
    { preferences: [] },
  );

  const rows = (data.preferences || []).filter(row =>
    (
      row.sourceFormat +
      ' ' +
      row.targetFormat +
      ' ' +
      (row.alternatives || []).map(alt => alt.name + ' ' + alt.id).join(' ')
    )
      .toLowerCase()
      .includes(query.toLowerCase()),
  );

  async function choose(row: PreferenceMapping, profileId: string) {
    try {
      const path = `/profile-preferences/global/${row.mode}/${encodeURIComponent(row.sourceFormat)}/${encodeURIComponent(row.targetFormat)}`;
      if (profileId) {
        await apiRequest(path, { method: 'PUT', body: json({ profileId }) });
      } else {
        await apiRequest(path, { method: 'DELETE' });
      }
      notify(profileId ? 'Administrator default saved.' : 'Automatic fallback restored.');
      await refresh();
    } catch (err) {
      notify(explain((err as Error).message));
    }
  }

  return (
    <>
      <PageHeading
        eyebrow="CONVERTER SETUP"
        title="Profile defaults"
        description="Choose preferred profiles for exact format mappings. Users can override these defaults in their account preferences."
        action={
          <button className="secondary" onClick={() => void refresh()}>
            <RefreshCw size={15} /> Refresh
          </button>
        }
      />
      <nav className="tabbar" aria-label="Default profile category">
        <button className={mode === 'convert' ? 'active' : ''} onClick={() => setMode('convert')}>
          Conversion defaults
        </button>
        <button className={mode === 'compress' ? 'active' : ''} onClick={() => setMode('compress')}>
          Compression defaults
        </button>
      </nav>
      <section className="admin-panel">
        <div className="toolbar">
          <label className="search-box">
            <Search size={17} />
            <input
              aria-label="Search format mappings"
              placeholder="Search formats or profiles"
              value={query}
              onChange={event => setQuery(event.target.value)}
            />
          </label>
          <span className="result-count">{rows.length} mappings</span>
        </div>
        {error ? (
          <ErrorState message={error} retry={refresh} />
        ) : loading ? (
          <Loading />
        ) : rows.length === 0 ? (
          <Empty
            icon={Settings2}
            title="No compatible mappings"
            detail="Install and enable profiles to configure defaults for their supported formats."
          />
        ) : (
          <div className="preference-table">
            {rows.map(row => {
              const current = row.globalProfileId || '';
              const unavailable = current && !row.globalAvailable;
              return (
                <article
                  className="preference-row"
                  key={`${row.mode}:${row.sourceFormat}:${row.targetFormat}`}
                >
                  <div className="preference-mapping">
                    <span>{mode === 'compress' ? 'Compression' : 'Conversion'}</span>
                    <b>
                      {row.sourceFormat.toUpperCase()} <ArrowRight size={13} /> {row.targetFormat.toUpperCase()}
                    </b>
                    <small>
                      {row.alternatives.length} compatible alternative
                      {row.alternatives.length === 1 ? '' : 's'} · fallback:{' '}
                      {row.alternatives[0]?.name || 'Unavailable'}
                    </small>
                  </div>
                  <div className="select-wrap">
                    <select
                      aria-label={`Default ${mode} profile for ${row.sourceFormat} to ${row.targetFormat}`}
                      value={current}
                      onChange={event => void choose(row, event.target.value)}
                    >
                      <option value="">Automatic fallback</option>
                      {unavailable && (
                        <option value={current} disabled>
                          Unavailable · {current}
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
            })}
          </div>
        )}
      </section>
    </>
  );
}
