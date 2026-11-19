import { useState } from 'react';
import { json } from '../format';
import { apiRequest } from '../session';
import type { ExtensionView } from './types';

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

  async function installExtension() {
    setExtensionBusy(true);
    try {
      const manifest = JSON.parse(extensionManifest) as unknown;
      await apiRequest('/extensions/install', {
        method: 'POST',
        body: json({ manifest, activate: true }),
      });
      setExtensionManifest('');
      await onRefresh();
      notify('Declarative extension release installed.');
    } catch (err) {
      notify((err as Error).message);
    } finally {
      setExtensionBusy(false);
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
        Extension releases <small>advanced</small>
      </summary>
      <div className="auto-advanced-body">
        <section className="automation-history extension-lifecycle">
          <div className="panel-heading">
            <div>
              <h3>Extension releases</h3>
              <p>Only reviewed declarative metadata and typed blocks are retained here; no extension code is executed.</p>
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

        <section className="automation-history extension-install">
          <h3>Install a declarative release</h3>
          <p className="muted-copy">
            Paste a manifest from a reviewed source. Package downloads, arbitrary commands, and runtime code are
            intentionally unsupported.
          </p>
          <textarea
            aria-label="Extension manifest JSON"
            rows={6}
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
