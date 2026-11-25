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
import { CompressionOptionField } from '../../components/CompressionOptionField';
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
  unavailableCapabilityMessage,
  withoutBlankOptionalValues,
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

export function CompressPage({
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
    '/profile-preferences?mode=compress',
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
  const [combinedTargetSizeMb, setCombinedTargetSizeMb] = useState('');
  const [busy, setBusy] = useState(false);
  const [uploadProgress, setUploadProgress] = useState<{
    index: number;
    total: number;
    name: string;
    percent: number;
  } | null>(null);
  const [options, setOptions] = useState<Record<string, string>>({});
  const [hardwareAcceleration, setHardwareAcceleration] = useState('auto');

  const fileLimit = session.guest ? session.guestMaxBatchFiles || 5 : 50;

  const addFiles = (incoming: File[]) => {
    if (busy || !session.authenticated) return;
    const result = selectFiles(files, incoming, fileLimit);
    setFiles(result.files);
    setSelectionMessage(result.message);
  };

  const sources = useMemo(() => files.map(file => ext(file.name)), [files]);
  const canonicalProviders = useMemo(() => providersFor(capabilityData, 'compress'), [capabilityData]);
  const available = useMemo(
    () =>
      profiles.filter(
        item =>
          item.definition.type !== 'conversion' &&
          item.state.enabled &&
          canonicalProviders.some(provider => provider.profileId === item.definition.id),
      ),
    [profiles, canonicalProviders],
  );
  const compatible = useMemo(
    () =>
      available.filter(
        item => sources.length && target && sources.every(source => supportsMapping(item, source, target, true)),
      ),
    [available, sources, target],
  );
  const targets = useMemo(() => outputFormats(capabilityData, 'compress', sources), [capabilityData, sources]);
  const profile = compatible.find(item => item.definition.id === selected);
  const canonicalSelection = useMemo(
    () => optionsFor(capabilityData, 'compress', sources, target, selected),
    [capabilityData, sources, target, selected],
  );
  const visibleOptions = canonicalSelection.options;
  const effectiveAutomaticProfile = canonicalSelection.provider?.profileId;

  useEffect(() => {
    if (selected && !profile) setSelected('');
  }, [selected, profile?.definition.id]);

  useEffect(() => {
    if (!target || !targets.includes(target)) {
      const same = sources.length && sources.every(source => source === sources[0]) ? sources[0] : '';
      setTarget(same && targets.includes(same) ? same : '');
    }
  }, [targets.join(','), sources.join(','), target]);

  useEffect(() => {
    const defaults = Object.fromEntries(visibleOptions.map(option => [option.key, option.default]));
    setOptions(current =>
      Object.keys(current).every(key => key in defaults) ? { ...defaults, ...current } : defaults,
    );
    setCombinedTargetSizeMb('');
  }, [canonicalSelection.provider?.id, JSON.stringify(visibleOptions)]);

  useEffect(() => {
    if (files.length < 2) setCombinedTargetSizeMb('');
  }, [files.length]);

  const combinedTargetAvailable = files.length > 1 && Boolean(visibleOptions.some(option => option.targetSizeMb));
  const submitOptions = useMemo(
    () => withoutBlankOptionalValues(options, visibleOptions),
    [options, visibleOptions],
  );

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
        items.push({
          uploadId: upload.id,
          targetFormat: target,
          ...(profile
            ? { profileId: profile.definition.id, options: submitOptions }
            : { profileId: effectiveAutomaticProfile, options: submitOptions }),
          ...(['mp4', 'mkv', 'webm', 'mov', 'avi'].includes(target.toLowerCase()) && hardwareAcceleration !== 'auto'
            ? { hardwareAcceleration }
            : {}),
        });
      } catch (err) {
        failures.push(file.name + ': ' + explain((err as Error).message));
      }
    }

    let created: Batch | undefined;
    try {
      if (items.length) {
        const response = await apiRequest<{ batch: Batch }>('/batches', {
          method: 'POST',
          body: json({
            mode: 'compress',
            idempotencyKey: idempotencyKey(),
            ...(combinedTargetSizeMb ? { combinedTargetSizeMb: Number(combinedTargetSizeMb) } : {}),
            items,
          }),
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
      setCombinedTargetSizeMb('');
      setTracked(current => [created!.id, ...current].slice(0, 3));
      const details = failures.length
        ? ' ' + failures.join('; ')
        : created.failed
          ? ` ${created.failed} item(s) need attention in Jobs & history.`
          : '';
      notify(`Compression batch created: ${created.succeeded + created.queued + created.running} item(s) submitted.${details}`);
    } else if (failures.length) {
      notify(failures.join('; '));
    }
  }

  async function saveMyDefault() {
    if (!profile || !target) return;
    try {
      for (const source of Array.from(new Set(sources))) {
        await apiRequest(
          `/profile-preferences/me/compress/${encodeURIComponent(source)}/${encodeURIComponent(target)}`,
          { method: 'PUT', body: json({ profileId: profile.definition.id }) },
        );
      }
      notify('Saved as your compression default for these format mappings.');
    } catch (err) {
      notify(explain((err as Error).message));
    }
  }

  return (
    <>
      <PageHeading
        title="Make files smaller"
        description="Keep the current format by default, optionally cap the output size, and choose another supported format when you need to."
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
            <b>Couldn’t load compression capabilities</b>
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
            <b>No compression capabilities available</b>
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
            mode="compress"
            files={files.length}
            onFiles={addFiles}
            limit={fileLimit}
            disabled={busy || !session.authenticated}
            formats={inputFormats(capabilityData, 'compress')}
            message={selectionMessage}
            onDismiss={() => setSelectionMessage('')}
          />
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
              <small>{uploadProgress.name}</small>
            </div>
          )}
          {files.length > 0 && (
            <section className="file-list-panel">
              <div className="section-row">
                <div>
                  <h3>
                    Ready to compress <span className="count-badge">{files.length}</span>
                  </h3>
                  <p>
                    Output format applies to the batch · {bytes(files.reduce((sum, file) => sum + file.size, 0))}{' '}
                    total.
                  </p>
                </div>
                <button
                  className="text-btn"
                  disabled={busy}
                  onClick={() => {
                    setFiles([]);
                    setCombinedTargetSizeMb('');
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
                      onClick={() => setFiles(prev => prev.filter((_, itemIndex) => itemIndex !== index))}
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
              <h3>Compression setup</h3>
            </div>
          </div>
          <label className="field-label" htmlFor="compress-format">
            Output format
          </label>
          <div className="select-wrap">
            <select
              id="compress-format"
              value={target}
              onChange={event => {
                setTarget(event.target.value);
                setOptions({});
              }}
              disabled={busy || !files.length || !targets.length}
            >
              <option value="">
                {!files.length
                  ? 'Add files first'
                  : targets.length
                    ? 'Choose an output format'
                    : 'No compression format supported'}
              </option>
              {targets.map(format => (
                <option key={format} value={format}>
                  {format.toUpperCase()}
                  {sources.length && sources.every(source => source === format) ? ' · original format' : ''}
                </option>
              ))}
            </select>
            <ChevronDown size={16} />
          </div>

          <PresetBar
            mode="compress"
            session={session}
            sources={sources}
            target={target}
            selectedProfile={profile || null}
            selectedProviderId={canonicalSelection.provider?.id}
            options={submitOptions}
            notify={notify}
            disabled={busy || !files.length || !targets.length}
            onApply={preset => {
              setTarget(preset.targetFormat);
              setSelected(preset.profileId || profileIdFromProvider(preset.providerId));
              setOptions(preset.options || {});
            }}
          />

          <details className="advanced-provider">
            <summary>Advanced provider override (optional)</summary>
            <label className="field-label" htmlFor="compress-profile">
              Profile for this job
            </label>
            <div className="select-wrap">
              <select
                id="compress-profile"
                value={selected}
                onChange={event => {
                  setSelected(event.target.value);
                  setOptions({});
                }}
                disabled={busy || !files.length || !targets.length}
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
                <label className="field-label" htmlFor="compress-hw-accel">
                  Hardware video acceleration
                </label>
                <div className="select-wrap">
                  <select
                    id="compress-hw-accel"
                    value={hardwareAcceleration}
                    onChange={event => setHardwareAcceleration(event.target.value)}
                    disabled={busy}
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

          {visibleOptions.map(option => (
            <CompressionOptionField
              key={option.key}
              option={option}
              value={options[option.key]}
              disabled={busy}
              onChange={value => setOptions(current => ({ ...current, [option.key]: value }))}
            />
          ))}

          {combinedTargetAvailable && (
            <label className="field-label" htmlFor="compression-combined-target">
              Combined maximum (MB)
              <input
                className="compression-number"
                id="compression-combined-target"
                type="number"
                min={1}
                max={20000}
                step={1}
                value={combinedTargetSizeMb}
                disabled={busy}
                onChange={event => setCombinedTargetSizeMb(event.target.value)}
              />
              <small className="field-hint">
                Distributed proportionally from the uploaded file sizes. Per-file profile limits still apply, so the
                actual total may be lower.
              </small>
            </label>
          )}

          {!profile && files.length > 0 && target && (
            <div className="preference-summary">
              {Array.from(new Set(sources)).map(source => {
                const row = preferenceData.preferences.find(
                  item => item.sourceFormat === source && item.targetFormat === target,
                );
                const choice = row?.alternatives.find(item => item.id === row.effectiveProfileId);
                return (
                  <div key={source}>
                    <span>
                      {source.toUpperCase()} → {target.toUpperCase()}
                    </span>
                    <b>{choice?.name || 'Automatic compatible profile'}</b>
                    <small>{row?.effectiveSource || 'Automatic fallback'}</small>
                  </div>
                );
              })}
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

          {profile && target && !session.guest && (
            <button className="text-btn preference-save" onClick={() => void saveMyDefault()}>
              <Check size={14} /> Set as my default
            </button>
          )}

          {files.length > 0 && !targets.length && (
            <div className="notice">
              <CircleAlert size={16} />
              <span>
                {unavailableCapabilityMessage(capabilityData, 'compress', sources)}{' '}
                {session.role === 'admin' && (
                  <NavLink className="text-link" to="/marketplace">
                    Open the catalog <ArrowRight size={14} />
                  </NavLink>
                )}
              </span>
            </div>
          )}

          <button
            className="primary submit-button"
            disabled={busy || !files.length || !target || (Boolean(selected) && !profile)}
            onClick={() => void submit()}
          >
            {busy ? (
              <>
                <LoaderCircle className="spin" /> Uploading…
              </>
            ) : (
              <>
                <Zap size={16} /> Compress {files.length || ''} {files.length === 1 ? 'file' : 'files'}
                <ArrowRight size={16} />
              </>
            )}
          </button>
        </aside>
      </div>
    </>
  );
}
