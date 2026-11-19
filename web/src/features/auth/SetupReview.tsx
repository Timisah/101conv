import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { ErrorState, Loading } from '../../components/common';
import { apiRequest } from '../session';
import { useData } from '../useData';
import type { SetupReadiness } from './types';

export function SetupReview({ onComplete }: { onComplete: () => void }) {
  const { data, loading, error, refresh } = useData<SetupReadiness>('/setup/readiness', {
    storage: [],
    storageReady: false,
    installedEngines: 0,
    installedProfiles: 0,
    retention: 'manual',
    workerWorkspaceLimitBytes: 1 << 30,
  });
  const [busy, setBusy] = useState(false);
  const [submitError, setSubmitError] = useState('');
  const navigate = useNavigate();

  async function finish() {
    setBusy(true);
    setSubmitError('');
    try {
      await apiRequest('/setup/complete', { method: 'POST' });
      onComplete();
      navigate(data.installedProfiles ? '/convert' : '/marketplace');
    } catch (err) {
      setSubmitError((err as Error).message);
    } finally {
      setBusy(false);
    }
  }

  return (
    <main className="setup-page">
      <section className="setup-card setup-review">
        <div className="setup-brand">
          <span className="brand-mark">101</span>
          <b>101conv</b>
        </div>
        <h1>Check storage and start converting</h1>
        <p>
          Configuration and engines use the application data mount. Originals, results, and PostgreSQL use separate
          persistent volumes. The Worker has a 1 GiB temporary workspace per container.
        </p>
        {error ? (
          <ErrorState message={error} retry={refresh} />
        ) : loading ? (
          <Loading />
        ) : (
          <>
            <div className="setup-checks">
              {data.storage.map(item => (
                <div key={item.name}>
                  <div>
                    <b>{item.name}</b>
                    <small>{item.path} · container path</small>
                  </div>
                  <strong className={item.writable ? 'check-ok' : 'check-error'}>
                    {item.writable ? 'Writable' : 'Needs attention'}
                  </strong>
                  {item.issue && <small>{item.issue}</small>}
                </div>
              ))}
            </div>
            <p className="setup-help">
              Storage and database retention default to manual cleanup; no originals or results are deleted
              automatically. For a host bind permission problem, check your Portainer <code>GO_DATA_PATH</code> (or{' '}
              <code>DATA_PATH/go-data</code>) and give that directory to UID/GID 65532 on the Docker host.
            </p>
            <div className="setup-counts">
              <span>
                <b>{data.installedEngines}</b> installed engines
              </span>
              <span>
                <b>{data.installedProfiles}</b> installed profiles
              </span>
            </div>
            <p>
              {data.installedProfiles
                ? 'You can open Convert after setup.'
                : 'After setup, open the catalog and review an engine and its profile before installing. The catalog shows source, license, size, and trust information when available; unsigned installs require your approval.'}
            </p>
            {submitError && (
              <p className="form-error" role="alert">
                {submitError}
              </p>
            )}
            <button
              className="primary full"
              disabled={!data.storageReady || busy}
              onClick={() => void finish()}
            >
              {busy
                ? 'Finishing…'
                : data.installedProfiles
                  ? 'Finish setup and convert'
                  : 'Finish setup and open catalog'}
            </button>
          </>
        )}
      </section>
    </main>
  );
}
