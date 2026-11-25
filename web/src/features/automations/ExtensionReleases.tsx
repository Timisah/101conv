import { useEffect, useState } from 'react';
import { json } from '../format';
import { apiRequest } from '../session';
import { ImmichConnector } from './ImmichConnector';
import type { ExtensionView } from './types';

type CatalogExtension = {
  id: string;
  name: string;
  publisher: string;
  version: string;
  externalApplication?: string;
  externalVersion?: string;
  permissions?: string[];
  integrity?: string;
  blocks?: { id: string; version: string; kind: string; name: string }[];
};

export function ExtensionReleases({
  extensions,
  error,
  onRefresh,
  notify,
}: {
  extensions: ExtensionView[];
  error: string;
  onRefresh: () => Promise<void>;
  notify: (message: string) => void;
}) {
  const [extensionManifest, setExtensionManifest] = useState('');
  const [extensionBusy, setExtensionBusy] = useState(false);
  const [catalog, setCatalog] = useState<CatalogExtension[]>([]);

  useEffect(() => {
    let alive = true;
    void apiRequest<{ extensions: CatalogExtension[] }>('/extensions/catalog')
      .then(resp => {
        if (alive) setCatalog(resp.extensions || []);
      })
      .catch(() => {
        // Catalog is optional in minimal test harnesses.
      });
    return () => {
      alive = false;
    };
  }, []);

  async function installManifest(manifest: unknown) {
    setExtensionBusy(true);
    try {
      await apiRequest('/extensions/install', {
        method: 'POST',
        body: json({ manifest, activate: true }),
      });
      await onRefresh();
      notify('Verified declarative extension release installed.');
    } catch (err) {
      notify((err as Error).message);
    } finally {
      setExtensionBusy(false);
    }
  }

  async function installExtension() {
    try {
      const manifest = JSON.parse(extensionManifest) as unknown;
      await installManifest(manifest);
      setExtensionManifest('');
    } catch (err) {
      notify((err as Error).message);
    }
  }

  async function activateExtension(item: ExtensionView) {
    try {
      await apiRequest(`/extensions/${item.manifest.id}/activate`, {
        method: 'POST',
        body: json({ version: item.manifest.version }),
      });
      await onRefresh();
      notify(`Activated ${item.manifest.name} v${item.manifest.version}.`);
    } catch (err) {
      notify((err as Error).message);
    }
  }

  async function rollbackExtension(item: ExtensionView) {
    try {
      await apiRequest(`/extensions/${item.manifest.id}/rollback`, {
        method: 'POST',
        body: json({}),
      });
      await onRefresh();
      notify(`Rolled back ${item.manifest.name}.`);
    } catch (err) {
      notify((err as Error).message);
    }
  }

  return (
    <details className="auto-advanced extension-details">
      <summary>
        Extensions &amp; connectors <small>advanced</small>
      </summary>
      <div className="auto-advanced-body">
        <section className="automation-history extension-lifecycle">
          <div className="panel-heading">
            <div>
              <h3>Installed extension releases</h3>
              <p>
                Retained declarative releases with SHA-256 integrity checks and explicit permissions. No untrusted
                extension code is executed.
              </p>
            </div>
            <span className="state-pill enabled">
              {extensions.filter(item => item.active).length} active
            </span>
          </div>
          {error ? (
            <p className="form-error">{error}</p>
          ) : extensions.length === 0 ? (
            <p className="muted-copy">No extension releases installed. The built-in blocks remain available.</p>
          ) : (
            extensions.map(item => (
              <div className="extension-row" key={`${item.manifest.id}:${item.manifest.version}`}>
                <div>
                  <b>
                    {item.manifest.name} · v{item.manifest.version}
                  </b>
                  <small>
                    {item.manifest.id} · {item.manifest.publisher} · {item.manifest.blocks?.length || 0} typed blocks
                  </small>
                  {item.diagnostic && <small className="form-error">{item.diagnostic}</small>}
                </div>
                <div className="drive-actions">
                  <span
                    className={
                      'state-pill ' + (item.active ? 'enabled' : item.compatible ? 'ready' : 'failed')
                    }
                  >
                    {item.status}
                  </span>
                  {item.compatible && !item.active && (
                    <button className="text-btn" onClick={() => void activateExtension(item)}>
                      Activate
                    </button>
                  )}
                  {item.active && (
                    <button className="text-btn" onClick={() => void rollbackExtension(item)}>
                      Rollback
                    </button>
                  )}
                </div>
              </div>
            ))
          )}
        </section>

        {catalog.length > 0 && (
          <section className="automation-history extension-lifecycle" aria-label="Extension marketplace catalog">
            <div className="panel-heading">
              <div>
                <h3>Extension marketplace</h3>
                <p>Integrity-verified versioned releases, including older compatible versions for safe pinning.</p>
              </div>
            </div>
            {catalog.map(entry => {
              const installed = extensions.some(
                item => item.manifest.id === entry.id && item.manifest.version === entry.version,
              );
              return (
                <div className="extension-row" key={`${entry.id}:${entry.version}`}>
                  <div>
                    <b>
                      {entry.name} · v{entry.version}
                    </b>
                    <small>
                      {entry.publisher}
                      {entry.externalApplication && entry.externalVersion
                        ? ` · ${entry.externalApplication} ${entry.externalVersion}`
                        : ''}
                      {entry.integrity ? ' · SHA-256 verified' : ''}
                    </small>
                    {entry.permissions?.length ? (
                      <small>Permissions: {entry.permissions.join(', ')}</small>
                    ) : null}
                  </div>
                  <div className="drive-actions">
                    <button
                      type="button"
                      className="text-btn"
                      disabled={extensionBusy || installed}
                      onClick={() => void installManifest(entry)}
                    >
                      {installed ? 'Installed' : 'Install'}
                    </button>
                  </div>
                </div>
              );
            })}
          </section>
        )}

        <ImmichConnector notify={notify} />

        <section className="automation-history extension-install">
          <h3>Install custom declarative manifest</h3>
          <p className="muted-copy">
            Paste a reviewed manifest JSON. Unapproved permissions, mismatched SHA-256 digests, and executable configs
            are rejected.
          </p>
          <textarea
            aria-label="Extension manifest JSON"
            rows={5}
            value={extensionManifest}
            onChange={event => setExtensionManifest(event.target.value)}
            placeholder={
              '{"id":"example","name":"Example blocks","publisher":"Your team","version":"1.0.0","extensionApiRange":{"minimum":"1.0.0","maximumExclusive":"2.0.0"},"configurationSchema":1,"blocks":[]}'
            }
          />
          <button
            className="secondary"
            disabled={!extensionManifest.trim() || extensionBusy}
            onClick={() => void installExtension()}
          >
            {extensionBusy ? 'Installing…' : 'Install and activate release'}
          </button>
        </section>
      </div>
    </details>
  );
}
