import { useQueryClient } from '@tanstack/react-query';
import { CircleAlert, CircleCheck, RotateCcw } from 'lucide-react';
import { useCallback, useRef, useState } from 'react';
import { errorMessage, getCsrfToken } from '../../api/client';
import { uploadMedia, type MediaAsset } from '../../api/media';
import { Button } from '../ui/Button';
import { Spinner } from '../ui/Spinner';
import styles from './media.module.css';

export interface UploadItem {
  key: number;
  file: File;
  progress: number;
  status: 'uploading' | 'done' | 'error';
  error?: string;
  asset?: MediaAsset;
}

const ACCEPT = ['image/jpeg', 'image/png', 'image/webp', 'image/gif'];

// 여러 파일을 순서대로 올린다 (서버의 이미지 처리는 한 번에 하나)
export function useUploader(onUploaded?: (a: MediaAsset) => void) {
  const [items, setItems] = useState<UploadItem[]>([]);
  const seq = useRef(0);
  const queue = useRef(Promise.resolve());
  const qc = useQueryClient();

  const patch = (key: number, p: Partial<UploadItem>) => setItems((xs) => xs.map((x) => (x.key === key ? { ...x, ...p } : x)));

  const run = useCallback(
    (item: UploadItem) => {
      queue.current = queue.current.then(async () => {
        if (!ACCEPT.includes(item.file.type) && item.file.type !== '') {
          patch(item.key, { status: 'error', error: 'JPG·PNG·WEBP·GIF 이미지만 올릴 수 있어요' });
          return;
        }
        if (item.file.size > 15 * 1024 * 1024) {
          patch(item.key, { status: 'error', error: '파일이 15MB를 넘어요' });
          return;
        }
        try {
          const a = await uploadMedia(item.file, getCsrfToken(), '', (p) => patch(item.key, { progress: p }));
          patch(item.key, { status: 'done', progress: 100, asset: a });
          await qc.invalidateQueries({ queryKey: ['media'] });
          onUploaded?.(a);
        } catch (e) {
          patch(item.key, { status: 'error', error: errorMessage(e) });
        }
      });
    },
    [qc, onUploaded],
  );

  const add = useCallback(
    (files: FileList | File[]) => {
      const list = Array.from(files).map((file) => ({ key: ++seq.current, file, progress: 0, status: 'uploading' as const }));
      setItems((xs) => [...list, ...xs].slice(0, 30));
      list.forEach(run);
    },
    [run],
  );

  const retry = useCallback(
    (key: number) => {
      const it = items.find((x) => x.key === key);
      if (!it) return;
      patch(key, { status: 'uploading', progress: 0, error: undefined });
      run({ ...it, status: 'uploading', progress: 0 });
    },
    [items, run],
  );

  const clearDone = () => setItems((xs) => xs.filter((x) => x.status !== 'done'));
  return { items, add, retry, clearDone };
}

export function UploadList({ items, onRetry }: { items: UploadItem[]; onRetry: (key: number) => void }) {
  if (!items.length) return null;
  return (
    <ul className={styles.uploads} aria-label="업로드 진행">
      {items.map((it) => (
        <li key={it.key} className={styles.upload}>
          {it.status === 'uploading' ? <Spinner size={18} /> : it.status === 'done' ? <CircleCheck size={18} className={styles.ok} aria-hidden /> : <CircleAlert size={18} className={styles.bad} aria-hidden />}
          <span className={styles.uploadName}>{it.file.name}</span>
          {it.status === 'uploading' && (
            <span className={styles.progress} role="progressbar" aria-valuenow={it.progress} aria-valuemin={0} aria-valuemax={100} aria-label={`${it.file.name} 업로드`}>
              <span style={{ width: `${it.progress}%` }} />
            </span>
          )}
          {it.status === 'done' && <span className={styles.muted}>완료</span>}
          {it.status === 'error' && (
            <>
              <span className={styles.err}>{it.error}</span>
              <Button size="sm" variant="ghost" icon={<RotateCcw size={14} />} onClick={() => onRetry(it.key)}>
                다시
              </Button>
            </>
          )}
        </li>
      ))}
    </ul>
  );
}
