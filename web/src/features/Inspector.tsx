import { useState } from 'react';
import { Check, CircleAlert, Download, Eye, File, LoaderCircle, ShieldCheck } from 'lucide-react';
import { UploadArea, selectFiles } from '../components/UploadArea';
import { uploadWithProgress, type Session } from './session';

type Stream = { index: number; codec_type: string; codec_name?: string; width?: number; height?: number; color_primaries?: string; color_transfer?: string; color_space?: string; color_range?: string; bits_per_raw_sample?: string; pix_fmt?: string; channels?: number; channel_layout?: string; sample_rate?: string; bitRate?: number; tags?: Record<string, string>; sideData?: { side_data_type: string; values?: Record<string, unknown> }[] };
type Report = { file: { id: string; fileName: string; extension: string; size: number; sha256: string }; mimeType?: string; detectedType?: string; summary: string; media?: { duration?: number; format?: string; formatTags?: Record<string, string>; streams: Stream[]; chapters?: unknown[]; rawJson?: unknown }; image?: { width: number; height: number; colorModel?: string; exifPresent?: boolean; iccProfile?: boolean }; document?: { kind: string; pdfVersion?: string; packageEntries?: number; expandedBytes?: number; metadata?: Record<string, string> }; warnings?: string[]; rawJson: unknown };

const formatBytes = (size: number) => size < 1024 * 1024 ? `${Math.round(size / 1024)} KB` : `${(size / 1024 / 1024).toFixed(1)} MB`;
const streamLabel = (stream: Stream) => {
  const kind = stream.codec_type || 'stream';
  const codec = stream.codec_name ? ` · ${stream.codec_name}` : '';
  if (kind === 'video' && stream.width && stream.height) return `Video${codec} · ${stream.width} × ${stream.height}`;
  if (kind === 'audio') return `Audio${codec}${stream.channel_layout ? ` · ${stream.channel_layout}` : ''}`;
  return `${kind.charAt(0).toUpperCase() + kind.slice(1)}${codec}`;
};

