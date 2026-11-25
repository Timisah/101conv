import { useCallback, useEffect, useState } from 'react';
import { json } from '../format';
import { apiRequest } from '../session';

type ImmichConnectorView = {
  configured: boolean;
  baseUrl?: string;
  apiKeyConfigured: boolean;
  lastVersion?: string;
  compatible: boolean;
  lastError?: string;
  lastCheckedAt?: string;
  disclosure: string;
};

type ImmichAsset = {
  id: string;
  originalFileName: string;
  type: string;
  originalMimeType?: string;
  fileCreatedAt?: string;
};

export function ImmichConnector({ notify }: { notify: (message: string) => void }) {
  const [config, setConfig] = useState<ImmichConnectorView | null>(null);
  const [baseUrl, setBaseUrl] = useState('');
  const [apiKey, setApiKey] = useState('');
  const [busy, setBusy] = useState(false);
  const [testNote, setTestNote] = useState('');
  const [assets, setAssets] = useState<ImmichAsset[]>([]);
  const [assetsBusy, setAssetsBusy] = useState(false);

  const loadConfig = useCallback(async () => {
    try {
      const resp = await apiRequest<{ connector: ImmichConnectorView }>('/connectors/immich');
      setConfig(resp.connector);
      if (resp.connector.baseUrl) setBaseUrl(resp.connector.baseUrl);
    } catch {
      // Connector store may be unavailable in non-DB preview environments.
    }
  }, []);

  useEffect(() => {
    void loadConfig();
  }, [loadConfig]);

  async function saveConfig() {
    setBusy(true);
    setTestNote('');
    try {
      const resp = await apiRequest<{ connector: ImmichConnectorView }>('/connectors/immich', {
        method: 'PUT',
        body: json({ baseUrl, apiKey }),
      });
      setConfig(resp.connector);
      setApiKey('');
      notify('Immich connector configuration saved (API key stored server-side).');
    } catch (err) {
      notify((err as Error).message);
    } finally {
      setBusy(false);
    }
  }

  async function testConnection() {
    setBusy(true);
    setTestNote('');
    try {
      const resp = await apiRequest<{
        ok: boolean;
        version?: string;
        compatible: boolean;
        reason?: string;
        error?: string;
        pausedAutomations?: number;
        connector?: ImmichConnectorView;
      }>('/connectors/immich/test', { method: 'POST', body: json({}) });
      if (resp.connector) setConfig(resp.connector);
      if (!resp.ok) {
        setTestNote(
          `Connection failed: ${resp.error || 'unreachable'}${resp.pausedAutomations ? ` · paused ${resp.pausedAutomations} affected Immich automation(s)` : ''}`,
        );
      } else if (!resp.compatible) {
        setTestNote(
          `Immich v${resp.version} is incompatible (${resp.reason || 'version out of range'}).${resp.pausedAutomations ? ` Paused ${resp.pausedAutomations} affected Immich automation(s); other automations remain active.` : ''}`,
        );
      } else {
        setTestNote(`Connected to Immich v${resp.version} (compatible).`);
      }
    } catch (err) {
      setTestNote((err as Error).message);
    } finally {
      setBusy(false);
    }
  }

  async function loadAssets() {
    setAssetsBusy(true);
    try {
      const resp = await apiRequest<{ assets: ImmichAsset[] }>('/connectors/immich/assets?limit=15');
      setAssets(resp.assets || []);
    } catch (err) {
      notify((err as Error).message);
    } finally {
      setAssetsBusy(false);
    }
  }

  async function importAsset(asset: ImmichAsset) {
    try {
      await apiRequest('/connectors/immich/import', {
        method: 'POST',
        body: json({ assetId: asset.id, fileName: asset.originalFileName }),
      });
      notify(`Imported ${asset.originalFileName} into uploads.`);
    } catch (err) {
      notify((err as Error).message);
    }
  }

  return (
    <section className="automation-history extension-lifecycle" aria-label="Immich connector">
      <div className="panel-heading">
        <div>
          <h3>Immich connector</h3>
          <p>
            {config?.disclosure ||
              'Connect to an Immich server via documented REST APIs. Processed files are uploaded as new assets; in-place ID replacement and full album/face/tag preservation are not guaranteed.'}
          </p>
        </div>
        <span className={'state-pill ' + (config?.compatible ? 'enabled' : config?.configured ? 'ready' : 'failed')}>
          {config?.compatible ? `v${config.lastVersion || 'ok'}` : config?.configured ? 'Configured' : 'Not connected'}
        </span>
      </div>

      <label className="field-label">
        Immich server URL
        <input
          type="url"
          value={baseUrl}
          onChange={event => setBaseUrl(event.target.value)}
          placeholder="https://photos.example.local:2283"
        />
      </label>
      <label className="field-label">
        API key {config?.apiKeyConfigured ? '(saved — leave blank to keep current key)' : ''}
        <input
          type="password"
          autoComplete="off"
          value={apiKey}
          onChange={event => setApiKey(event.target.value)}
          placeholder={config?.apiKeyConfigured ? '••••••••••••' : 'Paste Immich API key'}
        />
      </label>
      <div className="drive-actions">
        <button type="button" className="secondary" disabled={busy || !baseUrl.trim()} onClick={() => void saveConfig()}>
          Save connector
        </button>
        <button type="button" className="text-btn" disabled={busy || !config?.configured} onClick={() => void testConnection()}>
          Test &amp; check version
        </button>
        <button type="button" className="text-btn" disabled={assetsBusy || !config?.configured} onClick={() => void loadAssets()}>
          {assetsBusy ? 'Loading…' : 'Browse recent assets'}
        </button>
      </div>
      {testNote && <p className={testNote.startsWith('Connected') ? 'field-hint' : 'form-error'}>{testNote}</p>}
      {config?.lastError && !testNote && <p className="form-error">{config.lastError}</p>}

      {assets.length > 0 && (
        <div className="automation-history">
          {assets.map(asset => (
            <div className="extension-row" key={asset.id}>
              <div>
                <b>{asset.originalFileName}</b>
                <small>
                  {asset.type}
                  {asset.originalMimeType ? ` · ${asset.originalMimeType}` : ''}
                </small>
              </div>
              <button type="button" className="text-btn" onClick={() => void importAsset(asset)}>
                Import original
              </button>
            </div>
          ))}
        </div>
      )}
    </section>
  );
}
