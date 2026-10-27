import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { get, post } from './client';
import { keys } from './queries';
import type { ConnectionView, PublishRun, PublishRunList } from './types';

export const pubKeys = {
  runs: ['publish', 'runs'] as const,
};

// 게시가 진행 중이면 자주, 아니면 가끔 다시 읽는다
export function usePublishRuns() {
  return useQuery({
    queryKey: pubKeys.runs,
    queryFn: () => get<PublishRunList>('/api/publish/runs'),
    refetchInterval: (q) => ((q.state.data?.active.length ?? 0) > 0 ? 3_000 : 30_000),
  });
}

function useInvalidatePublishing() {
  const qc = useQueryClient();
  return () => {
    void qc.invalidateQueries({ queryKey: ['publish'] });
    void qc.invalidateQueries({ queryKey: ['schedule'] });
    void qc.invalidateQueries({ queryKey: ['contents'] });
    void qc.invalidateQueries({ queryKey: ['notifications'] });
  };
}

export function usePostNow() {
  const done = useInvalidatePublishing();
  return useMutation({
    mutationFn: (b: { content_id: string; confirm: boolean }) => post<PublishRun>('/api/publish/now', b),
    onSuccess: done,
  });
}

export type RunAction =
  | { kind: 'resolve'; position: number; posted: boolean; post_ref?: string }
  | { kind: 'retry'; accept_duplicate_risk: boolean }
  | { kind: 'abandon' };

export function useRunAction() {
  const done = useInvalidatePublishing();
  return useMutation({
    mutationFn: ({ id, action }: { id: string; action: RunAction }) => {
      const { kind, ...body } = action;
      return post<PublishRun>(`/api/publish/runs/${id}/${kind}`, body);
    },
    onSuccess: done,
  });
}

// X 연결: 서버가 PKCE·state를 만들고 인가 주소를 돌려준다. 브라우저를 그 주소로 보낸다
export async function startXConnect() {
  const { url } = await post<{ url: string }>('/api/x/oauth/start');
  window.location.assign(url);
}

export function useXConnectionAction() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (kind: 'disconnect' | 'check') => post<ConnectionView>(`/api/x/${kind}`),
    onSettled: () => {
      void qc.invalidateQueries({ queryKey: keys.integrations });
      void qc.invalidateQueries({ queryKey: ['notifications'] });
    },
  });
}
