import type { FormEvent } from 'react';

export type AnonymousSettings = {
  enabled: boolean; allowConversion: boolean; allowCompression: boolean;
  maxFileBytes: number; maxBatchFiles: number; maxSimultaneousJobs: number; uploadsPerMinute: number; submissionsPerMinute: number;
  globalUploadsPerMinute: number; globalSubmissionsPerMinute: number; maxGlobalJobs: number; maxGlobalStorageBytes: number;
  resultExpirationHours: number; sessionExpirationHours: number; uploadExpirationHours: number; failedExpirationHours: number; metadataExpirationHours: number;
};
type NumericKey = Exclude<keyof AnonymousSettings, 'enabled' | 'allowConversion' | 'allowCompression'>;
// [key, label, min, max, scale (stored value / displayed value), unit, hint]
type Field = [NumericKey, string, number, number, number, string, string?];

const GROUPS: { title: string; blurb: string; fields: Field[] }[] = [
  { title: 'Per-guest limits', blurb: '', fields: [
    ['maxFileBytes', 'Largest file', 1, 1024, 1024 * 1024, 'MiB'],
    ['maxBatchFiles', 'Files per batch', 1, 20, 1, 'files'],
    ['maxSimultaneousJobs', 'Jobs at once', 1, 100, 1, 'jobs'],
    ['uploadsPerMinute', 'Uploads per minute', 1, 120, 1, '/min'],
    ['submissionsPerMinute', 'Submissions per minute', 1, 60, 1, '/min'],
  ] },
  { title: 'Whole-instance guest capacity', blurb: '', fields: [
    ['maxGlobalJobs', 'Jobs at once, all guests', 1, 1000, 1, 'jobs'],
    ['globalUploadsPerMinute', 'Uploads per minute, all guests', 1, 10000, 1, '/min'],
    ['globalSubmissionsPerMinute', 'Submissions per minute, all guests', 1, 10000, 1, '/min'],
    ['maxGlobalStorageBytes', 'Total guest storage', 1, 1024, 1024 * 1024 * 1024, 'GiB', 'Keep above one largest file plus its result.'],
  ] },
  { title: 'How long guest data is kept', blurb: '', fields: [
    ['sessionExpirationHours', 'Guest session', 1, 720, 1, 'hours'],
    ['resultExpirationHours', 'Results and converted originals', 1, 720, 1, 'hours'],
    ['uploadExpirationHours', 'Uploads never submitted', 1, 720, 1, 'hours'],
    ['failedExpirationHours', 'Failed or canceled originals', 1, 720, 1, 'hours'],
    ['metadataExpirationHours', 'Job history', 1, 8760, 1, 'hours'],
  ] },
];

export function AnonymousAccessForm({ settings, onChange, onSubmit, saving }: { settings: AnonymousSettings; onChange: (next: AnonymousSettings) => void; onSubmit: (event: FormEvent) => void; saving: boolean }) {
  return (
    <form onSubmit={onSubmit} className="guest-form">
      <fieldset className="guest-group">
        <legend>What guests can do</legend>
        <div className="guest-checks">
          {([['allowConversion', 'Convert files'], ['allowCompression', 'Compress files']] as const).map(([key, label]) => (
            <label key={key} className="guest-check"><input type="checkbox" checked={settings[key]} onChange={event => onChange({ ...settings, [key]: event.target.checked })} />{label}</label>
          ))}
        </div>
      </fieldset>
      {GROUPS.map(group => (
        <fieldset key={group.title} className="guest-group">
          <legend>{group.title}</legend>
          {group.blurb && <p className="guest-blurb">{group.blurb}</p>}
          <div className="guest-fields">
            {group.fields.map(([key, label, min, max, scale, unit, hint]) => (
              <label className="admin-field" key={key}>
                <span>{label}</span>
                <span className="admin-input"><input type="number" min={min} max={max} step={1} value={Math.round(Number(settings[key]) / scale)} onChange={event => onChange({ ...settings, [key]: Number(event.target.value) * scale })} /><em>{unit}</em></span>
                {hint && <small>{hint}</small>}
              </label>
            ))}
          </div>
        </fieldset>
      ))}
      {!settings.enabled && <p className="muted-copy">Anonymous access is off, so guests cannot start new work.</p>}
      <button className="primary" disabled={saving}>{saving ? 'Saving…' : 'Save anonymous access'}</button>
    </form>
  );
}
