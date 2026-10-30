import { createBrowserRouter, Outlet, ScrollRestoration } from 'react-router';
import { RequireAuth } from './auth/RequireAuth';
import { AppShell } from './components/layout/AppShell';
import { ToastProvider } from './components/ui/Toast';
import { ContentEditorPage } from './pages/contents/ContentEditorPage';
import { ContentsPage } from './pages/contents/ContentsPage';
import { HomePage } from './pages/home/HomePage';
import { ImagesPage } from './pages/images/ImagesPage';
import { LoginPage } from './pages/LoginPage';
import { NotFoundPage } from './pages/NotFoundPage';
import { NotificationsPage } from './pages/NotificationsPage';
import { AiCostsPage } from './pages/AiCostsPage';
import { KeysPage } from './pages/settings/KeysPage';
import { PromptEditorPage } from './pages/prompts/PromptEditorPage';
import { PromptsPage } from './pages/prompts/PromptsPage';
import { ScheduleRulesPage } from './pages/schedule/ScheduleRulesPage';
import { PublishPage } from './pages/publish/PublishPage';
import { SchedulePage } from './pages/schedule/SchedulePage';
import { ConnectionsPage } from './pages/settings/ConnectionsPage';
import { GenerationSettingsPage } from './pages/settings/GenerationSettingsPage';
import { SettingsPage } from './pages/settings/SettingsPage';
import { ThreadSettingsPage } from './pages/settings/ThreadSettingsPage';
import { BudgetSettingsPage } from './pages/settings/BudgetSettingsPage';
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
          { path: '/images', element: <ImagesPage /> },
          { path: '/prompts/:id', element: <PromptEditorPage /> },
          { path: '/schedule', element: <SchedulePage /> },
          { path: '/schedule/rules', element: <ScheduleRulesPage /> },
          { path: '/publish', element: <PublishPage /> },
          { path: '/settings', element: <SettingsPage /> },
          { path: '/settings/connections', element: <ConnectionsPage /> },
          { path: '/settings/generation', element: <GenerationSettingsPage /> },
          { path: '/settings/thread', element: <ThreadSettingsPage /> },
          { path: '/settings/budget', element: <BudgetSettingsPage /> },
          { path: '/settings/keys', element: <KeysPage /> },
          { path: '/notifications', element: <NotificationsPage /> },
          { path: '/ai-costs', element: <AiCostsPage /> },
          { path: '/setup', element: <SetupPage /> },
          { path: '*', element: <NotFoundPage /> },
        ],
      },
    ],
  },
]);
