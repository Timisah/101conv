import { useEffect, useState } from 'react';
import { Eye } from 'lucide-react';
import { apiRequest } from '../session';

type InspectionSummary = {
  summary: string;
  image?: { width: number; height: number; exifPresent?: boolean; iccProfile?: boolean };
  media?: {
    durationSeconds?: number;
    streams?: {
      codec_type?: string;
      codec_name?: string;
      width?: number;
      height?: number;
      color_transfer?: string;
      color_primaries?: string;
    }[];
  };
  document?: { kind: string; pdfVersion?: string };
};

export function formatAnalysisChips(report: InspectionSummary): string[] {
  const chips: string[] = [];
  if (report.image && report.image.width > 0 && report.image.height > 0) {
    chips.push(`${report.image.width} × ${report.image.height}`);
    if (report.image.exifPresent) chips.push('EXIF');
    if (report.image.iccProfile) chips.push('ICC');
  }
  const streams = report.media?.streams || [];
  const video = streams.find(s => s.codec_type === 'video');
  const audioCount = streams.filter(s => s.codec_type === 'audio').length;
  const subCount = streams.filter(s => s.codec_type === 'subtitle').length;
  if (video) {
    if (video.width && video.height) chips.push(`${video.width} × ${video.height}`);
    if (video.codec_name) chips.push(video.codec_name.toUpperCase());
    if (video.color_transfer === 'smpte2084' || video.color_transfer === 'arib-std-b67') {
      chips.push('HDR');
    }
  }
  if (audioCount > 0) {
    const firstAudio = streams.find(s => s.codec_type === 'audio');
    chips.push(
      audioCount === 1 && firstAudio?.codec_name
        ? `Audio: ${firstAudio.codec_name.toUpperCase()}`
        : `${audioCount} audio streams`,
    );
  }
  if (subCount > 0) {
    chips.push(`${subCount} subtitle${subCount === 1 ? '' : 's'}`);
  }
  if (report.document) {
    chips.push(report.document.pdfVersion ? `PDF ${report.document.pdfVersion}` : report.document.kind.toUpperCase());
  }
  return chips;
}

export function SourceAnalysisHint({ files, disabled }: { files: File[]; disabled?: boolean }) {
  const first = files[0] || null;
  const [report, setReport] = useState<InspectionSummary | null>(null);

  useEffect(() => {
    setReport(null);
    if (!first || disabled || first.size === 0 || first.size > 64 * 1024 * 1024) return;
    let cancelled = false;
    const timer = window.setTimeout(async () => {
      try {
        const form = new FormData();
        form.append('file', first);
        const data = await apiRequest<InspectionSummary>('/inspect', { method: 'POST', body: form });
        if (!cancelled && data && typeof data === 'object') {
          setReport(data);
        }
      } catch {
        // Ephemeral hint is best-effort and never blocks conversion/compression.
      }
    }, 200);
    return () => {
      cancelled = true;
      window.clearTimeout(timer);
    };
  }, [first, disabled]);

  if (!report) return null;
  const chips = formatAnalysisChips(report);
  if (!chips.length) return null;

  return (
    <div className="inline-callout" style={{ marginTop: 8, padding: '8px 12px', fontSize: 12 }} aria-label="Detected source properties">
      <Eye size={14} aria-hidden="true" />
      <span>
        <b>Detected{files.length > 1 ? ` (${first?.name})` : ''}:</b> {chips.join(' · ')}
      </span>
    </div>
  );
}
