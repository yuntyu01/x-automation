import { useQuery, useQueryClient } from '@tanstack/react-query';
import { createContext, useCallback, useContext, useEffect, useMemo, type ReactNode } from 'react';
import { ApiError, get, onUnauthorized, post, setCsrfToken } from '../api/client';

export interface SessionInfo {
  authenticated: boolean;
  username?: string;
  csrf_token?: string;
  expires_at?: string;
}

interface AuthState {
  status: 'loading' | 'authenticated' | 'anonymous' | 'error';
  session?: SessionInfo;
  error?: unknown;
  login: (username: string, password: string) => Promise<void>;
  logout: () => Promise<void>;
  refresh: () => void;
}

const AuthContext = createContext<AuthState | null>(null);
const SESSION_KEY = ['auth', 'session'] as const;

async function fetchSession(): Promise<SessionInfo> {
  try {
    return await get<SessionInfo>('/api/auth/session');
  } catch (e) {
    if (e instanceof ApiError && e.status === 401) return { authenticated: false };
    throw e;
  }
}

export function AuthProvider({ children }: { children: ReactNode }) {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: SESSION_KEY, queryFn: fetchSession, staleTime: 60_000, retry: 1 });

  useEffect(() => {
    setCsrfToken(q.data?.csrf_token ?? '');
  }, [q.data]);

  // 다른 API가 401을 받으면 세션을 다시 확인해 로그인 화면으로 보낸다
  useEffect(
    () =>
      onUnauthorized(() => {
        qc.setQueryData(SESSION_KEY, { authenticated: false });
      }),
    [qc],
  );

  const login = useCallback(
    async (username: string, password: string) => {
      const s = await post<SessionInfo>('/api/auth/login', { username, password });
      setCsrfToken(s.csrf_token ?? '');
      qc.setQueryData(SESSION_KEY, s);
    },
    [qc],
  );

  const logout = useCallback(async () => {
    try {
      await post('/api/auth/logout');
    } catch (e) {
      // 세션이 이미 끝났으면(401) 화면도 로그아웃한다. 그 밖의 실패는 서버 세션이 살아 있으니
      // 화면만 로그아웃한 척하지 않고 알린다 (다시 열면 그대로 로그인돼 있게 되므로)
      if (!(e instanceof ApiError && e.status === 401)) throw e;
    }
    setCsrfToken('');
    // 세션 쿼리는 지우지 않고 값만 바꾼다: 지우면 화면의 세션 구독이 새 값을 받지 못해 로그인 화면으로 넘어가지 않는다
    qc.setQueryData(SESSION_KEY, { authenticated: false });
    qc.removeQueries({ predicate: (query) => query.queryKey[0] !== SESSION_KEY[0] });
  }, [qc]);

  const value = useMemo<AuthState>(() => {
    let status: AuthState['status'] = 'loading';
    if (q.isError) status = 'error';
    else if (q.data) status = q.data.authenticated ? 'authenticated' : 'anonymous';
    return { status, session: q.data, error: q.error, login, logout, refresh: () => void q.refetch() };
  }, [q, login, logout]);

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuth(): AuthState {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error('AuthProvider가 필요합니다');
  return ctx;
}
