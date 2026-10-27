import { useQueryClient } from '@tanstack/react-query';
import { Sparkles } from 'lucide-react';
import { useEffect } from 'react';
import { errorMessage } from '../../api/client';
import { useCancelGeneration, useGenerationRequests } from '../../api/generation';
import { GEN_STATE_LABEL, TRIGGER_LABEL, type GenerationRequest, type GenerationState } from '../../api/types';
import { relativeTime } from '../../lib/format';
import { Badge, type Tone } from '../ui/Badge';
import { Button } from '../ui/Button';
import { Card, CardTitle } from '../ui/Card';
import { useToast } from '../ui/Toast';
import styles from './generation.module.css';

const TONE: Record<GenerationState, Tone> = {
  queued: 'scheduled',
  running: 'progress',
  succeeded: 'success',
  partial: 'warning',
  failed: 'danger',
  canceled: 'neutral',
  unknown: 'warning',
};

// 생성 요청 진행 목록. activeOnly면 진행 중인 요청이 있을 때만 보인다 (콘텐츠 화면 위)
export function GenerationQueue({ activeOnly = false, limit = 10 }: { activeOnly?: boolean; limit?: number }) {
  const q = useGenerationRequests(activeOnly, limit);
  const items = q.data?.items ?? [];
  const qc = useQueryClient();
  // 진행이 바뀌면(마지막 요청이 끝나 목록에서 빠질 때 포함) 콘텐츠 목록·개수를 새로 읽는다
  const progress = items.map((r) => `${r.id}:${r.produced + r.failed}`).join(',');
  useEffect(() => {
    void qc.invalidateQueries({ queryKey: ['contents'] });
  }, [progress, qc]);
  if (activeOnly && items.length === 0) return null;
  return (
    <Card aria-labelledby="gen-queue-title">
      <CardTitle id="gen-queue-title" action={q.data?.provider === 'mock' ? <Badge tone="info">목업 생성</Badge> : undefined}>
        <span className={styles.titleIcon}>
          <Sparkles size={18} aria-hidden /> {activeOnly ? '생성 중' : '최근 생성 요청'}
        </span>
      </CardTitle>
      {items.length === 0 && <p className={styles.muted}>아직 생성 요청이 없어요.</p>}
      <ul className={styles.queue}>
        {items.map((r) => (
          <RequestRow key={r.id} r={r} />
        ))}
      </ul>
    </Card>
  );
}

function RequestRow({ r }: { r: GenerationRequest }) {
  const cancel = useCancelGeneration();
  const toast = useToast();
  const done = r.produced + r.failed;
  const pct = Math.round((done / r.quantity) * 100);
  const what = r.trigger === 'regenerate' ? (r.options.target === 'image' ? '이미지 다시 만들기' : '글 다시 만들기') : r.options.text ? r.prompt_name ?? '글' : '이미지';
  return (
    <li className={styles.req}>
      <div className={styles.reqHead}>
        <strong>{what}</strong>
        <Badge tone={TONE[r.state]}>{GEN_STATE_LABEL[r.state]}</Badge>
      </div>
      <div className={styles.reqMeta}>
        <span>{TRIGGER_LABEL[r.trigger]}</span>
        <span>
          {r.produced}/{r.quantity}개 완료{r.failed > 0 ? ` · 실패·취소 ${r.failed}` : ''}
          {r.running > 0 ? ` · 진행 ${r.running}` : ''}
        </span>
        <span>{relativeTime(r.created_at)}</span>
      </div>
      <div className={styles.bar} role="progressbar" aria-label="생성 진행률" aria-valuenow={pct} aria-valuemin={0} aria-valuemax={100}>
        <span style={{ width: `${pct}%` }} />
      </div>
      {r.error && <p className={styles.err}>{r.error}</p>}
      {r.queued > 0 && (
        <Button
          size="sm"
          variant="secondary"
          busy={cancel.isPending}
          onClick={async () => {
            try {
              await cancel.mutateAsync(r.id);
              toast.show('대기 중인 생성을 취소했어요. 이미 시작한 생성은 끝까지 진행될 수 있어요', 'info');
            } catch (e) {
              toast.show(errorMessage(e), 'error');
            }
          }}
        >
          대기 {r.queued}개 취소
        </Button>
      )}
    </li>
  );
}
