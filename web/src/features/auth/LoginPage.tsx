import { useState, type FormEvent } from 'react';
import { ArrowRight, LoaderCircle } from 'lucide-react';
import { explain, json } from '../format';
import { apiRequest, type Session } from '../session';

export function LoginPage({
  onLogin,
  notify: _notify,
}: {
  onLogin: (session: Session) => void;
  notify?: (message: string) => void;
}) {
  const [error, setError] = useState('');
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [busy, setBusy] = useState(false);

  async function submit(event: FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError('');
    try {
      const session = await apiRequest<Session>('/auth/session', {
        method: 'POST',
        body: json(username.trim() ? { username, password } : { password }),
      });
      onLogin(session);
    } catch (err) {
      setError(explain((err as Error).message));
    } finally {
      setBusy(false);
    }
  }

  return (
    <main className="login-page">
      <div className="login-card">
        <a className="brand login-brand" href="/">
          <span className="brand-mark">101</span>
          <span>101conv</span>
        </a>
        <h1>Sign in</h1>
        <p className="login-copy">
          Enter your username and password. Leave the username blank to sign in as administrator.
        </p>
        <form onSubmit={submit}>
          <label htmlFor="login-user">
            User name <span>(optional for administrator)</span>
          </label>
          <input
            id="login-user"
            autoComplete="username"
            value={username}
            onChange={event => setUsername(event.target.value)}
            autoFocus
          />
          <label htmlFor="admin-pass">Password</label>
          <input
            id="admin-pass"
            type="password"
            autoComplete="current-password"
            value={password}
            onChange={event => setPassword(event.target.value)}
            required
          />
          {error && (
            <p className="form-error" role="alert">
              {error}
            </p>
          )}
          <button className="primary full" disabled={busy}>
            {busy ? <LoaderCircle className="spin" /> : <ArrowRight size={17} />} Sign in
          </button>
        </form>
      </div>
    </main>
  );
}
