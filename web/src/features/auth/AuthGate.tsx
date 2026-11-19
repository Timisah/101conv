import type { ReactNode } from 'react';
import { Navigate, useLocation, useNavigate } from 'react-router-dom';
import { LoaderCircle } from 'lucide-react';
import { LoginPage } from './LoginPage';
import { SetupClaim } from './SetupClaim';
import { SetupReview } from './SetupReview';
import { VisitorConversion } from './VisitorConversion';
import type { useAppSession } from './useAppSession';

export type AppSessionState = ReturnType<typeof useAppSession>;

export function AuthGate({ app, children }: { app: AppSessionState; children: ReactNode }) {
  const {
    session,
    setSession,
    sessionReady,
    setupStatus,
    setSetupStatus,
    setupError,
    guestStartError,
    guestStarting,
    startGuest,
    setToast,
  } = app;
  const location = useLocation();
  const navigate = useNavigate();

  if (!sessionReady || (!setupStatus && !setupError)) {
    return (
      <div className="splash">
        <div className="brand-mark">101</div>
        <LoaderCircle className="spin" />
      </div>
    );
  }

  if (setupError) {
    return (
      <main className="login-page">
        <div className="login-card">
          <h1>Setup unavailable</h1>
          <p>{setupError}</p>
          <button className="primary" onClick={() => window.location.reload()}>
            Retry
          </button>
        </div>
      </main>
    );
  }

  if (setupStatus && !setupStatus.completed && !setupStatus.administratorCreated) {
    return (
      <SetupClaim
        onClaim={claimed => {
          setSession(claimed);
          setSetupStatus({ completed: false, administratorCreated: true });
        }}
      />
    );
  }

  if (!session.authenticated) {
    if (location.pathname === '/login') {
      return <LoginPage onLogin={setSession} notify={setToast} />;
    }
    if (session.anonymousEnabled) {
      return (
        <VisitorConversion
          session={session}
          guestError={guestStartError}
          guestStarting={guestStarting}
          onRetryGuest={startGuest}
          onLogin={() => navigate('/login')}
          onNotify={setToast}
        />
      );
    }
    return <Navigate to="/login" replace />;
  }

  if (setupStatus && !setupStatus.completed) {
    return (
      <SetupReview
        onComplete={() => setSetupStatus({ completed: true, administratorCreated: true })}
      />
    );
  }

  return <>{children}</>;
}
