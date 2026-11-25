import { useEffect, useMemo, useState } from 'react';
import { NavLink } from 'react-router-dom';
import {
  ArrowRight,
  Boxes,
  Check,
  ChevronDown,
  CircleAlert,
  LoaderCircle,
  Settings2,
  X,
  Zap,
} from 'lucide-react';
import { isMetadataChoice, PreserveMetadataToggle } from '../../components/PreserveMetadataToggle';
import { UploadArea, selectFiles } from '../../components/UploadArea';
import { PageHeading } from '../../components/common';
import { LocalFileThumb } from '../previews/FileThumbnail';
import { SourceAnalysisHint } from '../previews/SourceAnalysisHint';
import {
  inputFormats,
  optionsFor,
  outputFormats,
  profileIdFromProvider,
  providersFor,
  type CapabilityCatalog,
} from '../capabilities';
import {
  automaticProfileLabel,
  ext,
  idempotencyKey,
  supportsMapping,
  type PreferenceMapping,
  type Profile,
} from '../converters/types';
import { bytes, explain, json } from '../format';
import type { Batch } from '../history/types';
import { PresetBar } from '../presets/PresetBar';
import { SubmissionTracker } from '../results/SubmissionTracker';
import { apiRequest, guestRequestHeaders, uploadWithProgress, type Session } from '../session';
import { useData } from '../useData';

const OFFICE_SOURCES = ['docx', 'doc', 'odt', 'xlsx', 'xls', 'ods', 'pptx', 'ppt', 'odp', 'rtf', 'txt'];

