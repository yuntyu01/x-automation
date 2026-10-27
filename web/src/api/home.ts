import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { get, post } from './client';
import { keys } from './queries';
import type { Settings, UsageSummary } from './types';

export interface HomeSlot {
  id: string;
  slot_at: string;
  state: string;
  content_id: string | null;
  title: string | null;
  preview: string | null;
  wait_reason: string | null;
  block_reason: string | null;
  part_count: number;
  generating: boolean;
  generation_due_at: string | null;
}

export interface HomeAction {
  key: string;
  severity: 'critical' | 'warning' | 'info';
  title: string;
  body?: string;
  link: string;
  count: number;
}

export interface HomeData {
  now: string;
  timezone: string;
  operation_mode: string;
  next: HomeSlot | null;
  upcoming: HomeSlot[];
  today: { date: string; target: number; published: number; in_progress: number; remaining: number; posts: number; skipped: number };
  actions: HomeAction[];
  automation: {
    enabled: boolean;
    changed_at: string | null;
    generation_paused: boolean;
    publishing_paused: boolean;
    mock: boolean;
    publishing_enabled: boolean;
    publish_blockers: string[];
    generation_blockers: string[];
  };
  stock: { ready: number; review: number; draft: number; problem: number };
  usage: UsageSummary;
}

export const homeKeys = { home: ['home'] as const };

// 홈은 실제 DB 상태를 보여 준다. 열어 둔 동안 30초마다 다시 읽는다
export function useHome() {
  return useQuery({ queryKey: homeKeys.home, queryFn: () => get<HomeData>('/api/home'), refetchInterval: 30_000 });
}

// 자동 운영 시작·중지, 생성·게시 일시정지
export function useAutomation() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (b: { automation_enabled?: boolean; generation_paused?: boolean; publishing_paused?: boolean }) =>
      post<{ settings: Settings; fill_note?: string; fill_shortfall?: number }>('/api/automation', b),
    onSuccess: (r) => {
      qc.setQueryData(keys.settings, r.settings);
      void qc.invalidateQueries({ queryKey: homeKeys.home });
      void qc.invalidateQueries({ queryKey: ['generation'] });
      void qc.invalidateQueries({ queryKey: ['schedule'] });
    },
  });
}
