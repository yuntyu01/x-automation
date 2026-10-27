import { CircleAlert, Inbox, WifiOff } from 'lucide-react';
import type { ReactNode } from 'react';
import { ApiError, errorMessage } from '../../api/client';
import { Button } from './Button';
import styles from './States.module.css';

export function EmptyState({ icon, title, body, children }: { icon?: ReactNode; title: string; body?: string; children?: ReactNode }) {
  return (
    <div className={styles.empty}>
      <span className={styles.icon}>{icon ?? <Inbox size={28} aria-hidden />}</span>
      <p className={styles.title}>{title}</p>
      {body && <p className={styles.body}>{body}</p>}
      {children && <div className={styles.actions}>{children}</div>}
    </div>
  );
}

export function ErrorState({ error, onRetry }: { error: unknown; onRetry?: () => void }) {
  const offline = error instanceof ApiError && error.code === 'network';
  return (
    <div className={styles.empty} role="alert">
      <span className={[styles.icon, styles.danger].join(' ')}>{offline ? <WifiOff size={28} aria-hidden /> : <CircleAlert size={28} aria-hidden />}</span>
      <p className={styles.title}>{offline ? '연결이 끊겼어요' : '불러오지 못했어요'}</p>
      <p className={styles.body}>{errorMessage(error)}</p>
      {onRetry && (
        <div className={styles.actions}>
          <Button variant="secondary" onClick={onRetry}>
            다시 시도
          </Button>
        </div>
      )}
    </div>
  );
}

export function Skeleton({ height = 20, width = '100%', radius }: { height?: number; width?: number | string; radius?: number }) {
  return <span className={styles.skeleton} style={{ height, width, borderRadius: radius }} aria-hidden />;
}

export function LoadingList({ rows = 3 }: { rows?: number }) {
  return (
    <div className={styles.loading} role="status" aria-label="불러오는 중">
      {Array.from({ length: rows }, (_, i) => (
        <Skeleton key={i} height={84} radius={20} />
      ))}
    </div>
  );
}
