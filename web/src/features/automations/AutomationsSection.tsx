import { useState, type FormEvent } from 'react';
import { Eye, Pause, Play, Trash2, Workflow } from 'lucide-react';
import { ErrorState, Loading } from '../../components/common';
import type { Preset, Profile } from '../converters/types';
import { bytes, json } from '../format';
import { apiRequest } from '../session';
import { ExtensionReleases } from './ExtensionReleases';
import {
  readCondition,
  type AutomationCondition,
  type AutomationDryRunPreview,
  type AutomationGraphNode,
  type AutomationRunView,
  type AutomationView,
  type BlueprintView,
  type DriveView,
  type ExtensionView,
} from './types';

export function AutomationsSection({
  drives,
  automations,
  automationsLoading,
  automationsError,
  onRefreshAutomations,
  blueprints,
  profiles,
  presets,
  extensions,
  extensionsError,
  onRefreshExtensionsAndAutomations,
  notify,
}: {
  drives: DriveView[];
  automations: AutomationView[];
  automationsLoading: boolean;
  automationsError: string;
  onRefreshAutomations: () => Promise<void>;
  blueprints: BlueprintView[];
  profiles: Profile[];
  presets: Preset[];
  extensions: ExtensionView[];
  extensionsError: string;
  onRefreshExtensionsAndAutomations: () => Promise<void>;
  notify: (message: string) => void;
}) {
  const [blueprintID, setBlueprintID] = useState('automatic-folder-conversion');
  const [name, setName] = useState('');
  const [inputID, setInputID] = useState('');
  const [outputID, setOutputID] = useState('');
  const [profileID, setProfileID] = useState('');
  const [presetID, setPresetID] = useState('');
  const [target, setTarget] = useState('');
  const [actionOptions, setActionOptions] = useState<Record<string, string>>({});
  const [extensionsInput, setExtensionsInput] = useState('');
  const [nameGlob, setNameGlob] = useState('');
  const [minMB, setMinMB] = useState('');
  const [maxMB, setMaxMB] = useState('');
  const [conditionLogic, setConditionLogic] = useState<'and' | 'or' | 'not'>('and');
  const [interval, setIntervalValue] = useState('60');
  const [stability, setStability] = useState('30');
  const [maxConcurrent, setMaxConcurrent] = useState('1');
  const [processExisting, setProcessExisting] = useState(false);
  const [behavior, setBehavior] = useState('unique');
  const [editing, setEditing] = useState<AutomationView | null>(null);
  const [preview, setPreview] = useState<AutomationDryRunPreview | null>(null);
  const [history, setHistory] = useState<AutomationRunView[]>([]);
  const [busy, setBusy] = useState(false);
  const [editorMode, setEditorMode] = useState<'visual' | 'details'>('visual');

  const blueprint = blueprints.find(item => item.id === blueprintID);
  const profileChoice = profiles.find(item => item.definition.id === profileID);
  const targets = Array.from(
    new Set(
      (profileChoice?.definition.rules || []).flatMap(rule =>
        rule.targets.map(item => item.replace(/^\./, '').toLowerCase()),
      ),
    ),
  );

  function loadEditorState(automation: AutomationView, nextEditing: AutomationView | null, nextName: string) {
    const cond = readCondition(automation.definition.condition);
    setEditing(nextEditing);
    setName(nextName);
    setBlueprintID(
      automation.definition.action.mode === 'compress'
        ? 'automatic-compression'
        : 'automatic-folder-conversion',
    );
    setInputID(automation.definition.trigger.inputDriveId);
    setOutputID(automation.definition.action.outputDriveId);
    setProfileID(automation.definition.action.profileId);
    setPresetID('');
    setTarget(automation.definition.action.targetFormat);
    setActionOptions(automation.definition.action.options || {});
    setExtensionsInput((automation.definition.filter.extensions || []).join(', ') || cond.extensions);
    setNameGlob(automation.definition.filter.nameGlob || cond.glob);
    setMinMB(
      automation.definition.filter.minBytes
        ? String(Math.round(automation.definition.filter.minBytes / 1048576))
        : cond.min,
    );
    setMaxMB(
      automation.definition.filter.maxBytes
        ? String(Math.round(automation.definition.filter.maxBytes / 1048576))
        : cond.max,
    );
    setConditionLogic(cond.logic);
    setIntervalValue(String(automation.definition.trigger.intervalSeconds || 60));
    setStability(String(automation.definition.trigger.stabilitySeconds || 30));
    setMaxConcurrent(String(automation.definition.trigger.maxConcurrent || 1));
    setProcessExisting(false);
    setBehavior(automation.definition.action.outputBehavior || 'unique');
    setPreview(null);
    setHistory([]);
  }

  function resetEditor() {
    setEditing(null);
    setName('');
    setInputID('');
    setOutputID('');
    setProfileID('');
    setPresetID('');
    setTarget('');
    setActionOptions({});
    setExtensionsInput('');
    setNameGlob('');
    setMinMB('');
    setMaxMB('');
    setConditionLogic('and');
    setPreview(null);
    setHistory([]);
    setProcessExisting(false);
    setBlueprintID('automatic-folder-conversion');
    setEditorMode('visual');
  }

  function buildDefinition() {
    const parsedExtensions = extensionsInput
      .split(',')
      .map(item => item.trim().replace(/^\./, '').toLowerCase())
      .filter(Boolean);
    const leaves: AutomationCondition[] = [
      ...parsedExtensions.map(value => ({ operator: 'extension', value })),
      ...(nameGlob.trim() ? [{ operator: 'name_glob', value: nameGlob.trim() }] : []),
      ...(minMB ? [{ operator: 'min_size', bytes: Number(minMB) * 1048576 }] : []),
      ...(maxMB ? [{ operator: 'max_size', bytes: Number(maxMB) * 1048576 }] : []),
    ];

    let condition: AutomationCondition | undefined;
    if (leaves.length === 1) {
      condition = leaves[0];
    } else if (leaves.length > 1) {
      condition = { operator: conditionLogic === 'not' ? 'and' : conditionLogic, children: leaves };
    }
    if (condition && conditionLogic === 'not') {
      condition = { operator: 'not', children: [condition] };
    }

    const mode = blueprint?.mode || editing?.definition.action.mode || 'convert';
    const nodes: AutomationGraphNode[] = [
      {
        id: 'trigger',
        blockId: 'trigger.folder-scan',
        version: '1.0.0',
        kind: 'trigger',
        config: {
          type: 'folder.scan',
          inputDriveId: inputID,
          intervalSeconds: Number(interval),
          stabilitySeconds: Number(stability),
          maxConcurrent: Number(maxConcurrent),
          processExistingOnNextScan: processExisting,
        },
      },
    ];
    if (leaves.length || condition) {
      nodes.push({
        id: 'conditions',
        blockId: 'condition.file-filter',
        version: '1.0.0',
        kind: 'condition',
        config: {
          extensions: parsedExtensions,
          nameGlob: nameGlob.trim(),
          minBytes: Number(minMB || 0) * 1048576,
          maxBytes: Number(maxMB || 0) * 1048576,
          tree: condition,
        },
      });
    }
    nodes.push({
      id: 'action',
      blockId: mode === 'compress' ? 'action.compress' : 'action.convert',
      version: '1.0.0',
      kind: 'action',
      config: {
        mode,
        profileId: profileID,
        profileVersion: profileChoice?.definition.version || editing?.definition.action.profileVersion || '',
        targetFormat: target,
        options: actionOptions,
      },
    });
    nodes.push({
      id: 'output',
      blockId: 'action.file-output',
      version: '1.0.0',
      kind: 'output',
      config: { outputDriveId: outputID, behavior },
    });

    const edges =
      nodes.length === 4
        ? [
            { from: 'trigger', to: 'conditions' },
            { from: 'conditions', to: 'action' },
            { from: 'action', to: 'output' },
          ]
        : [
            { from: 'trigger', to: 'action' },
            { from: 'action', to: 'output' },
          ];

    return {
      trigger: {
        type: 'folder.scan',
        inputDriveId: inputID,
        intervalSeconds: Number(interval),
        stabilitySeconds: Number(stability),
        maxConcurrent: Number(maxConcurrent),
        processExistingOnNextScan: processExisting,
      },
      filter: {},
      condition,
      action: {
        mode,
        profileId: profileID,
        profileVersion: profileChoice?.definition.version || editing?.definition.action.profileVersion,
        targetFormat: target,
        options: actionOptions,
        outputDriveId: outputID,
        outputBehavior: behavior,
      },
      graph: { nodes, edges },
    };
  }

  async function saveAutomation(event: FormEvent) {
    event.preventDefault();
    setBusy(true);
    try {
      const def = buildDefinition();
      if (editing) {
        await apiRequest('/automations/' + editing.id, {
          method: 'PUT',
          body: json({ name, definition: def }),
        });
        notify('Saved a new automation revision.');
      } else {
        await apiRequest('/blueprints/' + blueprintID + '/install', {
          method: 'POST',
          body: json({
            name,
            inputDriveId: inputID,
            outputDriveId: outputID,
            profileId: profileID,
            profileVersion: profileChoice?.definition.version,
            targetFormat: target,
            options: actionOptions,
            condition: def.condition,
            processExistingOnNextScan: processExisting,
            outputBehavior: behavior,
            intervalSeconds: Number(interval),
            stabilitySeconds: Number(stability),
            maxConcurrent: Number(maxConcurrent),
          }),
        });
        notify('Blueprint installed as an independent automation.');
      }
      setProcessExisting(false);
      await onRefreshAutomations();
    } catch (err) {
      notify((err as Error).message);
    } finally {
      setBusy(false);
    }
  }

  async function toggle(automation: AutomationView) {
    try {
      await apiRequest('/automations/' + automation.id + '/enabled', {
        method: 'PATCH',
        body: json({ enabled: !automation.enabled }),
      });
      await onRefreshAutomations();
    } catch (err) {
      notify((err as Error).message);
    }
  }

  async function showPreview(automation: AutomationView) {
    try {
      setPreview(
        await apiRequest<AutomationDryRunPreview>('/automations/' + automation.id + '/dry-run', {
          method: 'POST',
          body: json({}),
        }),
      );
    } catch (err) {
      notify((err as Error).message);
    }
  }

  async function runNow(automation: AutomationView) {
    try {
      await apiRequest('/automations/' + automation.id + '/run', { method: 'POST', body: json({}) });
      const hist = await apiRequest<{ runs: AutomationRunView[] }>(
        '/automations/' + automation.id + '/history',
      );
      setHistory(hist.runs);
      await onRefreshAutomations();
      notify('Folder scan started.');
    } catch (err) {
      notify((err as Error).message);
    }
  }

  async function loadHistory(automation: AutomationView) {
    try {
      const hist = await apiRequest<{ runs: AutomationRunView[] }>(
        '/automations/' + automation.id + '/history',
      );
      setHistory(hist.runs);
    } catch (err) {
      notify((err as Error).message);
    }
  }

  async function deleteAutomation(automation: AutomationView) {
    try {
      await apiRequest('/automations/' + automation.id, { method: 'DELETE' });
      await onRefreshAutomations();
      if (editing?.id === automation.id) resetEditor();
    } catch (err) {
      notify((err as Error).message);
    }
  }

  return (
    <div className="automation-admin-grid">
      <section className="panel">
        <div className="panel-heading">
          <div>
            <h2>Automations</h2>
            <p>Scans inspect at most 250 top-level entries and submit up to 20 stable files per pass.</p>
          </div>
          <Workflow size={18} />
        </div>
        {automationsError ? (
          <ErrorState message={automationsError} retry={onRefreshAutomations} />
        ) : automationsLoading ? (
          <Loading />
        ) : automations.length === 0 ? (
          <div className="empty-state">
            <h3>No automations yet</h3>
            <p>Choose a blueprint to create an editable workflow.</p>
          </div>
        ) : (
          <div className="automation-list">
            {automations.map(automation => (
              <article className="automation-card" key={automation.id}>
                <div className="automation-card-title">
                  <div>
                    <b>{automation.name}</b>
                    <small>
                      Revision {automation.revision} · {automation.definition.action.mode} · {automation.status}
                      {automation.lastExecution
                        ? ' · Last run ' + new Date(automation.lastExecution).toLocaleString()
                        : ' · Not run yet'}
                    </small>
                  </div>
                  <button
                    className="icon-btn"
                    aria-label={automation.enabled ? 'Pause automation' : 'Enable automation'}
                    onClick={() => void toggle(automation)}
                  >
                    {automation.enabled ? <Pause size={16} /> : <Play size={16} />}
                  </button>
                </div>
                <p>
                  {drives.find(drive => drive.id === automation.definition.trigger.inputDriveId)?.name ||
                    'Missing input'}{' '}
                  →{' '}
                  {drives.find(drive => drive.id === automation.definition.action.outputDriveId)?.name ||
                    'Missing output'}
                </p>
                {automation.diagnostic && <p className="form-error">{automation.diagnostic}</p>}
                <div className="drive-actions">
                  <button
                    className="text-btn"
                    onClick={() => loadEditorState(automation, automation, automation.name)}
                  >
                    Edit
                  </button>
                  <button className="text-btn" onClick={() => void showPreview(automation)}>
                    <Eye size={14} /> Preview
                  </button>
                  <button className="text-btn" onClick={() => void runNow(automation)}>
                    <Play size={14} /> Run now
                  </button>
                  <button className="text-btn" onClick={() => void loadHistory(automation)}>
                    History
                  </button>
                  <button
                    className="text-btn"
                    onClick={() => loadEditorState(automation, null, automation.name + ' copy')}
                  >
                    Duplicate
                  </button>
                  <button className="text-btn" onClick={() => void deleteAutomation(automation)}>
                    <Trash2 size={14} /> Delete
                  </button>
                </div>
              </article>
            ))}
          </div>
        )}

        {(history.length > 0 || preview) && (
          <section className="automation-history">
            <h3>{preview ? 'Current-file match preview' : 'Execution history'}</h3>
            {preview && (
              <p className="muted-copy">
                Current filename and size matches only. Execution also checks stability, baseline skips, generated or
                duplicate contents, permissions, and concurrency. This preview does not guarantee a run.
                {preview.truncated ? ' Results are truncated.' : ''}
              </p>
            )}
            {preview?.files?.map(file => (
              <div key={file.name}>
                <span>
                  {file.matches ? 'Match' : 'Skip'} · {file.name}
                </span>
                <small>
                  {bytes(file.size)} {file.reason ? `· ${file.reason}` : ''}
                </small>
              </div>
            ))}
            {history.map(run => (
              <div key={run.id}>
                <span>{run.fileName}</span>
                <b>{run.state}</b>
                {run.outputName && <small>{run.outputName}</small>}
                {run.error && <small>{run.error}</small>}
              </div>
            ))}
          </section>
        )}

        <ExtensionReleases
          extensions={extensions}
          error={extensionsError}
          onRefresh={onRefreshExtensionsAndAutomations}
          notify={notify}
        />
      </section>

      <section className="panel">
        <h2>{editing ? 'Edit automation' : 'Create from blueprint'}</h2>
        <p className="muted-copy">
          An edit creates a new revision. Existing executions retain their pinned profile and engine specification.
        </p>
        <div className="tabbar automation-editor-tabs" role="tablist" aria-label="Automation editor mode">
          <button
            className={editorMode === 'visual' ? 'active' : ''}
            type="button"
            onClick={() => setEditorMode('visual')}
          >
            Visual flow
          </button>
          <button
            className={editorMode === 'details' ? 'active' : ''}
            type="button"
            onClick={() => setEditorMode('details')}
          >
            Block details
          </button>
        </div>

        {editorMode === 'visual' && (
          <div className="automation-flow" aria-label="Typed visual automation flow">
            <article>
              <span className="flow-kind">TRIGGER</span>
              <b>Watch a mounted folder</b>
              <small>
                {inputID ? 'Input folder selected' : 'Choose an input folder below'} · stable files only
              </small>
            </article>
            <span className="flow-arrow" aria-hidden="true">
              ↓
            </span>
            <article>
              <span className="flow-kind">CONDITION</span>
              <b>Inspect and match files</b>
              <small>
                {extensionsInput || nameGlob || minMB || maxMB
                  ? 'Configured filters apply'
                  : 'All supported files'}{' '}
                · optional AND / OR / NOT group
              </small>
            </article>
            <span className="flow-arrow" aria-hidden="true">
              ↓
            </span>
            <article>
              <span className="flow-kind">ACTION</span>
              <b>
                {(blueprint?.mode || editing?.definition.action.mode) === 'compress'
                  ? 'Compress file'
                  : 'Convert file'}
              </b>
              <small>
                {profileID && target
                  ? `${profileID} → ${target.toUpperCase()}`
                  : 'Choose a typed provider below'}
              </small>
            </article>
            <span className="flow-arrow" aria-hidden="true">
              ↓
            </span>
            <article>
              <span className="flow-kind">OUTPUT</span>
              <b>Publish a validated result</b>
              <small>
                {outputID ? 'Output folder selected' : 'Choose a writable folder below'} · never overwrite originals
              </small>
            </article>
          </div>
        )}

        <form className="automation-form" onSubmit={saveAutomation}>
          <label className="field-label">
            Blueprint
            <select
              value={blueprintID}
              disabled={Boolean(editing)}
              onChange={event => {
                setBlueprintID(event.target.value);
                setProfileID('');
                setPresetID('');
                setTarget('');
                setActionOptions({});
                setMinMB(event.target.value === 'automatic-compression' ? '10' : '');
              }}
            >
              {blueprints.map(item => (
                <option key={item.id} value={item.id}>
                  {item.name} · v{item.version}
                </option>
              ))}
            </select>
          </label>
          {blueprint && <p className="muted-copy">{blueprint.description}</p>}

          <label className="field-label">
            Name
            <input
              required
              maxLength={120}
              value={name}
              onChange={event => setName(event.target.value)}
              placeholder="Convert incoming photos"
            />
          </label>

          <label className="field-label">
            Input folder
            <select required value={inputID} onChange={event => setInputID(event.target.value)}>
              <option value="">Choose a read-access drive</option>
              {drives
                .filter(drive => drive.enabled && drive.accessible)
                .map(drive => (
                  <option key={drive.id} value={drive.id}>
                    {drive.name} · {drive.path}
                  </option>
                ))}
            </select>
          </label>

          <label className="field-label">
            Output folder
            <select required value={outputID} onChange={event => setOutputID(event.target.value)}>
              <option value="">Choose a writable drive</option>
              {drives
                .filter(drive => drive.enabled && drive.access === 'read_write' && drive.writable)
                .map(drive => (
                  <option key={drive.id} value={drive.id}>
                    {drive.name} · {drive.path}
                  </option>
                ))}
            </select>
          </label>

          <label className="field-label">
            Saved preset
            <select
              aria-label="Automation preset"
              value={presetID}
              onChange={event => {
                const preset = presets.find(item => item.id === event.target.value);
                setPresetID(event.target.value);
                if (preset) {
                  setProfileID(preset.profileId || '');
                  setTarget(preset.targetFormat);
                  setActionOptions(preset.options || {});
                }
              }}
            >
              <option value="">Choose a preset (optional)</option>
              {presets
                .filter(preset => preset.mode === (blueprint?.mode || 'convert') && preset.compatible)
                .map(preset => (
                  <option key={preset.id} value={preset.id}>
                    {preset.name}
                    {preset.default ? ' · default' : ''}
                  </option>
                ))}
            </select>
            <small>Applying a preset pins its validated options into this automation revision.</small>
          </label>

          <label className="field-label">
            Installed profile
            <select
              required
              value={profileID}
              onChange={event => {
                setProfileID(event.target.value);
                setPresetID('');
                setTarget('');
                setActionOptions({});
              }}
            >
              <option value="">Choose profile</option>
              {profiles
                .filter(
                  profile =>
                    profile.state.enabled &&
                    profile.definition.type !== (blueprint?.mode === 'compress' ? 'conversion' : 'compression'),
                )
                .map(profile => (
                  <option key={profile.definition.id} value={profile.definition.id}>
                    {profile.definition.name} · v{profile.definition.version}
                  </option>
                ))}
            </select>
          </label>

          <label className="field-label">
            Output format
            <select required value={target} onChange={event => setTarget(event.target.value)}>
              <option value="">Choose format</option>
              {targets.map(format => (
                <option key={format} value={format}>
                  {format.toUpperCase()}
                </option>
              ))}
            </select>
          </label>

          <details className="auto-advanced" open={Boolean(editing)}>
            <summary>
              Which files to pick up <small>optional</small>
            </summary>
            <div className="auto-advanced-body">
              <label className="field-label">
                Condition group
                <select
                  aria-label="Condition group"
                  value={conditionLogic}
                  onChange={event => setConditionLogic(event.target.value as 'and' | 'or' | 'not')}
                >
                  <option value="and">Match all blocks (AND)</option>
                  <option value="or">Match any block (OR)</option>
                  <option value="not">Exclude files matching all blocks (NOT)</option>
                </select>
              </label>
              <label className="field-label">
                Extensions
                <input
                  value={extensionsInput}
                  onChange={event => setExtensionsInput(event.target.value)}
                  placeholder="jpg, png (empty = all supported)"
                />
              </label>
              <label className="field-label">
                Filename pattern
                <input
                  value={nameGlob}
                  onChange={event => setNameGlob(event.target.value)}
                  placeholder="summer-*.jpg"
                />
                <small>Optional glob; leave blank to match any name.</small>
              </label>
              <div className="automation-two-fields">
                <label className="field-label">
                  Minimum file size (MB)
                  <input
                    type="number"
                    min="0"
                    value={minMB}
                    onChange={event => setMinMB(event.target.value)}
                    placeholder={blueprintID === 'automatic-compression' ? '10' : 'No minimum'}
                  />
                </label>
                <label className="field-label">
                  Maximum file size (MB)
                  <input
                    type="number"
                    min="0"
                    value={maxMB}
                    onChange={event => setMaxMB(event.target.value)}
                    placeholder="No maximum"
                  />
                </label>
              </div>
            </div>
          </details>

          <details className="auto-advanced" open={Boolean(editing)}>
            <summary>
              Timing and output behavior <small>optional</small>
            </summary>
            <div className="auto-advanced-body">
              <div className="automation-two-fields">
                <label className="field-label">
                  Scan interval (seconds)
                  <input
                    type="number"
                    min="30"
                    max="86400"
                    value={interval}
                    onChange={event => setIntervalValue(event.target.value)}
                  />
                </label>
                <label className="field-label">
                  Stable for (seconds)
                  <input
                    type="number"
                    min="5"
                    max="3600"
                    value={stability}
                    onChange={event => setStability(event.target.value)}
                  />
                </label>
                <label className="field-label">
                  Concurrent files (1–8)
                  <input
                    type="number"
                    min="1"
                    max="8"
                    value={maxConcurrent}
                    onChange={event => setMaxConcurrent(event.target.value)}
                  />
                </label>
              </div>
              <label className="field-label">
                If output exists
                <select value={behavior} onChange={event => setBehavior(event.target.value)}>
                  <option value="unique">Create a unique filename</option>
                  <option value="skip_existing">Skip existing output</option>
                  <option value="create">Create only if absent</option>
                </select>
              </label>
              <label className="automation-check">
                <input
                  type="checkbox"
                  checked={processExisting}
                  onChange={event => setProcessExisting(event.target.checked)}
                />{' '}
                Request existing files once on the next scan
              </label>
            </div>
          </details>

          <p className="muted-copy">
            Outputs are staged and never overwrite existing files. Originals are retained. The conversion Worker cannot
            access mounted folders.
          </p>
          <div className="form-actions">
            <button className="primary" disabled={busy}>
              {editing ? 'Save new revision' : 'Install blueprint'}
            </button>
            {editing && (
              <button type="button" className="secondary" onClick={resetEditor}>
                New
              </button>
            )}
          </div>
        </form>
      </section>
    </div>
  );
}
