// API 클라이언트. 같은 origin 쿠키 세션 + 상태 변경 요청에 CSRF 헤더를 붙인다.

export type FieldErrors = Record<string, string>;

export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly fields: FieldErrors;
  readonly requestId?: string;
  readonly body?: unknown; // 충돌 시 서버의 현재 상태 등 추가 정보

  constructor(status: number, code: string, message: string, fields: FieldErrors = {}, requestId?: string, body?: unknown) {
    super(message);
    this.status = status;
    this.code = code;
    this.fields = fields;
    this.requestId = requestId;
    this.body = body;
  }
}

let csrfToken = '';
const unauthorizedListeners = new Set<() => void>();

export function setCsrfToken(token: string) {
  csrfToken = token;
}

export function onUnauthorized(fn: () => void): () => void {
  unauthorizedListeners.add(fn);
  return () => {
    unauthorizedListeners.delete(fn);
  };
}

type Method = 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE';

export interface RequestOptions {
  body?: unknown;
  signal?: AbortSignal;
  raw?: BodyInit; // FormData 등 (Content-Type은 브라우저가 정함)
  headers?: Record<string, string>;
}

export async function api<T>(method: Method, path: string, opts: RequestOptions = {}): Promise<T> {
  const headers: Record<string, string> = { Accept: 'application/json', ...opts.headers };
  let body: BodyInit | undefined = opts.raw;
  if (opts.body !== undefined) {
    headers['Content-Type'] = 'application/json';
    body = JSON.stringify(opts.body);
  }
  if (method !== 'GET' && csrfToken) {
    headers['X-CSRF-Token'] = csrfToken;
  }
  let res: Response;
  try {
    res = await fetch(path, { method, headers, body, credentials: 'same-origin', signal: opts.signal });
  } catch (e) {
    if (e instanceof DOMException && e.name === 'AbortError') throw e;
    // 네트워크 단절을 저장 성공으로 오해하지 않게 명확한 오류로 바꾼다
    throw new ApiError(0, 'network', '서버에 연결할 수 없습니다. 네트워크 상태를 확인해 주세요');
  }
  const text = await res.text();
  let data: unknown = null;
  if (text) {
    try {
      data = JSON.parse(text);
    } catch {
      data = null;
    }
  }
  if (!res.ok) {
    const err = (data as { error?: { code?: string; message?: string; fields?: FieldErrors; request_id?: string } } | null)
      ?.error;
    if (res.status === 401 && !path.startsWith('/api/auth/')) {
      unauthorizedListeners.forEach((fn) => fn());
    }
    throw new ApiError(
      res.status,
      err?.code ?? 'http_' + res.status,
      err?.message ?? '요청을 처리하지 못했습니다',
      err?.fields ?? {},
      err?.request_id,
      data,
    );
  }
  return data as T;
}

export const get = <T>(path: string, signal?: AbortSignal) => api<T>('GET', path, { signal });
export const post = <T>(path: string, body?: unknown) => api<T>('POST', path, { body });
export const put = <T>(path: string, body?: unknown) => api<T>('PUT', path, { body });
export const patch = <T>(path: string, body?: unknown) => api<T>('PATCH', path, { body });
export const del = <T>(path: string, body?: unknown) => api<T>('DELETE', path, { body });

export function errorMessage(e: unknown): string {
  if (e instanceof ApiError) return e.message;
  if (e instanceof Error) return e.message;
  return '알 수 없는 오류가 발생했습니다';
}
