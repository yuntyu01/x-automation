import { createBrowserRouter, Outlet } from 'react-router';
import { RequireAuth } from './auth/RequireAuth';
import { LoginPage } from './pages/LoginPage';
import { FullScreenMessage } from './components/FullScreenMessage';

function Protected() {
  return (
    <RequireAuth>
      <Outlet />
    </RequireAuth>
  );
}

function Placeholder() {
  return <FullScreenMessage title="로그인되었습니다" body="앱 화면은 다음 단계에서 연결됩니다." />;
}

export const router = createBrowserRouter([
  { path: '/login', element: <LoginPage /> },
  { element: <Protected />, children: [{ path: '*', element: <Placeholder /> }] },
]);
