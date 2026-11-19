import { useState, type FormEvent } from 'react';
import { json } from '../format';
import { apiRequest, type Session } from '../session';

export function SetupClaim({ onClaim }: { onClaim: (session: Session) => void }) {
  const [token, setToken] = useState('');
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  async function submit(event: FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError('');
    try {
      const session = await apiRequest<Session>('/setup/claim', {
        method: 'POST',
        body: json({ bootstrapToken: token, username, password }),
      });
      onClaim(session);
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setBusy(false);
    }
  }

  return (
    <main className="setup-page">
      <section className="setup-card">
        <div className="setup-brand">
          <span className="brand-mark">101</span>
          <b>101conv</b>
        </div>
        <h1>Create your administrator account</h1>
        <p>
          Use the bootstrap passphrase from your installation settings to claim this instance. It proves you control
          the deployment and is separate from your administrator password.
        </p>
        <form onSubmit={submit}>
          <label className="field-label">
            Bootstrap passphrase
            <input
              type="password"
              autoComplete="off"
              required
              value={token}
              onChange={event => setToken(event.target.value)}
            />
          </label>
          <label className="field-label">
            Administrator username
            <input
              autoComplete="username"
              minLength={3}
              maxLength={64}
              required
              value={username}
              onChange={event => setUsername(event.target.value)}
            />
          </label>
          <label className="field-label">
            New password
            <input
              type="password"
              autoComplete="new-password"
              minLength={5}
              maxLength={256}
              required
              value={password}
              onChange={event => setPassword(event.target.value)}
            />
            <small>Five characters are accepted for compatibility; use a long passphrase.</small>
          </label>
          {error && (
            <p className="form-error" role="alert">
              {error}
            </p>
          )}
          <button className="primary full" disabled={busy}>
            {busy ? 'Creating account…' : 'Create administrator'}
          </button>
        </form>
        <p className="setup-help">
          Local install: read <code>ENGINE_ADMIN_TOKEN</code> from the generated <code>.env</code> file. Portainer: use
          the stack variable with that name. Setup can be resumed by signing in with this account.
        </p>
      </section>
    </main>
  );
}
