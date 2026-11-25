export type DemoFunnelEvent =
  | 'demo_visit'
  | 'cta_github_click'
  | 'cta_install_docs_click'
  | 'cta_self_host_copy';

export interface DemoStatus {
  enabled: boolean;
  killSwitchActive?: boolean;
  killSwitchReason?: string;
  maxFileBytes?: number;
  maxBatchFiles?: number;
  maxActiveJobs?: number;
  maxGlobalJobsPerHour?: number;
  maxOutputBytes?: number;
  retentionMinutes?: number;
  allowedProfiles?: string[];
  repoUrl?: string;
  privacyUrl?: string;
  termsUrl?: string;
  notice?: string;
}

export async function fetchDemoStatus(): Promise<DemoStatus> {
  try {
    const res = await fetch('/api/v1/demo/status', {
      credentials: 'same-origin',
      headers: { Accept: 'application/json' },
    });
    if (!res.ok) return { enabled: false };
    const data = (await res.json()) as DemoStatus;
    return data && typeof data.enabled === 'boolean' ? data : { enabled: false };
  } catch {
    return { enabled: false };
  }
}

export function recordDemoFunnel(event: DemoFunnelEvent): void {
  try {
    void fetch('/api/v1/demo/funnel', {
      method: 'POST',
      credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ event }),
      keepalive: true,
    });
  } catch {
    // Best-effort aggregate funnel counter; never disrupt the user flow.
  }
}

export function formatDemoMiB(bytes?: number): string {
  if (!bytes || bytes <= 0) return '15 MiB';
  return `${Math.max(1, Math.round(bytes / (1024 * 1024)))} MiB`;
}
