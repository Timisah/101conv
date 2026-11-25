import { CircleAlert, LockKeyhole } from 'lucide-react';
import { ThemePicker } from '../../components/ThemePicker';
import { ConvertPage } from '../convert/ConvertPage';
import { DemoBanner } from '../demo/DemoBanner';
import type { Session } from '../session';

export function VisitorConversion({
  session,
  guestError,
  guestStarting,
  onRetryGuest,
  onLogin,
  onNotify,
}: {
  session: Session;
  guestError: string;
  guestStarting: boolean;
  onRetryGuest: () => void;
  onLogin: () => void;
  onNotify: (message: string) => void;
}) {
  return (
    <div className="visitor-shell">
      <header className="visitor-topbar">
        <a className="brand" href="/">
          <span className="brand-mark">101</span>
          <span>101conv</span>
        </a>
        <div className="top-actions">
          <ThemePicker compact />
          <button className="secondary visitor-login" onClick={onLogin}>
            Log in
          </button>
        </div>
      </header>
      <main className="visitor-convert-main">
        <DemoBanner />
        {guestError ? (
          <div className="banner error visitor-access-message" role="alert">
            <CircleAlert size={18} />
            <div>
              <b>Guest session could not start</b>
              <span>{guestError}</span>
            </div>
            <button className="secondary" onClick={onRetryGuest} disabled={guestStarting}>
              {guestStarting ? 'Retrying…' : 'Retry'}
            </button>
          </div>
        ) : !session.anonymousEnabled ? (
          <div className="banner visitor-access-message">
            <LockKeyhole size={18} />
            <div>
              <b>Sign in required</b>
              <span>Anonymous conversions are disabled for this instance.</span>
            </div>
            <button className="secondary" onClick={onLogin}>
              Log in
            </button>
          </div>
        ) : null}
        <ConvertPage notify={onNotify} session={session} />
      </main>
    </div>
  );
}