export function Inspector({ notify, session }: { notify: (message: string) => void; session: Session }) {
  const [files, setFiles] = useState<File[]>([]);
  const [message, setMessage] = useState('');
  const [report, setReport] = useState<Report | null>(null);
  const [busy, setBusy] = useState(false);
  const [progress, setProgress] = useState(0);

  const choose = (incoming: File[]) => {
    const selected = selectFiles([], incoming, 1);
    setFiles(selected.files.slice(0, 1));
    setMessage(selected.message);
    setReport(null);
  };

  async function inspect() {
    const file = files[0];
    if (!file || busy) return;
    setBusy(true);
    setProgress(0);
    try {
      setReport(await uploadWithProgress<Report>(file, setProgress, session.guest === true, '/inspect'));
    } catch (error) {
      notify((error as Error).message || 'The file could not be inspected.');
    } finally {
      setBusy(false);
    }
  }

  function downloadReport() {
    if (!report) return;
    const blob = new Blob([JSON.stringify(report.rawJson, null, 2)], { type: 'application/json' });
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement('a');
    anchor.href = url;
    anchor.download = `${report.file.fileName.replace(/[\\/\s]+/g, '-')}-inspection.json`;
    anchor.click();
    URL.revokeObjectURL(url);
  }

  return <>
    <div className="page-heading"><div><h1>Inspect a file</h1><p className="page-description">See a readable summary and the fullest bounded metadata report before choosing a conversion or compression profile.</p></div>{report && <button className="secondary" onClick={downloadReport}><Download size={15}/> Download JSON</button>}</div>
    <div className="banner setup-notice"><ShieldCheck size={18}/><div><b>Ephemeral inspection</b><span>The file exists only while this scan runs; it is not added to conversion storage. The report includes extracted metadata in full, which may contain GPS, location, device, ownership, and other sensitive values.</span></div></div>
    <div className="inspector-layout">
      <div>
        <UploadArea mode="convert" files={files.length} onFiles={choose} limit={1} disabled={busy} formats={[]} message={message} onDismiss={() => setMessage('')}/>
        {files.length > 0 && <section className="file-list-panel"><div className="section-row"><div><h3>Ready to inspect</h3><p>{files[0].name} · {formatBytes(files[0].size)}</p></div><button className="text-btn" disabled={busy} onClick={() => { setFiles([]); setReport(null); }}>Clear</button></div><button className="primary submit-button" disabled={busy} onClick={() => void inspect()}>{busy ? <><LoaderCircle className="spin"/> Inspecting {progress}%</> : <><Eye size={16}/> Inspect file</>}</button></section>}
      </div>
      <div className="inspector-results">
        {!report ? <div className="empty-state"><span className="empty-icon"><File size={22}/></span><h3>Inspection results appear here</h3><p>Upload one supported file to see dimensions, streams, codecs, HDR signals, and extracted document metadata.</p></div> : <>
          <section className="inspector-summary"><div className="section-row"><div><p className="eyebrow">FILE SUMMARY</p><h2>{report.file.fileName}</h2><p>{report.summary}</p></div><span className="success-icon"><Check size={18}/></span></div><div className="inspector-facts"><span><b>Size</b>{formatBytes(report.file.size)}</span><span><b>Type</b>{report.mimeType || report.detectedType || 'Unknown'}</span><span><b>Extension</b>.{report.file.extension}</span>{report.media?.duration ? <span><b>Duration</b>{report.media.duration.toFixed(1)} s</span> : null}</div></section>
          {report.image && <section className="inspector-section"><h3>Image details</h3><div className="inspector-facts"><span><b>Dimensions</b>{report.image.width} × {report.image.height}</span>{report.image.colorModel && <span><b>Color model</b>{report.image.colorModel}</span>}<span><b>EXIF</b>{report.image.exifPresent ? 'Present (see raw metadata)' : 'Not detected'}</span><span><b>ICC profile</b>{report.image.iccProfile ? 'Present' : 'Not detected'}</span></div></section>}
          {report.document && <section className="inspector-section"><h3>Document details</h3><div className="inspector-facts"><span><b>Kind</b>{report.document.kind}</span>{report.document.pdfVersion && <span><b>PDF version</b>{report.document.pdfVersion}</span>}{report.document.packageEntries !== undefined && <span><b>Package entries</b>{report.document.packageEntries}</span>}{report.document.expandedBytes !== undefined && <span><b>Expanded size</b>{formatBytes(report.document.expandedBytes)}</span>}</div>{report.document.metadata && <details className="metadata-details"><summary>Document metadata ({Object.keys(report.document.metadata).length})</summary><pre>{JSON.stringify(report.document.metadata, null, 2)}</pre></details>}</section>}
          {report.media && <section className="inspector-section"><div className="section-row"><div><h3>Streams and metadata</h3><p>{report.media.format || 'Media container'} · {report.media.streams.length} stream(s)</p></div></div>{report.media.formatTags && <details className="metadata-details"><summary>Container tags ({Object.keys(report.media.formatTags).length})</summary><pre>{JSON.stringify(report.media.formatTags, null, 2)}</pre></details>}<div className="inspector-streams">{report.media.streams.map(stream => <article key={stream.index}><div><b>{streamLabel(stream)}</b><small>Stream {stream.index}{stream.bits_per_raw_sample ? ` · ${stream.bits_per_raw_sample}-bit` : ''}{stream.pix_fmt ? ` · ${stream.pix_fmt}` : ''}</small></div><div className="stream-properties">{stream.codec_type === 'video' && <><span>{stream.color_transfer || 'transfer unknown'}</span><span>{stream.color_primaries || 'primaries unknown'}</span><span>{stream.color_space || 'matrix unknown'}</span></>}{stream.codec_type === 'audio' && <><span>{stream.sample_rate || 'sample rate unknown'}</span><span>{stream.channels ? `${stream.channels} channels` : 'channels unknown'}</span></>}{(stream.sideData || []).some(item => /hdr|mastering|content light|dolby|dovi/i.test(item.side_data_type)) && <span className="hdr-badge">HDR metadata detected</span>}</div>{stream.tags && <details className="metadata-details"><summary>Stream tags ({Object.keys(stream.tags).length})</summary><pre>{JSON.stringify(stream.tags, null, 2)}</pre></details>}</article>)}</div></section>}
          {report.warnings?.map(warning => <div className="banner" key={warning}><CircleAlert size={17}/><span>{warning}</span></div>)}
          <details className="raw-inspection"><summary>Show extracted raw JSON</summary><pre>{JSON.stringify(report.rawJson, null, 2)}</pre></details>
        </>}
      </div>
    </div>
  </>;
}
