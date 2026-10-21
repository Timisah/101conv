import { ChevronDown } from 'lucide-react';
import type { CapabilityOption } from '../features/capabilities';

// Human-readable help for each compression control. An optional maximum size is
// described as "no limit" so an untouched field never reads as a hidden cap.
function hintFor(option: CapabilityOption) {
  if (option.targetSizeMb && option.optional) {
    return 'Optional. Leave empty for no size limit. If you set one, the highest quality that fits under it is used, and the job fails rather than exceed it.';
  }
  if (option.targetSizeMb) return 'The worker makes a bounded quality search and rejects output larger than this target.';
  if (option.key === 'compressionLevel') return 'Lossless PNG encoding; higher levels may be slower but can reduce size.';
  if (option.key === 'quality') return 'This codec-specific quality control keeps the original pixel dimensions.';
  return 'Choose the encoder control for this output format.';
}

export function CompressionOptionField({ option, value, disabled, onChange }: { option: CapabilityOption; value: string | undefined; disabled: boolean; onChange: (value: string) => void }) {
  const id = 'compression-option-' + option.key;
  const current = value ?? option.default;
  return (
    <label className="field-label" htmlFor={id}>
      {option.label}
      {option.optional && <span className="optional-tag"> · optional</span>}
      {option.type === 'select' ? (
        <div className="select-wrap">
          <select id={id} value={current} disabled={disabled} onChange={event => onChange(event.target.value)}>
            {option.choices?.map(choice => <option key={choice} value={choice}>{option.key === 'metadata' ? (choice === '0' ? 'Preserve metadata' : 'Remove metadata') : choice}</option>)}
          </select>
          <ChevronDown size={16} />
        </div>
      ) : (
        <>
          <input className="compression-number" id={id} type="number" inputMode={option.step && option.step < 1 ? 'decimal' : 'numeric'} min={option.min} max={option.max} step={option.step || 1} value={current} placeholder={option.optional ? 'No limit' : undefined} disabled={disabled} onChange={event => onChange(event.target.value)} />
          <small className="field-hint">{hintFor(option)}</small>
        </>
      )}
    </label>
  );
}
