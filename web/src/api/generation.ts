import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { get, post } from './client';
import type { ContentDetail, GenerationOptions, GenerationPlan, GenerationRequest, GenerationResult, UsageSummary } from './types';

export const genKeys = {
  requests: (active: boolean) => ['generation', 'requests', active] as const,
  plan: ['generation', 'plan'] as const,
  results: (contentId: string) => ['generation', 'results', contentId] as const,
  usage: ['usage'] as const,
};

const isActive = (r: GenerationRequest) => r.state === 'queued' || r.state === 'running';

// 진행 중인 요청이 있으면 3초마다 다시 읽어 진행률을 보여 준다
export function useGenerationRequests(active = false, limit = 20) {
  return useQuery({
    queryKey: genKeys.requests(active),
    queryFn: () => get<{ items: GenerationRequest[]; provider: string }>(`/api/generation?limit=${limit}${active ? '&active=1' : ''}`),
    refetchInterval: (q) => (q.state.data?.items.some(isActive) ? 3000 : false),
  });
}

export interface SubmitBody {
  idempotency_key: string;
  prompt_id: string;
  variables: Record<string, string>;
  options: GenerationOptions;
  quantity: number;
  place_into_slots: boolean;
}

export function useSubmitGeneration() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (b: SubmitBody) => post<GenerationRequest>('/api/generation', b),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['generation'] }),
  });
}

export function useCancelGeneration() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => post<GenerationRequest>(`/api/generation/${id}/cancel`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['generation'] }),
  });
}

export function useEstimate(quantity: number, options: GenerationOptions, enabled: boolean) {
  return useQuery({
    queryKey: ['generation', 'estimate', quantity, options],
    queryFn: () => post<{ micros: number; pricing_known: boolean; currency: string }>('/api/generation/estimate', { quantity, options }),
    enabled: enabled && quantity > 0,
    placeholderData: (p) => p,
  });
}

export function usePlan() {
  return useQuery({ queryKey: genKeys.plan, queryFn: () => get<GenerationPlan>('/api/generation/plan'), refetchInterval: 60_000 });
}

export function useFillNow() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => post<{ request: GenerationRequest | null; shortfall: number }>('/api/generation/fill'),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['generation'] }),
  });
}

// after: 끝난 요청 id. 바뀌면 결과를 새로 읽는다 (진행 중에는 poll)
export function useResults(contentId: string, poll: boolean, after = '') {
  return useQuery({
    queryKey: [...genKeys.results(contentId), after],
    queryFn: () => get<{ items: GenerationResult[] }>(`/api/contents/${contentId}/generation-results`),
    refetchInterval: poll ? 2500 : false,
  });
}

export function useRegenerate(contentId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (b: { idempotency_key: string; target: 'all' | 'part' | 'image'; part_index: number; instruction: string; image_prompt: string }) =>
      post<GenerationRequest>(`/api/contents/${contentId}/regenerate`, b),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['generation'] }),
  });
}

export function applyResult(contentId: string, resultId: string, editVersion: number) {
  return post<ContentDetail>(`/api/contents/${contentId}/generation-results/${resultId}/apply`, { edit_version: editVersion });
}

export function discardResult(contentId: string, resultId: string) {
  return post(`/api/contents/${contentId}/generation-results/${resultId}/discard`);
}

export function useUsage() {
  return useQuery({ queryKey: genKeys.usage, queryFn: () => get<UsageSummary>('/api/usage') });
}

// 새 요청 키: 같은 버튼을 두 번 눌러도(네트워크 재전송 포함) 요청은 하나다
export function newRequestKey(prefix: string) {
  const b = crypto.getRandomValues(new Uint8Array(16));
  return `${prefix}-${Array.from(b, (x) => x.toString(16).padStart(2, '0')).join('')}`;
}
