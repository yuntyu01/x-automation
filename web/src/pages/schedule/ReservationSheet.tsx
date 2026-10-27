import { ArrowDown, ArrowUp, CalendarArrowUp, Clock, ExternalLink, FilePlus2, Pin, PinOff, Send, SkipForward, StepForward, X } from 'lucide-react';
import { useState } from 'react';
import { useNavigate } from 'react-router';
import { errorMessage } from '../../api/client';
import { confirmWarnings, useScheduleAction } from '../../api/schedule';
import type { ReservationItem, ScheduleDay } from '../../api/types';
import { Button } from '../../components/ui/Button';
import { Row, RowGroup } from '../../components/ui/Row';
import { Sheet } from '../../components/ui/Sheet';
import { TimeField } from '../../components/ui/TimeField';
import { useToast } from '../../components/ui/Toast';
import { dayLabel, formatTime } from '../../lib/format';
import { ContentPicker } from './ContentPicker';
import { stateBadge } from './SchedulePage';
import styles from './SchedulePage.module.css';

// 예약 하나의 작업: 글 고르기·옮기기·순서·시간 지정·빼기·건너뛰기. 게시 중·완료 예약은 정보만 보여 준다
export function ReservationSheet({ item, day, onClose }: { item: ReservationItem | null; day?: ScheduleDay; onClose: () => void }) {
  const act = useScheduleAction();
  const toast = useToast();
  const nav = useNavigate();
  const [picker, setPicker] = useState(false);
  const [timeOpen, setTimeOpen] = useState(false);
  const [time, setTime] = useState('');
  const [warnings, setWarnings] = useState<string[] | null>(null);
  const [skipConfirm, setSkipConfirm] = useState(false);
  if (!item) return null;
  const editable = item.state === 'open' || item.state === 'assigned' || item.state === 'waiting';
  const list = (day?.items ?? []).filter((i) => i.state === 'open' || i.state === 'assigned' || i.state === 'waiting');
  const idx = list.findIndex((i) => i.id === item.id);
  const prev = idx > 0 ? list[idx - 1] : undefined;
  const next = idx >= 0 && idx < list.length - 1 ? list[idx + 1] : undefined;

  async function run(path: string, body: unknown, done: string) {
    try {
      await act.mutateAsync({ path, body });
      toast.show(done, 'success');
      onClose();
    } catch (e) {
      toast.show(errorMessage(e), 'error');
    }
  }

  async function pin(confirm: boolean) {
    try {
      await act.mutateAsync({ path: `/api/schedule/reservations/${item!.id}/pin`, body: { date: item!.local_date, time, confirm } });
      toast.show(`${time}로 시간을 고정했어요`, 'success');
      setTimeOpen(false);
      setWarnings(null);
      onClose();
    } catch (e) {
      const w = confirmWarnings(e);
      if (w) setWarnings(w);
      else toast.show(errorMessage(e), 'error');
    }
  }

  const b = stateBadge(item);
  return (
    <>
      <Sheet
        open={!!item && !picker && !timeOpen && !skipConfirm}
        onClose={onClose}
        title={`${dayLabel(item.local_date)} ${formatTime(item.slot_at)}`}
        description={`${item.title ?? '빈 예약'} · ${b.label}${item.delay_note ? ` · ${item.delay_note}` : ''}`}
      >
        {!editable && <p className={styles.muted}>이미 게시 중이거나 끝난 예약이라 바꿀 수 없어요.</p>}
        {item.block_reason && editable && (
          <p className={styles.blocked} role="note">
            시작하지 못한 이유: {item.block_reason}
          </p>
        )}
        {item.run_note && <p className={styles.muted}>{item.run_note}</p>}
        <RowGroup>
          {item.post_url && (
            <Row
              icon={<ExternalLink size={20} />}
              label="X에서 본문 보기"
              onClick={() => window.open(item.post_url!, '_blank', 'noopener,noreferrer')}
            />
          )}
          {item.run_id && (
            <Row
              icon={<Send size={20} />}
              label={item.state === 'partial' || item.state === 'unknown' || item.state === 'failed' ? '게시 결과 확인·처리' : '게시 기록 보기'}
              onClick={() => nav('/publish')}
            />
          )}
          {editable && (
            <Row icon={<FilePlus2 size={20} />} label={item.content_id ? '다른 글로 바꾸기' : '글 고르기'} description="준비 안 된 글은 준비될 때까지 대기해요" onClick={() => setPicker(true)} />
          )}
          {item.content_id && <Row icon={<ExternalLink size={20} />} label="글 편집으로" onClick={() => nav(`/contents/${item.content_id}`)} />}
          {editable && item.content_id && (
            <>
              <Row icon={<StepForward size={20} />} label="다음 빈 시간으로" onClick={() => run(`/api/schedule/reservations/${item.id}/move`, { to: 'next' }, '다음 빈 예약으로 옮겼어요')} />
              <Row icon={<CalendarArrowUp size={20} />} label="내일로 이동" onClick={() => run(`/api/schedule/reservations/${item.id}/move`, { to: 'tomorrow' }, '내일 첫 빈 예약으로 옮겼어요')} />
            </>
          )}
          {editable && prev && (
            <Row icon={<ArrowUp size={20} />} label={`앞 예약(${formatTime(prev.slot_at)})과 순서 바꾸기`} onClick={() => run('/api/schedule/swap', { a: item.id, b: prev.id }, '순서를 바꿨어요')} />
          )}
          {editable && next && (
            <Row icon={<ArrowDown size={20} />} label={`뒤 예약(${formatTime(next.slot_at)})과 순서 바꾸기`} onClick={() => run('/api/schedule/swap', { a: item.id, b: next.id }, '순서를 바꿨어요')} />
          )}
          {editable && (
            <Row
              icon={<Clock size={20} />}
              label="시간 지정·고정"
              description="고정한 예약은 시간표를 다시 짜도 그대로예요"
              onClick={() => {
                setTime(formatTime(item.slot_at));
                setTimeOpen(true);
              }}
            />
          )}
          {editable && item.pinned && <Row icon={<PinOff size={20} />} label="시간 고정 풀기" onClick={() => run(`/api/schedule/reservations/${item.id}/unpin`, {}, '고정을 풀었어요')} />}
          {editable && item.content_id && (
            <Row icon={<X size={20} />} label="예약에서 빼기" description="글은 보관함에 남고, 직접 다시 넣기 전에는 자동 배치하지 않아요" onClick={() => run(`/api/schedule/reservations/${item.id}/unassign`, {}, '예약에서 뺐어요')} />
          )}
          {editable && <Row icon={<SkipForward size={20} />} label="이 시간 건너뛰기" tone="danger" onClick={() => setSkipConfirm(true)} />}
        </RowGroup>
      </Sheet>

      <ContentPicker
        open={picker}
        onClose={() => setPicker(false)}
        title={`${formatTime(item.slot_at)} 예약에 넣을 글`}
        onPick={async (id) => {
          setPicker(false);
          await run(`/api/schedule/reservations/${item.id}/assign`, { content_id: id }, '예약에 넣었어요');
        }}
      />

      <Sheet
        open={timeOpen}
        onClose={() => {
          setTimeOpen(false);
          setWarnings(null);
        }}
        title="시간 지정·고정"
        description={`${dayLabel(item.local_date)} 안에서 시각을 정해요. 다른 날로 옮기려면 '내일로 이동'이나 새 고정 예약을 써 주세요.`}
        footer={
          warnings ? (
            <>
              <Button variant="secondary" onClick={() => setWarnings(null)}>
                다시 고르기
              </Button>
              <Button busy={act.isPending} onClick={() => pin(true)}>
                그래도 고정
              </Button>
            </>
          ) : (
            <Button busy={act.isPending} disabled={!/^\d{2}:\d{2}$/.test(time)} onClick={() => pin(false)}>
              <Pin size={16} aria-hidden /> 고정
            </Button>
          )
        }
      >
        <TimeField label="시각" value={time} onChange={setTime} />
        {warnings && (
          <div className={styles.warnBox} role="alert">
            <strong>규칙과 다른 점이 있어요</strong>
            <ul>
              {warnings.map((w) => (
                <li key={w}>{w}</li>
              ))}
            </ul>
          </div>
        )}
      </Sheet>

      <Sheet
        open={skipConfirm}
        onClose={() => setSkipConfirm(false)}
        title="이 시간을 건너뛸까요?"
        description={item.content_id ? '이 예약은 없어지고, 글은 다음 빈 예약으로 옮겨요.' : '이 빈 예약을 없애요. 그날 묶음 수가 하나 줄어요.'}
        footer={
          <>
            <Button variant="secondary" onClick={() => setSkipConfirm(false)}>
              취소
            </Button>
            <Button
              variant="danger"
              busy={act.isPending}
              onClick={async () => {
                setSkipConfirm(false);
                await run(`/api/schedule/reservations/${item.id}/skip`, {}, '건너뛰었어요');
              }}
            >
              건너뛰기
            </Button>
          </>
        }
      >
        <span />
      </Sheet>
    </>
  );
}
