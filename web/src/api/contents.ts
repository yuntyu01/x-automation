import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { get, post } from './client';
import type { BulkResult, ContentDetail, ContentPage, Prompt, PromptVersion, RevisionSummary, Stage } from './types';

export interface ContentQuery {
  stage: Stage | 'all';
  q: string;
  category: string;
  from: string;
  to: string;
  sort: 'updated' | 'created' | 'title' | 'slot';
  limit: number;
}

export function contentQueryString(f: Partial<ContentQuery>, offset = 0): string {
  const p = new URLSearchParams();
  if (f.stage && f.stage !== 'all') p.set('stage', f.stage);
  if (f.q) p.set('q', f.q);
  if (f.category) p.set('category', f.category);
  if (f.from) p.set('from', f.from);
  if (f.to) p.set('to', f.to);
  if (f.sort && f.sort !== 'updated') p.set('sort', f.sort);
  if (f.limit) p.set('limit', String(f.limit));
  if (offset) p.set('offset', String(offset));
  return p.toString();
}

export function useContents(f: ContentQuery, offset: number) {
  return useQuery({
    queryKey: ['contents', 'list', f, offset],
    queryFn: ({ signal }) => get<ContentPage>(`/api/contents?${contentQueryString(f, offset)}`, signal),
    placeholderData: keepPreviousData,
  });
}

export function useContent(id: string | undefined) {
  return useQuery({
    queryKey: ['contents', 'detail', id],
    queryFn: ({ signal }) => get<ContentDetail>(`/api/contents/${id}`, signal),
    enabled: !!id,
    staleTime: 0,
    refetchOnWindowFocus: false, // 편집 중 덮어쓰지 않는다. 충돌은 저장 시 감지
  });
}

export function useCategories() {
  return useQuery({ queryKey: ['contents', 'categories'], queryFn: () => get<{ items: string[] }>('/api/contents/categories') });
}

export function useRevisions(id: string, enabled: boolean) {
  return useQuery({ queryKey: ['contents', 'revisions', id], queryFn: () => get<{ items: RevisionSummary[] }>(`/api/contents/${id}/revisions`), enabled });
}

export function useBulk() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (b: { action: 'archive' | 'unarchive' | 'category' | 'delete'; ids: string[]; category?: string }) => post<BulkResult>('/api/contents/bulk', b),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['contents'] }),
  });
}

export function usePrompts(all = false) {
  return useQuery({ queryKey: ['prompts', all], queryFn: () => get<{ items: Prompt[] }>(`/api/prompts${all ? '?all=1' : ''}`) });
}

export function usePrompt(id: string | undefined, version = 0) {
  return useQuery({
    queryKey: ['prompts', 'detail', id, version],
    queryFn: () => get<Prompt>(`/api/prompts/${id}${version ? `?version=${version}` : ''}`),
    enabled: !!id && id !== 'new',
  });
}

export function usePromptVersions(id: string | undefined) {
  return useQuery({
    queryKey: ['prompts', 'versions', id],
    queryFn: () => get<{ items: PromptVersion[] }>(`/api/prompts/${id}/versions`),
    enabled: !!id && id !== 'new',
  });
}
