import { apiRequest } from '../session';
import type {
  DocumentFormatInfo,
  DocumentManifest,
  DocumentProject,
  ExportJob,
  SourceInspection,
} from './types';

export async function fetchDocumentFormats(): Promise<DocumentFormatInfo[]> {
  const data = await apiRequest<{ formats: DocumentFormatInfo[] }>('/documents/formats');
  return Array.isArray(data.formats) ? data.formats : [];
}

export async function inspectDocumentSource(uploadId: string): Promise<SourceInspection> {
  return apiRequest<SourceInspection>(`/documents/sources/${encodeURIComponent(uploadId)}`);
}

export async function listDocumentProjects(): Promise<DocumentProject[]> {
  const data = await apiRequest<{ projects: DocumentProject[] }>('/documents/projects');
  return Array.isArray(data.projects) ? data.projects : [];
}

export async function saveDocumentProject(manifest: DocumentManifest, existingId?: string): Promise<DocumentProject> {
  const path = existingId
    ? `/documents/projects/${encodeURIComponent(existingId)}`
    : '/documents/projects';
  const data = await apiRequest<{ project: DocumentProject }>(path, {
    method: existingId ? 'PUT' : 'POST',
    body: JSON.stringify(manifest),
  });
  return data.project;
}

export async function deleteDocumentProject(projectId: string): Promise<void> {
  await apiRequest(`/documents/projects/${encodeURIComponent(projectId)}`, { method: 'DELETE' });
}

export async function exportDocumentOnServer(manifest: DocumentManifest, idempotencyKey: string): Promise<ExportJob> {
  const data = await apiRequest<{ job: ExportJob }>('/documents/export', {
    method: 'POST',
    body: JSON.stringify({ manifest, idempotencyKey }),
  });
  return data.job;
}

export async function fetchBuildJob(jobId: string): Promise<ExportJob> {
  const data = await apiRequest<{ job: ExportJob }>(`/jobs/${encodeURIComponent(jobId)}`);
  return data.job;
}

export async function downloadBuildResult(jobId: string, fileName: string): Promise<void> {
  const response = await fetch(`/api/v1/jobs/${encodeURIComponent(jobId)}/result`, {
    credentials: 'include',
  });
  if (!response.ok) {
    let message = `Download failed (${response.status})`;
    try {
      const err = (await response.json()) as { error?: { message?: string } };
      if (err?.error?.message) message = err.error.message;
    } catch { /* ignore */ }
    throw new Error(message);
  }
  const blob = await response.blob();
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement('a');
  anchor.href = url;
  anchor.download = fileName;
  anchor.click();
  URL.revokeObjectURL(url);
}
