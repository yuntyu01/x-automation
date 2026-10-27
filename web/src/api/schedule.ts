import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { ApiError, del, get, post, put } from './client';
import type { ScheduleDay, SchedulePlanReport, ScheduleRules, ScheduleSettings, ScheduleWindow } from './types';

export const schedKeys = {
  days: (from: string, days: number) => ['schedule', 'days', from, days] as const,
  settings: ['schedule', 'settings'] as const,
};

export function useScheduleDays(from: string, days: number) {
  return useQuery({
    queryKey: schedKeys.days(from, days),
    queryFn: () => get<{ days: ScheduleDay[]; now: string }>(`/api/schedule?from=${from}&days=${days}`),
    refetchInterval: 60_000,
  });
}

export function useScheduleSettings() {
  return useQuery({ queryKey: schedKeys.settings, queryFn: () => get<ScheduleSettings>('/api/schedule/settings') });
}

export interface RulesBody {
  rules: ScheduleRules;
  windows: ScheduleWindow[];
  scope: 'tomorrow' | 'now' | 'later';
  seed: number;
}

export const previewRules = (b: RulesBody) => post<SchedulePlanReport>('/api/schedule/settings/preview', b);

export function useSaveScheduleRules() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (b: RulesBody) => put<SchedulePlanReport>('/api/schedule/settings', b),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['schedule'] }),
  });
}

// 예약 조작: 끝나면 시간표·콘텐츠 목록을 다시 읽는다
export function useScheduleAction() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ path, body, method = 'POST' }: { path: string; body?: unknown; method?: 'POST' | 'DELETE' }) =>
      method === 'DELETE' ? del(path) : post<Record<string, unknown>>(path, body),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['schedule'] });
      void qc.invalidateQueries({ queryKey: ['contents'] });
    },
  });
}

export function placeContents(ids: string[]) {
  return post<{ placed: { content_id: string; reservation_id: string; slot_at: string }[]; skipped: Record<string, string>; left: string[] }>(
    '/api/schedule/place',
    { content_ids: ids },
  );
}

// 409 needs_confirm 응답의 경고 목록
export function confirmWarnings(e: unknown): string[] | null {
  if (e instanceof ApiError && e.code === 'needs_confirm') {
    const w = (e.body as { error?: { details?: { warnings?: string[] } } } | null)?.error?.details?.warnings;
    return w ?? [];
  }
  return null;
}

export const removeException = (date: string) => del(`/api/schedule/exceptions/${date}`);
