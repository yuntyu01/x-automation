import { Bell, CheckCheck } from 'lucide-react';
import { useState } from 'react';
import { useNavigate } from 'react-router';
import { useMarkRead, useNotifications } from '../api/queries';
import type { NotificationItem } from '../api/types';
import { PageHeader } from '../components/layout/AppShell';
import { Badge } from '../components/ui/Badge';
import { Button } from '../components/ui/Button';
import { Card } from '../components/ui/Card';
import { Segmented } from '../components/ui/Segmented';
import { EmptyState, ErrorState, LoadingList } from '../components/ui/States';
import { useToast } from '../components/ui/Toast';
import { errorMessage } from '../api/client';
import { relativeTime } from '../lib/format';
import styles from './NotificationsPage.module.css';

const SEVERITY: Record<NotificationItem['severity'], { tone: 'danger' | 'warning' | 'info'; label: string }> = {
  critical: { tone: 'danger', label: '중요' },
  warning: { tone: 'warning', label: '주의' },
  info: { tone: 'info', label: '알림' },
};

export function NotificationsPage() {
  const [filter, setFilter] = useState<'unread' | 'all'>('unread');
  const q = useNotifications(filter === 'unread');
  const mark = useMarkRead();
  const nav = useNavigate();
  const toast = useToast();

  async function open(n: NotificationItem) {
    if (!n.read_at) {
      try {
        await mark.mutateAsync(n.id);
      } catch (e) {
        toast.show(errorMessage(e), 'error');
      }
    }
    if (n.link) nav(n.link);
  }

  return (
    <>
      <PageHeader
        title="알림"
        actions={
          <Button variant="ghost" size="sm" icon={<CheckCheck size={18} />} onClick={() => mark.mutate('all')} disabled={mark.isPending}>
            모두 읽음
          </Button>
        }
      />
      <div className={styles.filters}>
        <Segmented
          label="알림 필터"
          value={filter}
          onChange={setFilter}
          options={[
            { value: 'unread', label: '읽지 않음' },
            { value: 'all', label: '전체' },
          ]}
        />
      </div>
      {q.isLoading && <LoadingList />}
      {q.isError && <ErrorState error={q.error} onRetry={() => q.refetch()} />}
      {q.data && q.data.items.length === 0 && (
        <Card>
          <EmptyState icon={<Bell size={28} aria-hidden />} title={filter === 'unread' ? '읽지 않은 알림이 없어요' : '알림이 없어요'} body="생성·게시 실패, 연결 만료, 콘텐츠 부족 같은 일이 생기면 여기에 남아요." />
        </Card>
      )}
      <ul className={styles.list}>
        {q.data?.items.map((n) => (
          <li key={n.id}>
            <button type="button" className={[styles.item, n.read_at ? styles.read : ''].join(' ')} onClick={() => open(n)}>
              <span className={styles.head}>
                <Badge tone={SEVERITY[n.severity].tone}>{SEVERITY[n.severity].label}</Badge>
                {n.resolved_at && <Badge tone="success">해결됨</Badge>}
                {n.count > 1 && <span className={styles.repeat}>{n.count}회 반복</span>}
                <span className={styles.time}>{relativeTime(n.last_at)}</span>
              </span>
              <span className={styles.title}>{n.title}</span>
              {n.body && <span className={styles.body}>{n.body}</span>}
              {n.link && <span className={styles.link}>{n.resolved_at ? '관련 화면 보기' : '해결하러 가기'}</span>}
              {!n.read_at && <span className="sr-only">읽지 않음</span>}
            </button>
          </li>
        ))}
      </ul>
    </>
  );
}
