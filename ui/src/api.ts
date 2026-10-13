export class ApiError extends Error {
  data: unknown;
  status: number;
  constructor(message: string, status: number, data: unknown) {
    super(message);
    this.status = status;
    this.data = data;
  }
}

export interface ApiResponse<T> {
  data: T;
  headers: Headers;
}

/**
 * Calls the dashboard API. Strings are sent as-is (rules YAML), anything else
 * as JSON. The X-Requested-With header is the server's CSRF guard.
 */
export async function api<T = any>(method: string, path: string, body?: unknown, type?: string): Promise<ApiResponse<T>> {
  const opts: RequestInit = { method, headers: { 'X-Requested-With': 'grepalert' } };
  if (body !== undefined) {
    opts.body = typeof body === 'string' ? body : JSON.stringify(body);
    (opts.headers as Record<string, string>)['Content-Type'] = type || 'application/json';
  }
  // Absolute URL from origin: a page opened as http://user:pass@host/ would
  // otherwise resolve relative URLs with credentials, which fetch() rejects.
  const r = await fetch(location.origin + path, opts);
  const ct = r.headers.get('Content-Type') || '';
  const data = ct.includes('json') ? await r.json() : await r.text();
  if (r.status === 401 && !path.startsWith('/auth/')) {
    location.href = '/login?next=' + encodeURIComponent(location.pathname + location.hash);
    throw new ApiError('signed out', 401, data);
  }
  if (!r.ok) {
    const msg = (data && typeof data === 'object' && 'error' in data ? String((data as { error: unknown }).error) : '') || (typeof data === 'string' && data) || 'HTTP ' + r.status;
    throw new ApiError(msg, r.status, data);
  }
  return { data: data as T, headers: r.headers };
}

export const get = <T = any>(path: string) => api<T>('GET', path).then(r => r.data);
export const enc = encodeURIComponent;
export const errText = (e: unknown) => (e instanceof Error ? e.message : String(e));
