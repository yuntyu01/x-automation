import { useQuery } from '@tanstack/react-query';
import { ApiError, apiUrl, get } from './client';

export interface MediaAsset {
  id: string;
  parent_id: string | null;
  source: 'upload' | 'ai' | 'crop';
  content_type: string;
  bytes: number;
  width: number;
  height: number;
  alt_text: string;
  prompt: string | null;
  model: string | null;
  state: 'pending' | 'active' | 'deleting' | 'deleted' | 'delete_failed';
  last_published_at: string | null;
  retention_anchor_at: string | null;
  deleted_at: string | null;
  delete_error: string | null;
  created_at: string;
  url: string;
  thumb_url: string;
  usage_count: number;
  cleanup_after: string | null;
}

export interface MediaUsage {
  content_id: string;
  title: string;
  revision: number;
  current: boolean;
  part_index: number;
}

export type MediaFilter = 'all' | 'used' | 'unused' | 'cleaned' | 'failed';

export function useMediaList(usage: MediaFilter, q: string, limit = 60) {
  return useQuery({
    queryKey: ['media', 'list', usage, q, limit],
    queryFn: ({ signal }) => get<{ items: MediaAsset[]; total: number }>(`/api/media?usage=${usage}&limit=${limit}&q=${encodeURIComponent(q)}`, signal),
  });
}

export function useMediaUsage(id: string | null) {
  return useQuery({ queryKey: ['media', 'usage', id], queryFn: () => get<{ items: MediaUsage[] }>(`/api/media/${id}/usage`), enabled: !!id });
}

// 업로드 진행률을 보여 주려고 XHR을 쓴다 (fetch는 업로드 진행률이 없다)
export function uploadMedia(file: File, csrf: string, altText: string, onProgress: (pct: number) => void): Promise<MediaAsset> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open('POST', apiUrl('/api/media'));
    xhr.withCredentials = true;
    xhr.setRequestHeader('X-CSRF-Token', csrf);
    xhr.setRequestHeader('Accept', 'application/json');
    xhr.upload.onprogress = (e) => {
      if (e.lengthComputable) onProgress(Math.round((e.loaded / e.total) * 100));
    };
    xhr.onerror = () => reject(new ApiError(0, 'network', '서버에 연결할 수 없습니다. 네트워크 상태를 확인해 주세요'));
    xhr.onload = () => {
      let data: unknown;
      try {
        data = JSON.parse(xhr.responseText);
      } catch {
        data = null;
      }
      if (xhr.status >= 200 && xhr.status < 300) resolve(data as MediaAsset);
      else {
        const err = (data as { error?: { code?: string; message?: string; fields?: Record<string, string> } } | null)?.error;
        reject(new ApiError(xhr.status, err?.code ?? 'upload_failed', err?.fields?.file ?? err?.message ?? '업로드하지 못했어요', err?.fields ?? {}));
      }
    };
    const fd = new FormData();
    fd.append('file', file);
    fd.append('alt_text', altText);
    xhr.send(fd);
  });
}

export function formatBytes(n: number): string {
  if (n < 1024) return `${n}B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(0)}KB`;
  return `${(n / 1024 / 1024).toFixed(1)}MB`;
}
