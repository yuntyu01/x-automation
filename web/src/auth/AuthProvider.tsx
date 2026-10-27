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
    } finally {
      setCsrfToken('');
      qc.clear();
      qc.setQueryData(SESSION_KEY, { authenticated: false });
    }
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
