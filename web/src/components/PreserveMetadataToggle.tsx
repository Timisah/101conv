import type { CapabilityOption } from '../features/capabilities';

// The backend option is a typed select of "0" (keep metadata) and "-1" (strip
// supported metadata). The toggle is only a friendlier control over that same
// serialized value, so saved presets and queued jobs are unaffected.
export const isMetadataChoice = (option: CapabilityOption) =>
  option.key === 'metadata' && option.type === 'select' && option.choices?.length === 2 && option.choices.includes('0') && option.choices.includes('-1');

export function PreserveMetadataToggle({ id, value, disabled, onChange }: { id: string; value: string; disabled: boolean; onChange: (value: string) => void }) {
  return (
    <label className="toggle-row" htmlFor={id}>
      <input id={id} type="checkbox" checked={value === '0'} disabled={disabled} onChange={event => onChange(event.target.checked ? '0' : '-1')} />
      <span><b>Preserve metadata</b><small>Turn off to remove supported tags such as camera and location details.</small></span>
    </label>
  );
}
