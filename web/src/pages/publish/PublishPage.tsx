import { CircleCheck, CircleHelp, CircleX, ExternalLink, Loader, RotateCcw, Send, Square } from 'lucide-react';
import { useState } from 'react';
import { Link } from 'react-router';
import { errorMessage } from '../../api/client';
import { usePublishRuns, useRunAction, type RunAction } from '../../api/publish';
import { PART_STATE_LABEL, RUN_STATE_LABEL, type PublishPart, type PublishRun, type RunState } from '../../api/types';
import { PageHeader } from '../../components/layout/AppShell';
import { Badge, type Tone } from '../../components/ui/Badge';
import { Button } from '../../components/ui/Button';
import { Card, Section } from '../../components/ui/Card';
import { Sheet } from '../../components/ui/Sheet';
import { EmptyState, ErrorState, LoadingList } from '../../components/ui/States';
import { TextField } from '../../components/ui/TextField';
import { useToast } from '../../components/ui/Toast';
import { formatDateTime } from '../../lib/format';
import { BackButton } from '../settings/ConnectionsPage';
import styles from './PublishPage.module.css';

const RUN_TONE: Record<RunState, Tone> = {
  pending: 'progress',
  running: 'progress',
  partial: 'danger',
  unknown: 'danger',
  failed: 'danger',
  completed: 'success',
  canceled: 'neutral',
};

const partLabel = (p: PublishPart) => (p.position === 0 ? '본문' : `답글 ${p.position}`);

// 게시 기록: 진행 중인 묶음, 사람이 확인해야 하는 묶음(일부 성공·결과 모름·실패), 최근 기록
export function PublishPage() {
  const q = usePublishRuns();
  return (
    <>
      <PageHeader title="게시 기록" subtitle="본문과 답글이 X에 어떻게 올라갔는지 확인해요" back={<BackButton to="/schedule" />} />
      {q.isLoading && <LoadingList rows={3} />}
      {q.isError && <ErrorState error={q.error} onRetry={() => q.refetch()} />}
      {q.data && (
        <div className={styles.stack}>
          {q.data.attention.length > 0 && (
            <Section title="확인이 필요해요" description="자동으로 다시 보내지 않았어요. 확인한 뒤 처리해 주세요">
              {q.data.attention.map((r) => (
                <RunCard key={r.id} run={r} />
              ))}
            </Section>
          )}
          {q.data.active.length > 0 && (
            <Section title="진행 중">
              {q.data.active.map((r) => (
                <RunCard key={r.id} run={r} />
              ))}
            </Section>
          )}
          <Section title="최근 기록">
            {q.data.recent.length === 0 ? (
              <EmptyState icon={<Send size={28} />} title="아직 게시한 묶음이 없어요" body="예약 시각이 되면 자동으로 게시하고, 여기에 결과가 남아요." />
            ) : (
              q.data.recent.map((r) => <RunCard key={r.id} run={r} />)
            )}
          </Section>
        </div>
      )}
    </>
  );
}

function PartIcon({ state }: { state: PublishPart['state'] }) {
  if (state === 'succeeded') return <CircleCheck size={18} className={styles.ok} aria-hidden />;
  if (state === 'failed') return <CircleX size={18} className={styles.bad} aria-hidden />;
  if (state === 'unknown') return <CircleHelp size={18} className={styles.bad} aria-hidden />;
  if (state === 'sending') return <Loader size={18} className={styles.spin} aria-hidden />;
  return <Square size={16} className={styles.idle} aria-hidden />;
}

type Dialog = { kind: 'posted'; part: PublishPart } | { kind: 'not_posted'; part: PublishPart } | { kind: 'retry_unknown' } | { kind: 'abandon' } | null;