export function ConvertPage({
  notify,
  session,
}: {
  notify: (message: string) => void;
  session: Session;
}) {
  const {
    data: profilesResp,
    loading: profilesLoading,
    error: profileError,
    refresh: refreshProfiles,
  } = useData<{ profiles: Profile[] }>('/profiles', { profiles: [] });
  const {
    data: capabilityData,
    loading: capabilitiesLoading,
    error: capabilityError,
    refresh: refreshCapabilities,
  } = useData<CapabilityCatalog>('/capabilities', { schemaVersion: 1, capabilities: [], providers: [] });
  const { data: preferenceData } = useData<{ preferences: PreferenceMapping[] }>(
    '/profile-preferences?mode=convert',
    { preferences: [] },
  );

  const profiles = profilesResp.profiles || [];
  const loading = profilesLoading || capabilitiesLoading;
  const error = profileError || capabilityError;
  const refresh = async () => {
    await Promise.all([refreshCapabilities(), refreshProfiles()]);
  };

  const [files, setFiles] = useState<File[]>([]);
  const [selectionMessage, setSelectionMessage] = useState('');
  const [tracked, setTracked] = useState<string[]>([]);
  const [target, setTarget] = useState('');
  const [selected, setSelected] = useState('');
  const [options, setOptions] = useState<Record<string, string>>({});
  const [hardwareAcceleration, setHardwareAcceleration] = useState('auto');
  const [busy, setBusy] = useState(false);
  const [uploadProgress, setUploadProgress] = useState<{
    index: number;
    total: number;
    name: string;
    percent: number;
  } | null>(null);

  const accessLocked = !session.authenticated;
  const fileLimit = session.guest ? session.guestMaxBatchFiles || 5 : 50;

  const addFiles = (incoming: File[]) => {
    if (busy || !session.authenticated) return;
    const result = selectFiles(files, incoming, fileLimit);
    setFiles(result.files);
    setSelectionMessage(result.message);
  };

  const sources = useMemo(() => files.map(file => ext(file.name)), [files]);
  const canonicalProviders = useMemo(() => providersFor(capabilityData, 'convert'), [capabilityData]);
  const available = useMemo(
    () =>
      profiles.filter(
        item =>
          item.definition.type !== 'compression' &&
          item.state.enabled &&
          canonicalProviders.some(provider => provider.profileId === item.definition.id),
      ),
    [profiles, canonicalProviders],
  );
  const compatible = useMemo(
    () =>
      available.filter(
        item => sources.length && target && sources.every(source => supportsMapping(item, source, target)),
      ),
    [available, sources, target],
  );
  const targets = useMemo(() => outputFormats(capabilityData, 'convert', sources), [capabilityData, sources]);
  const profile = compatible.find(item => item.definition.id === selected);

  const profileOptions = useMemo(() => {
    if (!profile || !target) return [];
    const rules = Array.from(new Set(sources)).map(source =>
      profile.definition.rules.find(
        rule =>
          rule.sources.some(item => item.toLowerCase().replace(/^\./, '') === source) &&
          rule.targets.some(item => item.toLowerCase().replace(/^\./, '') === target),
      ),
    );
    if (!rules.length || rules.some(rule => JSON.stringify(rule?.options || []) !== JSON.stringify(rules[0]?.options || []))) {
      return [];
    }
    return rules[0]?.options || [];
  }, [profile, sources, target]);

  const canonicalSelection = useMemo(
    () => optionsFor(capabilityData, 'convert', sources, target, selected),
    [capabilityData, sources, target, selected],
  );
  const visibleOptions = profileOptions.length ? profileOptions : canonicalSelection.options;
  const effectiveAutomaticProfile = canonicalSelection.provider?.profileId;

  useEffect(() => {
    if (selected && !profile) setSelected('');
  }, [selected, profile?.definition.id]);

  useEffect(() => {
    if (target && !targets.includes(target)) setTarget('');
  }, [target, targets.join(',')]);

  useEffect(() => {
    if (!selected) {
      const defaults = Object.fromEntries(visibleOptions.map(option => [option.key, option.default]));
      setOptions(current =>
        Object.keys(current).every(key => key in defaults) ? { ...defaults, ...current } : defaults,
      );
    }
  }, [selected, canonicalSelection.provider?.id, JSON.stringify(visibleOptions)]);

  const effective = Array.from(new Set(sources)).map(source => ({
    source,
    row: preferenceData.preferences.find(row => row.sourceFormat === source && row.targetFormat === target),
  }));

  async function submit() {
    if ((selected && !profile) || !target || !files.length) return;
    setBusy(true);
    const selection = [...files];
    const items: {
      uploadId: string;
      targetFormat: string;
      profileId?: string;
      options?: Record<string, string>;
      hardwareAcceleration?: string;
    }[] = [];
    const failures: string[] = [];

    for (let index = 0; index < selection.length; index++) {
      const file = selection[index];
      setUploadProgress({ index: index + 1, total: selection.length, name: file.name, percent: 0 });
      try {
        const upload = await uploadWithProgress<{ id: string }>(
          file,
          percent => setUploadProgress({ index: index + 1, total: selection.length, name: file.name, percent }),
          session.guest,
        );
        const item: {
          uploadId: string;
          targetFormat: string;
          profileId?: string;
          options?: Record<string, string>;
          hardwareAcceleration?: string;
        } = { uploadId: upload.id, targetFormat: target };
        if (profile) {
          item.profileId = profile.definition.id;
          if (visibleOptions.length) item.options = options;
        } else if (visibleOptions.length && effectiveAutomaticProfile) {
          item.profileId = effectiveAutomaticProfile;
          item.options = options;
        }
        if (['mp4', 'mkv', 'webm', 'mov', 'avi'].includes(target.toLowerCase()) && hardwareAcceleration !== 'auto') {
          item.hardwareAcceleration = hardwareAcceleration;
        }
        items.push(item);
      } catch (err) {
        failures.push(file.name + ': ' + explain((err as Error).message));
      }
    }

    let created: Batch | undefined;
    try {
      if (items.length) {
        const response = await apiRequest<{ batch: Batch }>('/batches', {
          method: 'POST',
          body: json({ mode: 'convert', idempotencyKey: idempotencyKey(), items }),
          headers: guestRequestHeaders(session.guest === true),
        });
        created = response.batch;
        const batchIDs = JSON.parse(localStorage.getItem('101conv-batch-ids') || '[]') as string[];
        batchIDs.push(created.id);
        localStorage.setItem('101conv-batch-ids', JSON.stringify(batchIDs.slice(-100)));
        const jobIDs = JSON.parse(localStorage.getItem('101conv-job-ids') || '[]') as string[];
        for (const item of created.items) {
          if (item.jobId) jobIDs.push(item.jobId);
        }
        localStorage.setItem('101conv-job-ids', JSON.stringify(jobIDs.slice(-100)));
      }
    } catch (err) {
      failures.push(explain((err as Error).message));
    }

    setBusy(false);
    setUploadProgress(null);
    if (created) {
      setFiles([]);
      setTracked(current => [created!.id, ...current].slice(0, 3));
      setTarget('');
      const details = failures.length
        ? ' ' + failures.join('; ')
        : created.failed
          ? ` ${created.failed} item(s) need attention in Jobs & history.`
          : '';
      notify(`Batch created: ${created.succeeded + created.queued + created.running} item(s) submitted.${details}`);
    } else if (failures.length) {
      notify(failures.join('; '));
    }
  }

  async function saveMyDefault() {
    if (!profile || !target) return;
    try {
      for (const source of Array.from(new Set(sources))) {
        await apiRequest(
          `/profile-preferences/me/convert/${encodeURIComponent(source)}/${encodeURIComponent(target)}`,
          { method: 'PUT', body: json({ profileId: profile.definition.id }) },
        );
      }
      notify('Saved as your default for these format mappings.');
    } catch (err) {
      notify(explain((err as Error).message));
    }
  }

  return (
    <>
      <PageHeading
        title="What are we converting?"
        description="Drop a file, choose a format, and let your converters do the work."
      />
      {tracked.map(id => (
        <SubmissionTracker
          key={id}
          batchId={id}
          notify={notify}
          onDismiss={() => setTracked(current => current.filter(item => item !== id))}
        />
      ))}
      {error && (
        <div className="banner error">
          <CircleAlert size={18} />
          <div>
            <b>Couldn’t load conversion capabilities</b>
            <span>{error}</span>
          </div>
          <button className="text-btn" onClick={refresh}>
            Retry
          </button>
        </div>
      )}
      {!loading && !error && !canonicalProviders.length && (
        <div className="banner setup-notice">
          <Boxes size={20} />
          <div>
            <b>No conversion capabilities available</b>
            <span>
              {session.role === 'admin'
                ? 'Add a profile from the repository catalog to get started.'
                : 'Ask your administrator to enable a compatible profile.'}
            </span>
          </div>
          {session.role === 'admin' && (
            <NavLink className="secondary" to="/marketplace">
              Browse catalog <ArrowRight size={15} />
            </NavLink>
          )}
        </div>
      )}
      <div className="convert-layout">
        <div className="convert-main">
          <UploadArea
            mode="convert"
            files={files.length}
            onFiles={addFiles}
            limit={fileLimit}
            disabled={busy || !session.authenticated}
            formats={inputFormats(capabilityData, 'convert')}
            message={selectionMessage}
            onDismiss={() => setSelectionMessage('')}
          />
          {target === 'pdf' && sources.some(source => OFFICE_SOURCES.includes(source)) && (
            <p className="muted-copy" role="note">
              Office documents are rendered with LibreOffice. Fonts may be substituted; review complex layouts.
              Password-protected files are not supported.
            </p>
          )}
          {sources.includes('pdf') && (
            <p className="muted-copy" role="note">
              PDF images contain one selected page. Text extraction requires selectable text; OCR and encrypted PDFs
              are not supported. Choose a PDF profile to set page and layout options.
            </p>
          )}
          {uploadProgress && (
            <div className="upload-progress" role="status" aria-live="polite">
              <div>
                <span>
                  Uploading {uploadProgress.index} of {uploadProgress.total}
                </span>
                <b>{uploadProgress.percent}%</b>
              </div>
              <div
                className="upload-progress-track"
                role="progressbar"
                aria-label={'Uploading ' + uploadProgress.name}
                aria-valuemin={0}
                aria-valuemax={100}
                aria-valuenow={uploadProgress.percent}
              >
                <i style={{ width: uploadProgress.percent + '%' }} />
              </div>
              <small title={uploadProgress.name}>{uploadProgress.name}</small>
            </div>
          )}
          {files.length > 0 && (
            <section className="file-list-panel">
              <div className="section-row">
                <div>
                  <h3>
                    Ready to convert <span className="count-badge">{files.length}</span>
                  </h3>
                  <p>
                    All files use {target ? target.toUpperCase() : 'the selected output format'} ·{' '}
                    {bytes(files.reduce((sum, file) => sum + file.size, 0))} total.
                  </p>
                </div>
                <button
                  className="text-btn"
                  disabled={busy}
                  onClick={() => {
                    setFiles([]);
                    setSelectionMessage('');
                  }}
                >
                  Clear all
                </button>
              </div>
              {files.map((file, index) => {
                const sourceExt = ext(file.name);
                const prefRow = preferenceData.preferences.find(
                  row => row.sourceFormat === sourceExt && row.targetFormat === target,
                );
                const prefName = prefRow?.alternatives.find(alt => alt.id === prefRow.effectiveProfileId)?.name;
                return (
                  <div className="file-row" key={index}>
                    <LocalFileThumb file={file} />
                    <div className="file-info">
                      <b title={file.name}>{file.name}</b>
                      <small>
                        {bytes(file.size)} · .{sourceExt}
                      </small>
                      {target && (
                        <small className="batch-file-profile">
                          {profile?.definition.name || prefName || 'Automatic profile per format'}
                        </small>
                      )}
                    </div>
                    <button
                      className="icon-btn quiet"
                      disabled={busy}
                      onClick={() => setFiles(prev => prev.filter((_, itemIndex) => index !== itemIndex))}
                      aria-label={'Remove ' + file.name}
                    >
                      <X size={16} />
                    </button>
                  </div>
                );
              })}
              <SourceAnalysisHint files={files} disabled={busy || session.guest} />
            </section>
          )}
        </div>

        <aside className="conversion-options">
          <div className="options-heading">
            <span className="options-icon">
              <Settings2 size={18} />
            </span>
            <div>
              <h3>Conversion setup</h3>
            </div>
          </div>
          <label className="field-label" htmlFor="output-format">
            Convert to
          </label>
          <div className="select-wrap">
            <select
              id="output-format"
              value={target}
              onChange={event => {
                setTarget(event.target.value);
                setOptions({});
              }}
              disabled={accessLocked || busy || !files.length || !targets.length}
            >
              <option value="">
                {!files.length
                  ? 'Add files first'
                  : targets.length
                    ? 'Choose a format'
                    : 'No compatible format'}
              </option>
              {targets.map(format => (
                <option key={format} value={format}>
                  {format.toUpperCase()}
                </option>
              ))}
            </select>
            <ChevronDown size={16} />
          </div>

          <PresetBar
            mode="convert"
            session={session}
            sources={sources}
            target={target}
            selectedProfile={profile || null}
            selectedProviderId={canonicalSelection.provider?.id}
            options={options}
            notify={notify}
            disabled={accessLocked || busy || !files.length || !targets.length}
            onApply={preset => {
              setTarget(preset.targetFormat);
              setSelected(preset.profileId || profileIdFromProvider(preset.providerId));
              setOptions(preset.options || {});
            }}
          />

          <details className="advanced-provider">
            <summary>Advanced provider override (optional)</summary>
            <label className="field-label" htmlFor="profile-select">
              Profile for this job
            </label>
            <div className="select-wrap">
              <select
                id="profile-select"
                value={selected}
                onChange={event => {
                  setSelected(event.target.value);
                  setOptions({});
                }}
                disabled={accessLocked || busy || !files.length || !targets.length}
              >
                <option value="">{automaticProfileLabel(sources, target, preferenceData.preferences)}</option>
                {compatible.map(item => (
                  <option key={item.definition.id} value={item.definition.id}>
                    {item.definition.name} · v{item.definition.version}
                  </option>
                ))}
              </select>
              <ChevronDown size={16} />
            </div>
            <small className="field-hint">Only needed to force a specific installed implementation.</small>
            {['mp4', 'mkv', 'webm', 'mov', 'avi'].includes(target.toLowerCase()) && (
              <>
                <label className="field-label" htmlFor="convert-hw-accel">
                  Hardware video acceleration
                </label>
                <div className="select-wrap">
                  <select
                    id="convert-hw-accel"
                    value={hardwareAcceleration}
                    onChange={event => setHardwareAcceleration(event.target.value)}
                    disabled={accessLocked || busy}
                  >
                    <option value="auto">Automatic (use verified GPU when compatible, else CPU)</option>
                    <option value="cpu">CPU only (deterministic software encoder)</option>
                    <option value="prefer_hardware">Prefer hardware (fall back to CPU if unsupported)</option>
                    <option value="require_hardware">Require hardware (fail if no compatible GPU)</option>
                  </select>
                  <ChevronDown size={16} />
                </div>
                <small className="field-hint">
                  Hardware encoding is opt-in and faster on supported GPUs, though quality can differ from CPU. HDR-preserving jobs automatically use CPU unless hardware HDR is verified.
                </small>
              </>
            )}
          </details>

          {!files.length && <p className="field-hint">Add files to see compatible profiles and formats.</p>}
          {files.length > 0 && targets.length === 0 && (
            <div className="notice">
              <CircleAlert size={16} />
              <span>
                No available conversion capability supports a common output format for{' '}
                {Array.from(new Set(sources))
                  .map(item => '.' + item)
                  .join(', ')}
                . Browse the marketplace to add one.
              </span>
            </div>
          )}
          {files.length > 0 && !profile && target && (
            <div className="preference-summary">
              {effective.map(({ source, row }) => (
                <div key={source}>
                  <span>
                    {source.toUpperCase()} → {target.toUpperCase()}
                  </span>
                  <b>
                    {row?.alternatives.find(item => item.id === row.effectiveProfileId)?.name ||
                      'Automatic compatible profile'}
                  </b>
                  <small>
                    {row?.effectiveSource || 'Automatic fallback'}
                    {row?.effectiveProfileId &&
                    row.effectiveProfileId === row.userProfileId &&
                    !row.userAvailable
                      ? ' · saved choice unavailable; using a compatible profile'
                      : ''}
                  </small>
                </div>
              ))}
            </div>
          )}
          {profile && (
            <div className="profile-summary">
              <span className="summary-check">
                <Check size={13} />
              </span>
              <div>
                <b>{profile.definition.name}</b>
                <small>Selected for this job · v{profile.definition.version}</small>
              </div>
            </div>
          )}
          {!profile && canonicalSelection.provider && (
            <div className="profile-summary">
              <span className="summary-check">
                <Check size={13} />
              </span>
              <div>
                <b>{canonicalSelection.provider.name}</b>
                <small>Automatically selected compatible provider</small>
              </div>
            </div>
          )}

          {visibleOptions.map(option =>
            isMetadataChoice(option) ? (
              <PreserveMetadataToggle
                key={option.key}
                id={'conversion-option-' + option.key}
                value={options[option.key] ?? option.default}
                disabled={busy}
                onChange={value => setOptions(current => ({ ...current, [option.key]: value }))}
              />
            ) : (
              <label className="field-label" key={option.key} htmlFor={'conversion-option-' + option.key}>
                {option.label}
                {option.type === 'select' ? (
                  <div className="select-wrap">
                    <select
                      id={'conversion-option-' + option.key}
                      value={options[option.key] ?? option.default}
                      disabled={busy}
                      onChange={event =>
                        setOptions(current => ({ ...current, [option.key]: event.target.value }))
                      }
                    >
                      {option.choices?.map(choice => (
                        <option key={choice} value={choice}>
                          {choice}
                        </option>
                      ))}
                    </select>
                    <ChevronDown size={16} />
                  </div>
                ) : option.type === 'boolean' ? (
                  <input
                    id={'conversion-option-' + option.key}
                    type="checkbox"
                    checked={(options[option.key] ?? option.default) === 'true'}
                    disabled={busy}
                    onChange={event =>
                      setOptions(current => ({ ...current, [option.key]: String(event.target.checked) }))
                    }
                  />
                ) : (
                  <>
                    <input
                      className="compression-number"
                      id={'conversion-option-' + option.key}
                      type="number"
                      min={option.min}
                      max={option.max}
                      step={option.step || 1}
                      value={options[option.key] ?? option.default}
                      disabled={busy}
                      onChange={event =>
                        setOptions(current => ({ ...current, [option.key]: event.target.value }))
                      }
                    />
                    <small className="field-hint">
                      Allowed range: {option.min}–{option.max}.
                    </small>
                  </>
                )}
              </label>
            ),
          )}

          {profile && target && !session.guest && (
            <button className="text-btn preference-save" onClick={() => void saveMyDefault()}>
              <Check size={14} /> Set as my default
            </button>
          )}
          <button
            className="primary submit-button"
            disabled={accessLocked || busy || !files.length || !target || (Boolean(selected) && !profile)}
            onClick={() => void submit()}
          >
            {busy ? (
              <>
                <LoaderCircle className="spin" /> Uploading…
              </>
            ) : (
              <>
                <Zap size={16} /> Convert {files.length || ''}{' '}
                {files.length === 1 ? 'file' : files.length ? 'files' : 'files'}
                <ArrowRight size={16} />
              </>
            )}
          </button>
        </aside>
      </div>
    </>
  );
}
