import type { ReactNode } from 'react';
import { Navigate, useLocation } from 'react-router';
import { FullScreenMessage } from '../components/FullScreenMessage';
import { errorMessage } from '../api/client';
import { useAuth } from './AuthProvider';

// 로그인하지 않았으면 원래 가려던 경로를 next로 들고 로그인 화면으로 보낸다
export function RequireAuth({ children }: { children: ReactNode }) {
  const auth = useAuth();
  const loc = useLocation();
  if (auth.status === 'loading') return <FullScreenMessage busy title="불러오는 중" />;
  if (auth.status === 'error')
    return (
      <FullScreenMessage
        title="서버에 연결하지 못했어요"
        body={errorMessage(auth.error)}
        action={{ label: '다시 시도', onClick: auth.refresh }}
      />
    );
  if (auth.status === 'anonymous') {
    const next = loc.pathname + loc.search;
    return <Navigate to={`/login?next=${encodeURIComponent(next)}`} replace />;
  }
  return <>{children}</>;
}
