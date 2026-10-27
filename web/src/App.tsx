import { createBrowserRouter, Outlet, ScrollRestoration } from 'react-router';
import { RequireAuth } from './auth/RequireAuth';
import { AppShell } from './components/layout/AppShell';
import { ToastProvider } from './components/ui/Toast';
import { ContentEditorPage } from './pages/contents/ContentEditorPage';
import { ContentsPage } from './pages/contents/ContentsPage';
import { HomePage } from './pages/home/HomePage';
import { LoginPage } from './pages/LoginPage';
import { NotFoundPage } from './pages/NotFoundPage';
import { NotificationsPage } from './pages/NotificationsPage';
import { PromptEditorPage } from './pages/prompts/PromptEditorPage';
import { PromptsPage } from './pages/prompts/PromptsPage';
import { SchedulePage } from './pages/schedule/SchedulePage';
import { ConnectionsPage } from './pages/settings/ConnectionsPage';
import { SettingsPage } from './pages/settings/SettingsPage';
import { SetupPage } from './pages/setup/SetupPage';

function Root() {
  return (
    <ToastProvider>
      <Outlet />
      {/* 탭 복귀·뒤로 가기 때 스크롤 위치 복원 */}
      <ScrollRestoration />
    </ToastProvider>
  );
}

function Protected() {
  return (
    <RequireAuth>
      <AppShell />
    </RequireAuth>
  );
}

export const router = createBrowserRouter([
  {
    element: <Root />,
    children: [
      { path: '/login', element: <LoginPage /> },
      {
        element: <Protected />,
        children: [
          { path: '/', element: <HomePage /> },
          { path: '/contents', element: <ContentsPage /> },
          { path: '/contents/:id', element: <ContentEditorPage /> },
          { path: '/prompts', element: <PromptsPage /> },
          { path: '/prompts/:id', element: <PromptEditorPage /> },
          { path: '/schedule', element: <SchedulePage /> },
          { path: '/settings', element: <SettingsPage /> },
          { path: '/settings/connections', element: <ConnectionsPage /> },
          { path: '/notifications', element: <NotificationsPage /> },
          { path: '/setup', element: <SetupPage /> },
          { path: '*', element: <NotFoundPage /> },
        ],
      },
    ],
  },
]);
