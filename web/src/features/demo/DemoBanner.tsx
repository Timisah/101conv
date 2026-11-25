import { useEffect, useState } from 'react';
import { Check, Copy, ExternalLink, Info, ShieldAlert } from 'lucide-react';
import { fetchDemoStatus, formatDemoMiB, recordDemoFunnel, type DemoStatus } from './api';
import './demo.css';

const SELF_HOST_CMD =
  'git clone https://github.com/Timisah/101conv.git && cd 101conv && ./scripts/install.sh';

export function DemoBanner() {
  const [status, setStatus] = useState<DemoStatus | null>(null);
  const [copied, setCopied] = useState(false);

  useEffect(() => {
    let active = true;
    void fetchDemoStatus().then(data => {
      if (!active) return;
      setStatus(data);
      if (data.enabled) {
        recordDemoFunnel('demo_visit');
      }
    });
    return () => {
      active = false;
    };
  }, []);

  if (!status?.enabled) return null;

  const repoUrl = status.repoUrl || 'https://github.com/Timisah/101conv';
  const installUrl = `${repoUrl.replace(/\/$/, '')}#new-installation`;

  async function copyQuickStart() {
    recordDemoFunnel('cta_self_host_copy');
    try {
      await navigator.clipboard.writeText(SELF_HOST_CMD);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 2400);
    } catch {
      // Clipboard may be unavailable on non-HTTPS origins.
    }
  }

  return (
    <section className="demo-banner" aria-label="Public demonstration notice">
      {status.killSwitchActive ? (
        <div className="banner error demo-kill-alert" role="alert">
          <ShieldAlert size={18} />
          <div>
            <b>Public demo temporarily paused</b>
            <span>
              {status.killSwitchReason
                ? `Processing is paused (${status.killSwitchReason}). Self-host with Docker for private, unrestricted local conversions.`
                : 'Processing is temporarily paused to protect host capacity. Self-host with Docker for private, unrestricted local conversions.'}
            </span>
          </div>
        </div>
      ) : null}

      <div className="demo-banner-card">
        <div className="demo-banner-copy">
          <div className="demo-banner-title">
            <Info size={16} aria-hidden="true" />
            <strong>Public demonstration instance</strong>
            <span className="demo-pill">
              Max {formatDemoMiB(status.maxFileBytes)} · {status.maxBatchFiles ?? 3} files ·{' '}
              {status.retentionMinutes ?? 15}m auto-delete
            </span>
          </div>
          <p>
            {status.notice ||
              'Strict size and rate limits apply. Files are deleted automatically shortly after processing. Do not upload private or sensitive documents.'}
          </p>
          {status.privacyUrl || status.termsUrl ? (
            <div className="demo-legal-links">
              {status.privacyUrl ? (
                <a href={status.privacyUrl} target="_blank" rel="noopener noreferrer">
                  Privacy &amp; architecture
                </a>
              ) : null}
              {status.termsUrl ? (
                <a href={status.termsUrl} target="_blank" rel="noopener noreferrer">
                  Demo terms
                </a>
              ) : null}
            </div>
          ) : null}
        </div>

        <div className="demo-banner-actions">
          <button
            type="button"
            className="secondary demo-cta-btn"
            onClick={() => void copyQuickStart()}
          >
            {copied ? <Check size={14} /> : <Copy size={14} />}
            <span>{copied ? 'Docker command copied' : 'Copy Docker quick start'}</span>
          </button>
          <a
            className="secondary demo-cta-link"
            href={installUrl}
            target="_blank"
            rel="noopener noreferrer"
            onClick={() => recordDemoFunnel('cta_install_docs_click')}
          >
            <span>Self-host guide</span>
            <ExternalLink size={14} aria-hidden="true" />
          </a>
          <a
            className="secondary demo-cta-link"
            href={repoUrl}
            target="_blank"
            rel="noopener noreferrer"
            onClick={() => recordDemoFunnel('cta_github_click')}
          >
            <span>GitHub</span>
            <ExternalLink size={14} aria-hidden="true" />
          </a>
        </div>
      </div>
    </section>
  );
}
