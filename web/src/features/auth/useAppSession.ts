import { useCallback, useEffect, useState } from 'react';
import type { UpdateStatus } from '../admin/InstanceSettingsPage';
import { explain } from '../format';
import {
  apiRequest,
  createGuestSession,
  explainGuestStart,
  guestRequestHeaders,
  type Session,
} from '../session';
import type { SetupStatus } from './types';

export function useAppSession() {
  const [session, setSession] = useState<Session>({ authenticated: false });
  const [sessionReady, setSessionReady] = useState(false);
  const [setupStatus, setSetupStatus] = useState<SetupStatus | null>(null);
  const [setupError, setSetupError] = useState('');
  const [guestStartError, setGuestStartError] = useState('');
  const [guestStarting, setGuestStarting] = useState(false);
  const [coreInfo, setCoreInfo] = useState<{ ready: boolean | null; version: string }>({
    ready: null,
    version: '',
  });
  const [updateStatus, setUpdateStatus] = useState<UpdateStatus | null>(null);
  const [toast, setToast] = useState('');

  const refreshVisitorSession = async () => {
    try {
      const current = await apiRequest<Session>('/auth/session');
      if (!current.authenticated) setSession(current);
      return current;
    } catch {
      return null;
    }
  };

  const startGuest = async () => {
    if (guestStarting) return;
    setGuestStarting(true);
    setGuestStartError('');
    try {
      const guest = await createGuestSession();
      guest.anonymousEnabled = true;
      setSession(guest);
    } catch (err) {
      setGuestStartError(explainGuestStart(err, explain));
      await refreshVisitorSession();
    } finally {
      setGuestStarting(false);
    }
  };

  useEffect(() => {
    let live = true;
    apiRequest<Session>('/auth/session')
      .then(async current => {
        let next = current;
        if (!next.authenticated) {
          try {
            next = await createGuestSession();
            next.anonymousEnabled = true;
          } catch (err) {
            setGuestStartError(explainGuestStart(err, explain));
            try {
              next = await apiRequest<Session>('/auth/session');
            } catch {
              /* keep last known state and surface guest-start error */
            }
          }
        }
        if (live) setSession(next);
      })
      .catch(() => {
        if (live) setSession({ authenticated: false });
      })
      .finally(() => {
        if (live) setSessionReady(true);
      });
    return () => {
      live = false;
    };
  }, []);

  useEffect(() => {
    apiRequest<SetupStatus>('/setup/status')
      .then(setSetupStatus)
      .catch(err => setSetupError(explain((err as Error).message)));
  }, []);

  useEffect(() => {
    let live = true;
    const check = () =>
      fetch('/readyz', { cache: 'no-store' })
        .then(res => (res.ok ? res.json() : null))
        .then(data => {
          if (live) {
            setCoreInfo({
              ready: data?.status === 'ready',
              version: typeof data?.version === 'string' ? data.version : '',
            });
          }
        })
        .catch(() => {
          if (live) setCoreInfo(info => ({ ...info, ready: false }));
        });
    void check();
    const timer = setInterval(check, 30000);
    return () => {
      live = false;
      clearInterval(timer);
    };
  }, []);

  const refreshUpdate = useCallback(async () => {
    if (session.role !== 'admin') return;
    try {
      setUpdateStatus(await apiRequest<UpdateStatus>('/system/update'));
    } catch {
      setUpdateStatus(null);
    }
  }, [session.role]);

  useEffect(() => {
    if (session.role !== 'admin') {
      setUpdateStatus(null);
      return;
    }
    void refreshUpdate();
    const timer = setInterval(() => void refreshUpdate(), 12 * 60 * 60 * 1000);
    return () => clearInterval(timer);
  }, [refreshUpdate, session.role]);

  useEffect(() => {
    if (!toast) return;
    const timer = setTimeout(() => setToast(''), 4000);
    return () => clearTimeout(timer);
  }, [toast]);

  const logout = async () => {
    try {
      await apiRequest(session.guest ? '/auth/guest-session' : '/auth/session', {
        method: 'DELETE',
        ...(session.guest ? { headers: guestRequestHeaders(true) } : {}),
      });
    } finally {
      if (session.guest) {
        setSession({ authenticated: false, anonymousEnabled: session.anonymousEnabled });
        return;
      }
      setGuestStarting(true);
      try {
        const guest = await createGuestSession();
        guest.anonymousEnabled = true;
        setSession(guest);
      } catch (err) {
        setGuestStartError(explainGuestStart(err, explain));
        const current = await refreshVisitorSession();
        if (!current) setSession({ authenticated: false });
      } finally {
        setGuestStarting(false);
      }
    }
  };

  return {
    session,
    setSession,
    sessionReady,
    setupStatus,
    setSetupStatus,
    setupError,
    guestStartError,
    guestStarting,
    startGuest,
    coreInfo,
    updateStatus,
    refreshUpdate,
    toast,
    setToast,
    logout,
  };
}
