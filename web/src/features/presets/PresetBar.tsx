import { useEffect, useState } from 'react';
import { ChevronDown, Plus, Trash2 } from 'lucide-react';
import type { Preset, Profile } from '../converters/types';
import { explain, json } from '../format';
import { apiRequest, type Session } from '../session';
import { useData } from '../useData';

export type PresetBarProps = {
  mode: 'convert' | 'compress';
  session: Session;
  sources: string[];
  target: string;
  selectedProfile: Profile | null;
  selectedProviderId?: string;
  options: Record<string, string>;
  onApply: (preset: Preset) => void;
  notify: (message: string) => void;
  disabled?: boolean;
};

export function PresetBar({
  mode,
  session,
  sources,
  target,
  selectedProfile,
  selectedProviderId,
  options,
  onApply,
  notify,
  disabled,
}: PresetBarProps) {
  const { data, loading, error, refresh } = useData<{ presets: Preset[] }>('/presets?mode=' + mode, {
    presets: [],
  });
  const [selected, setSelected] = useState('');
  const [share, setShare] = useState(false);

  const presets = data.presets || [];
  const current = presets.find(preset => preset.id === selected);

  useEffect(() => {
    if (selected && (!sources.length || !current || !sources.every(source => source === current.sourceFormat))) {
      setSelected('');
    }
  }, [selected, current?.id, current?.sourceFormat, sources.join(',')]);

  const apply = (id: string) => {
    const preset = presets.find(item => item.id === id);
    if (!preset) return;
    if (!preset.compatible) {
      setSelected(id);
      notify(preset.compatibilityMessage || 'This preset is incompatible with the installed capabilities.');
      return;
    }
    if (!sources.length || !sources.every(source => source === preset.sourceFormat)) {
      notify(`This preset is for .${preset.sourceFormat} inputs. Choose matching files first.`);
      return;
    }
    setSelected(id);
    onApply(preset);
  };

  async function save() {
    if (session.guest || !target || sources.length === 0 || new Set(sources).size !== 1) {
      notify('Choose one source format before saving a preset.');
      return;
    }
    const name = window.prompt('Preset name');
    if (!name?.trim()) return;
    try {
      await apiRequest('/presets', {
        method: 'POST',
        body: json({
          name: name.trim(),
          mode,
          sourceFormat: sources[0],
          targetFormat: target,
          providerId: selectedProviderId || '',
          profileId: selectedProfile?.definition.id || '',
          profileVersion: selectedProfile?.definition.version || '',
          options,
          shared: session.role === 'admin' && share,
          default: false,
        }),
      });
      notify(share ? 'Shared preset saved.' : 'Preset saved.');
      setShare(false);
      await refresh();
    } catch (err) {
      notify(explain((err as Error).message));
    }
  }

  async function edit() {
    if (!current || session.guest) return;
    try {
      await apiRequest('/presets/' + current.id, {
        method: 'PUT',
        body: json({
          name: current.name,
          description: current.description || '',
          mode,
          sourceFormat: sources[0] || current.sourceFormat,
          targetFormat: target || current.targetFormat,
          providerId: selectedProviderId || current.providerId || '',
          profileId: selectedProfile?.definition.id || current.profileId || '',
          profileVersion: selectedProfile?.definition.version || current.profileVersion || '',
          options,
        }),
      });
      notify('Preset updated.');
      await refresh();
    } catch (err) {
      notify(explain((err as Error).message));
    }
  }

  async function duplicate() {
    if (!current || session.guest) return;
    const name = window.prompt('Name for the duplicate', current.name + ' copy');
    if (!name?.trim()) return;
    try {
      await apiRequest('/presets/' + current.id + '/duplicate', {
        method: 'POST',
        body: json({ name: name.trim() }),
      });
      notify('Preset duplicated.');
      await refresh();
    } catch (err) {
      notify(explain((err as Error).message));
    }
  }

  async function remove() {
    if (!current || session.guest || !window.confirm('Delete preset “' + current.name + '”?')) return;
    try {
      await apiRequest('/presets/' + current.id, { method: 'DELETE' });
      setSelected('');
      notify('Preset deleted.');
      await refresh();
    } catch (err) {
      notify(explain((err as Error).message));
    }
  }

  async function setDefault() {
    if (!current || session.guest) return;
    try {
      await apiRequest('/presets/' + current.id + '/default', {
        method: 'PATCH',
        body: json({ enabled: !current.default }),
      });
      notify(current.default ? 'Preset removed as the default.' : 'Preset set as the default.');
      await refresh();
    } catch (err) {
      notify(explain((err as Error).message));
    }
  }

  return (
    <div className="preset-bar">
      <label className="field-label" htmlFor={mode + '-preset'}>
        Saved preset
      </label>
      <div className="select-wrap">
        <select
          id={mode + '-preset'}
          aria-label="Saved presets"
          value={selected}
          onChange={event => apply(event.target.value)}
          disabled={disabled || loading || !presets.length}
        >
          <option value="">
            {loading ? 'Loading presets…' : presets.length ? 'Choose a preset' : 'No saved presets'}
          </option>
          {presets.map(preset => (
            <option key={preset.id} value={preset.id}>
              {preset.name}
              {preset.default ? ' · default' : ''}
              {!preset.compatible ? ' · unavailable' : ''}
            </option>
          ))}
        </select>
        <ChevronDown size={16} />
      </div>
      {error && <small className="field-hint">Saved presets unavailable: {error}</small>}
      {current && !current.compatible && (
        <small className="form-error">
          {current.compatibilityMessage || 'This preset is incompatible with the installed capabilities.'}
        </small>
      )}
      {!session.guest && (
        <div className="preset-actions">
          <button
            className="text-btn"
            type="button"
            disabled={disabled || !target || !sources.length || new Set(sources).size !== 1}
            onClick={() => void save()}
          >
            <Plus size={14} /> Save current setup
          </button>
          {session.role === 'admin' && (
            <label className="preset-share">
              <input
                type="checkbox"
                checked={share}
                onChange={event => setShare(event.target.checked)}
              />{' '}
              Share
            </label>
          )}
          {current && (!current.shared || session.role === 'admin') && (
            <>
              <button className="text-btn" type="button" onClick={() => void edit()}>
                Update
              </button>
              <button className="text-btn" type="button" onClick={() => void duplicate()}>
                Duplicate
              </button>
              <button className="text-btn" type="button" onClick={() => void setDefault()}>
                {current.default ? 'Clear default' : 'Set default'}
              </button>
              <button className="text-btn danger-btn" type="button" onClick={() => void remove()}>
                <Trash2 size={14} /> Delete
              </button>
            </>
          )}
          {current && current.shared && session.role !== 'admin' && (
            <button className="text-btn" type="button" onClick={() => void duplicate()}>
              Duplicate
            </button>
          )}
        </div>
      )}
    </div>
  );
}