function RunCard({ run }: { run: PublishRun }) {
  const act = useRunAction();
  const toast = useToast();
  const [dialog, setDialog] = useState<Dialog>(null);
  const [ref, setRef] = useState('');
  const [refError, setRefError] = useState('');
  const unknownPart = run.parts.find((p) => p.state === 'unknown');
  const canRetry = run.state === 'partial' || run.state === 'failed';
  const attention = (run.state === 'partial' || run.state === 'failed' || run.state === 'unknown') && !run.dismissed_at;

  async function go(action: RunAction, done: string) {
    try {
      await act.mutateAsync({ id: run.id, action });
      toast.show(done, 'success');
      setDialog(null);
      setRef('');
      setRefError('');
    } catch (e) {
      const msg = errorMessage(e);
      if (dialog?.kind === 'posted') setRefError(msg);
      else toast.show(msg, 'error');
    }
  }

  return (
    <Card className={styles.run}>
      <div className={styles.head}>
        <div className={styles.titleBox}>
          <Link to={`/contents/${run.content_id}`} className={styles.title}>
            {run.title || '제목 없음'}
          </Link>
          <span className={styles.meta}>
            {run.trigger === 'manual' ? '지금 게시' : `예약 ${formatDateTime(run.slot_at)}`}
            {run.started_at && ` · 시작 ${formatDateTime(run.started_at)}`}
          </span>
        </div>
        <Badge tone={RUN_TONE[run.state]}>{run.dismissed_at ? '중단함' : RUN_STATE_LABEL[run.state]}</Badge>
      </div>
      {run.note && <p className={styles.note}>{run.note}</p>}
      <ol className={styles.parts}>
        {run.parts.map((p) => (
          <li key={p.position} className={styles.part}>
            <PartIcon state={p.state} />
            <div className={styles.partBody}>
              <div className={styles.partHead}>
                <strong>{partLabel(p)}</strong>
                <span className={styles.meta}>{PART_STATE_LABEL[p.state]}</span>
                {p.media_count > 0 && <span className={styles.meta}>이미지 {p.media_count}장</span>}
                {p.made_with_ai && <span className={styles.meta}>AI 표시</span>}
                {p.x_url && (
                  <a className={styles.xlink} href={p.x_url} target="_blank" rel="noreferrer noopener">
                    X에서 보기 <ExternalLink size={14} aria-hidden />
                  </a>
                )}
              </div>
              <p className={styles.text}>{p.text}</p>
              {p.error && <p className={styles.err}>{p.error}</p>}
              {p.state === 'pending' && p.not_before && <p className={styles.meta}>{formatDateTime(p.not_before)} 이후에 올려요</p>}
            </div>
          </li>
        ))}
      </ol>
      {attention && (
        <div className={styles.actions}>
          {unknownPart && (
            <>
              <p className={styles.help}>
                {partLabel(unknownPart)}을(를) 보냈지만 결과를 몰라요. X 계정에서 실제로 올라갔는지 확인해 주세요. 다시 보내면 같은 글이 두 번 올라갈 수 있어요.
              </p>
              <Button size="sm" onClick={() => setDialog({ kind: 'posted', part: unknownPart })}>
                올라가 있어요
              </Button>
              <Button size="sm" variant="secondary" onClick={() => setDialog({ kind: 'not_posted', part: unknownPart })}>
                안 올라갔어요
              </Button>
            </>
          )}
          {canRetry && (
            <Button size="sm" icon={<RotateCcw size={16} />} busy={act.isPending} onClick={() => go({ kind: 'retry', accept_duplicate_risk: false }, '실패한 부분부터 다시 보내요')}>
              {run.state === 'partial' ? '실패한 답글부터 다시' : '다시 시도'}
            </Button>
          )}
          {unknownPart && (
            <Button size="sm" variant="ghost" onClick={() => setDialog({ kind: 'retry_unknown' })}>
              확인 없이 다시 보내기
            </Button>
          )}
          <Button size="sm" variant="ghost" onClick={() => setDialog({ kind: 'abandon' })}>
            그만두기
          </Button>
        </div>
      )}

      <Sheet
        open={dialog?.kind === 'posted'}
        onClose={() => setDialog(null)}
        title="X에 올라간 게시물 주소"
        description="X에서 그 게시물을 열고 주소를 복사해 붙여 넣어 주세요. 남은 답글은 그 게시물에 이어서 올려요."
        footer={
          <>
            <Button variant="secondary" onClick={() => setDialog(null)}>
              취소
            </Button>
            <Button
              busy={act.isPending}
              disabled={!ref.trim()}
              onClick={() => dialog?.kind === 'posted' && go({ kind: 'resolve', position: dialog.part.position, posted: true, post_ref: ref.trim() }, '확인한 결과로 정리했어요')}
            >
              확인
            </Button>
          </>
        }
      >
        <TextField label="게시물 주소 또는 ID" value={ref} onChange={(e) => setRef(e.target.value)} placeholder="https://x.com/계정/status/123…" error={refError} inputMode="url" data-autofocus />
      </Sheet>

      <Sheet
        open={dialog?.kind === 'not_posted'}
        onClose={() => setDialog(null)}
        title="X에 정말 없나요?"
        description="X 계정의 게시물·답글 목록에서 확인한 뒤에 골라 주세요. 실제로 올라가 있었다면 같은 글이 두 번 올라가요."
        footer={
          <>
            <Button variant="secondary" onClick={() => setDialog(null)}>
              취소
            </Button>
            <Button
              busy={act.isPending}
              onClick={() => dialog?.kind === 'not_posted' && go({ kind: 'resolve', position: dialog.part.position, posted: false }, '그 부분부터 다시 보내요')}
            >
              확인했어요, 다시 보내기
            </Button>
          </>
        }
      >
        <p className={styles.help}>보낸 시각: {formatDateTime(dialog?.kind === 'not_posted' ? dialog.part.intent_at : null)}</p>
      </Sheet>

      <Sheet
        open={dialog?.kind === 'retry_unknown'}
        onClose={() => setDialog(null)}
        title="확인 없이 다시 보낼까요?"
        description="결과를 모르는 부분을 그대로 다시 보내요. 이미 올라가 있었다면 X에 같은 글이 두 번 올라가요. 가능하면 X에서 먼저 확인해 보세요."
        footer={
          <>
            <Button variant="secondary" onClick={() => setDialog(null)}>
              취소
            </Button>
            <Button variant="danger" busy={act.isPending} onClick={() => go({ kind: 'retry', accept_duplicate_risk: true }, '다시 보내요')}>
              중복 위험을 알고 다시 보내기
            </Button>
          </>
        }
      >
        <p className={styles.help}>X API에는 같은 요청을 한 번만 처리하게 하는 장치가 없어요.</p>
      </Sheet>

      <Sheet
        open={dialog?.kind === 'abandon'}
        onClose={() => setDialog(null)}
        title="이 묶음을 그만둘까요?"
        description="남은 부분을 보내지 않고 끝내요. 이미 올라간 게시물은 그대로 남고, 이 글은 자동 예약에서 빠져요 (보관함에서 다시 배치할 수 있어요)."
        footer={
          <>
            <Button variant="secondary" onClick={() => setDialog(null)}>
              취소
            </Button>
            <Button variant="danger" busy={act.isPending} onClick={() => go({ kind: 'abandon' }, '그만뒀어요')}>
              그만두기
            </Button>
          </>
        }
      >
        <p className={styles.help}>올라간 부분: {run.parts.filter((p) => p.state === 'succeeded').map(partLabel).join(', ') || '없음'}</p>
      </Sheet>
    </Card>
  );
}
