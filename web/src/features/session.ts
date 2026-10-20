export type Session = {
  authenticated: boolean;
  username?: string;
  role?: 'admin' | 'user' | 'guest';
  guest?: boolean;
  anonymousEnabled?: boolean;
  guestResultExpirationHours?: number;
  guestMaxBatchFiles?: number;
  canConvert?: boolean;
  canCompress?: boolean;
  canReadJobs?: boolean;
  permissions?: Record<string, boolean>;
};

export type APIRequestError = Error & { code?: string; status?: number };

export const apiRequest = async <T,>(path: string, init: RequestInit = {}): Promise<T> => {
  const response = await fetch('/api/v1' + path, {
    ...init,
    credentials: 'include',
    headers: {
      ...(init.body instanceof FormData ? {} : { 'Content-Type': 'application/json' }),
      ...(init.headers || {}),
    },
  });
  if (response.status === 204) return undefined as T;
  const data = await response.json().catch(() => ({}));
  if (!response.ok) {
    const error = new Error(data?.error?.message || data?.message || 'The request could not be completed. Try again.') as APIRequestError;
    error.code = data?.error?.code;
    error.status = response.status;
    throw error;
  }
  return data as T;
};

export const guestRequestHeaders = (guest: boolean): Record<string, string> =>
  guest ? { 'X-101conv-Request': 'guest-session' } : {};

export const createGuestSession = () =>
  apiRequest<Session>('/auth/guest-session', {
    method: 'POST',
    body: JSON.stringify({}),
    headers: guestRequestHeaders(true),
  });

export const uploadWithProgress = <T,>(file: File, onProgress: (percent: number) => void, guest = false, endpoint = '/uploads'): Promise<T> =>
  new Promise((resolve, reject) => {
    const form = new FormData();
    form.append('file', file);
    const request = new XMLHttpRequest();
    request.open('POST', '/api/v1' + endpoint);
    request.withCredentials = true;
    request.upload.onprogress = event => {
      if (event.lengthComputable) onProgress(Math.round(event.loaded / event.total * 100));
    };
    request.onerror = () => reject(new Error('Upload connection failed. Check your connection and try again.'));
    request.onabort = () => reject(new Error('Upload was interrupted.'));
    request.onload = () => {
      let data: any = {};
      try {
        data = JSON.parse(request.responseText || '{}');
      } catch {
        reject(new Error('The server returned an invalid upload response.'));
        return;
      }
      if (request.status < 200 || request.status >= 300) {
        reject(new Error(data?.error?.message || data?.message || 'The upload could not be completed.'));
        return;
      }
      resolve(data as T);
    };
    for (const [name, value] of Object.entries(guestRequestHeaders(guest))) request.setRequestHeader(name, value);
    request.send(form);
  });

export const explainGuestStart = (error: unknown, fallback: (message?: string) => string): string => {
  const requestError = error as APIRequestError;
  if (requestError.code === 'guest_disabled') return 'Guest access is disabled. Sign in, enable Anonymous access in Admin settings, then return here.';
  if (requestError.code === 'csrf_rejected') return 'The guest request did not match this site address. Open 101conv directly and try again.';
  if (requestError.code === 'guest_rate_limited') return 'Too many guest sessions were started. Wait a minute and try again.';
  return fallback(requestError.message);
};
