import { useRef, useState, type DragEvent } from 'react';
import { ArrowRight, FileUp, ShieldCheck, Upload, X } from 'lucide-react';

export const MAX_FILE_BYTES = 1024 ** 3;

export function selectFiles(current: File[], incoming: File[], limit: number) {
  const accepted: File[] = [];
  const issues: string[] = [];
  for (const file of incoming) {
    if (!file.size) issues.push(`${file.name} is empty.`);
    else if (file.size > MAX_FILE_BYTES) issues.push(`${file.name} exceeds the 1 GiB upload ceiling.`);
    else accepted.push(file);
  }
  const available = Math.max(0, limit - current.length);
  if (accepted.length > available) issues.push(`You can select ${limit} files at a time. ${accepted.length - available} extra file(s) were not added.`);
  return { files: [...current, ...accepted.slice(0, available)], message: issues.join(' ') };
}

export function UploadArea({ mode, files, onFiles, limit, disabled, formats, message, onDismiss }: {
  mode: 'convert' | 'compress'; files: number; onFiles: (incoming: File[]) => void;
  limit: number; disabled: boolean; formats: string[]; message: string; onDismiss: () => void;
}) {
  const input = useRef<HTMLInputElement>(null);
  const [dragging, setDragging] = useState(false);
  const depth = useRef(0);
  const stopDrag = (event: DragEvent) => { event.preventDefault(); event.stopPropagation(); };
  return <>
    <section className={`dropzone${dragging ? ' dragging' : ''}${disabled ? ' upload-locked' : ''}${files ? ' has-files' : ''}`}
      aria-label={mode === 'convert' ? 'Upload files' : 'Upload files to compress'} aria-disabled={disabled}
      onDragEnter={event => { stopDrag(event); depth.current++; if (!disabled) setDragging(true); }}
      onDragOver={event => { stopDrag(event); event.dataTransfer.dropEffect = disabled ? 'none' : 'copy'; }}
      onDragLeave={event => { stopDrag(event); depth.current = Math.max(0, depth.current - 1); if (!depth.current) setDragging(false); }}
      onDrop={event => { stopDrag(event); depth.current = 0; setDragging(false); if (!disabled) onFiles(Array.from(event.dataTransfer.files)); }}>
      <input ref={input} type="file" multiple hidden disabled={disabled} onChange={event => { if (event.target.files) onFiles(Array.from(event.target.files)); event.target.value = ''; }} />
      <span className="upload-icon"><Upload size={26} strokeWidth={1.6} /></span>
      <h2>{dragging ? 'Drop to add your files' : files ? 'Add more files' : mode === 'compress' ? 'Drop files to compress' : 'Drop your files here'}</h2>
      <p>{files ? 'Build your batch, one file or many.' : 'or choose files from your device.'}</p>
      <button className="primary browse-button" disabled={disabled} onClick={() => input.current?.click()}><FileUp size={17} /> {files ? 'Add more files' : 'Browse files'} <ArrowRight size={15} /></button>
      <span className="upload-hint">Up to 1 GiB per file · {limit} files per batch</span>
      <span className="upload-account-hint">Your account or guest limits may be lower.</span>
    </section>
    {message && <div className="banner error selection-message" role="alert"><X size={17} /><span>{message}</span><button className="icon-btn" aria-label="Dismiss file selection warning" onClick={onDismiss}><X size={16} /></button></div>}
    {!files && formats.length > 0 && <div className="supported-formats"><span>Supported inputs</span><div>{formats.slice(0,10).map(format => <span key={format}>{format.toUpperCase()}</span>)}{formats.length > 10 && <span>+{formats.length - 10} more</span>}</div></div>}
    <div className="upload-privacy"><ShieldCheck size={15} /><span>Processed on this instance. Your files stay in your workspace.</span></div>
  </>;
}
