import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { get, post, put } from './client';
import type { IntegrationStatus, NotificationItem, RuntimeInfo, Settings } from './types';

export const keys = {
  runtime: ['runtime'] as const,
  settings: ['settings'] as const,
  integrations: ['integrations'] as const,
  notifications: (unread: boolean) => ['notifications', unread] as const,
  notificationCounts: ['notifications', 'counts'] as const,
};

export function useRuntime() {
  return useQuery({ queryKey: keys.runtime, queryFn: () => get<RuntimeInfo>('/api/runtime'), staleTime: 5 * 60_000 });
}

export function useSettings() {
  return useQuery({ queryKey: keys.settings, queryFn: () => get<Settings>('/api/settings') });
}

export function useSaveSettings() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (s: Pick<Settings, 'version' | 'operation_mode' | 'timezone' | 'policy'>) =>
      put<Settings>('/api/settings', {
        version: s.version,
        operation_mode: s.operation_mode,
        timezone: s.timezone,
        policy: s.policy,
      }),
    onSuccess: (data) => {
      qc.setQueryData(keys.settings, data);
      // 생성 계획·한도는 설정을 따른다
      void qc.invalidateQueries({ queryKey: ['generation'] });
    },
  });
}

export function useIntegrations() {
  return useQuery({ queryKey: keys.integrations, queryFn: () => get<IntegrationStatus>('/api/integrations') });
}

export function useNotificationCounts() {
  // 브라우저를 닫아도 서버에 알림이 쌓이고, 다시 열면 누적 상태를 보여 준다
  return useQuery({
    queryKey: keys.notificationCounts,
    queryFn: () => get<{ unread: number; critical: number }>('/api/notifications/counts'),
    refetchInterval: 30_000,
  });
}

export function useNotifications(unread: boolean) {
  return useQuery({
    queryKey: keys.notifications(unread),
    queryFn: () => get<{ items: NotificationItem[] }>(`/api/notifications?limit=50${unread ? '&unread=1' : ''}`),
  });
}

export function useMarkRead() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: number | 'all') => (id === 'all' ? post('/api/notifications/read-all') : post(`/api/notifications/${id}/read`)),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['notifications'] }),
  });
}
