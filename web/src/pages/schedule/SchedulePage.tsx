import { CalendarClock, CalendarOff, History, MoreHorizontal, Pin, Plus, Settings2, Sparkles } from 'lucide-react';
import { useState } from 'react';
import { Link, useNavigate, useSearchParams } from 'react-router';
import { errorMessage } from '../../api/client';
import { useScheduleAction, useScheduleDays } from '../../api/schedule';
import { RES_STATE_LABEL, type ReservationItem, type ScheduleDay } from '../../api/types';
import { PageHeader } from '../../components/layout/AppShell';
import { Badge, type Tone } from '../../components/ui/Badge';
import { Button } from '../../components/ui/Button';
import { Card } from '../../components/ui/Card';
import { IconButton } from '../../components/ui/IconButton';
import { Segmented } from '../../components/ui/Segmented';
import { EmptyState, ErrorState, LoadingList } from '../../components/ui/States';
import { useToast } from '../../components/ui/Toast';
import { addDays, dayLabel, formatTime, localDate } from '../../lib/format';
import { ReservationSheet } from './ReservationSheet';
import { PinSheet } from './PinSheet';
import styles from './SchedulePage.module.css';

type View = 'day' | 'week';

export function SchedulePage() {
  const [params, setParams] = useSearchParams();
  const today = localDate();
  const date = params.get('date') ?? today;
  const view = (params.get('view') as View) ?? 'day';
  const week = useScheduleDays(today, 7);
  const days = week.data?.days ?? [];
  const day = days.find((d) => d.date === date);
  const nav = useNavigate();
  const [selected, setSelected] = useState<ReservationItem | null>(null);
  const [pinOpen, setPinOpen] = useState(false);

  const setDate = (d: string) => setParams({ date: d }, { replace: true });
  const dayOptions = days.slice(0, 3).map((d, i) => ({ value: d.date, label: i === 0 ? '오늘' : i === 1 ? '내일' : dayLabel(d.date).replace(/^\d+월 /, '') }));

  return (
    <>
      <PageHeader
        title="예약"
        actions={
          <>
            <IconButton label="시간 지정해 넣기" onClick={() => setPinOpen(true)}>
              <Plus size={22} />
            </IconButton>
            <IconButton label="게시 기록" onClick={() => nav('/publish')}>
              <History size={22} />
            </IconButton>
            <IconButton label="시간표 설정" onClick={() => nav('/schedule/rules')}>
              <Settings2 size={22} />
            </IconButton>
          </>
        }
      />
      <div className={styles.toolbar}>
        <Segmented
          label="보기"
          value={view}
          onChange={(v) => setParams(v === 'week' ? { view: 'week' } : { date }, { replace: true })}
          options={[
            { value: 'day', label: '날짜별' },
            { value: 'week', label: '주간' },
          ]}
        />
        {view === 'day' && days.length > 0 && (
          <Segmented label="날짜" value={date} onChange={setDate} options={dayOptions.some((o) => o.value === date) ? dayOptions : [...dayOptions, { value: date, label: dayLabel(date) }]} />
        )}
      </div>
      {week.isLoading && <LoadingList rows={4} />}
      {week.isError && <ErrorState error={week.error} onRetry={() => week.refetch()} />}
      {view === 'week' && week.data && <WeekView days={days} onPick={(d) => setParams({ date: d })} />}
      {view === 'day' && week.data && (day ? <DayView day={day} isToday={date === today} onSelect={setSelected} /> : <Card>그 날짜는 아직 시간표가 없어요.</Card>)}
      <ReservationSheet item={selected} day={day} onClose={() => setSelected(null)} />
      <PinSheet open={pinOpen} onClose={() => setPinOpen(false)} defaultDate={date === today ? addDays(today, 1) : date} />
    </>
  );
}

function WeekView({ days, onPick }: { days: ScheduleDay[]; onPick: (d: string) => void }) {
  return (
    <ul className={styles.week} aria-label="주간 예약">
      {days.map((d) => (
        <li key={d.date}>
          <button type="button" className={styles.weekRow} onClick={() => onPick(d.date)}>
            <span className={styles.weekDate}>{dayLabel(d.date)}</span>
            {d.excluded ? (
              <Badge tone="neutral">쉬는 날</Badge>
            ) : (
              <span className={styles.weekCount}>
                <strong>{d.count}</strong>/{d.target}묶음
                <span className={styles.muted}> · X 게시물 {d.posts_approx ? '약 ' : ''}{d.posts}개</span>
              </span>
            )}
            <span className={styles.bar} aria-hidden>
              <span style={{ width: `${Math.min(100, (d.count / Math.max(d.target, 1)) * 100)}%` }} />
            </span>
          </button>
        </li>
      ))}
    </ul>
  );
}

function DayView({ day, isToday, onSelect }: { day: ScheduleDay; isToday: boolean; onSelect: (i: ReservationItem) => void }) {
  const act = useScheduleAction();
  const toast = useToast();
  const pending = day.items.filter((i) => i.state !== 'canceled');
  const groups: { label: string; items: ReservationItem[] }[] = [];
  for (const it of day.items) {
    const label = it.window || '구간 밖';
    const g = groups.find((x) => x.label === label);
    if (g) g.items.push(it);
    else groups.push({ label, items: [it] });
  }
  const short = !day.excluded && day.capacity_max < day.target;

  async function toggleRest() {
    try {
      if (day.excluded) {
        await act.mutateAsync({ path: `/api/schedule/exceptions/${day.date}`, method: 'DELETE' });
        toast.show('쉬는 날을 해제하고 예약을 다시 만들었어요', 'success');
      } else {
        const r = await act.mutateAsync({ path: '/api/schedule/exceptions', body: { date: day.date, note: isToday ? '오늘만 쉬기' : '' } });
        toast.show(`쉬는 날로 정했어요. 예약 ${(r as { closed?: number }).closed ?? 0}개를 건너뛰고 글은 다음 빈 예약으로 옮겼어요`, 'success');
      }
    } catch (e) {
      toast.show(errorMessage(e), 'error');
    }
  }

  return (
    <div className={styles.stack}>
      <Card className={styles.summary}>
        <div className={styles.summaryHead}>
          <h2>{dayLabel(day.date)}</h2>
          <Button size="sm" variant="ghost" icon={<CalendarOff size={16} />} busy={act.isPending} onClick={toggleRest}>
            {day.excluded ? '쉬는 날 해제' : isToday ? '오늘만 쉬기' : '이날 쉬기'}
          </Button>
        </div>
        {day.excluded ? (
          <p className={styles.muted}>쉬는 날이에요{day.exclude_note ? ` (${day.exclude_note})` : ''}. 시간 고정 예약만 남아요.</p>
        ) : (
          <p className={styles.counts}>
            <strong>{pending.length}</strong> / {day.target}묶음 · X 게시물 {day.posts_approx ? '약 ' : ''}
            {day.posts}개
          </p>
        )}
        <div className={styles.windows}>
          {day.intervals.map((iv) => (
            <span key={iv.start} className={styles.chip}>
              {iv.labels.join('·')} {formatTime(iv.start)}~{formatTime(iv.end)}
            </span>
          ))}
          {day.intervals.length === 0 && <span className={styles.muted}>이날은 허용 구간이 없어요</span>}
        </div>
        {short && (
          <p className={styles.warn}>
            지금 구간·간격으로는 하루 최대 {day.capacity_max}개까지 넣을 수 있어 {day.target - day.capacity_max}개가 부족해요. 간격을 줄이거나 시간을 넘겨 억지로
            채우지 않아요. <Link to="/schedule/rules">구간 늘리기·수량 줄이기</Link>
          </p>
        )}
      </Card>

      {day.items.length === 0 && !day.excluded && (
        <Card>
          <EmptyState icon={<CalendarClock size={28} aria-hidden />} title="예약이 없어요" body="허용 구간이 지났거나 아직 만들 시간이 아니에요." />
        </Card>
      )}
      {groups.map((g) => (
        <section key={g.label} className={styles.group} aria-label={g.label}>
          <h3 className={styles.groupTitle}>{g.label}</h3>
          <ol className={styles.list}>
            {g.items.map((it) => (
              <ReservationRow key={it.id} item={it} onOpen={() => onSelect(it)} />
            ))}
          </ol>
        </section>
      ))}
    </div>
  );
}

export function stateBadge(it: ReservationItem): { tone: Tone; label: string } {
  if (it.state === 'canceled') return { tone: 'neutral', label: '건너뜀' };
  if (it.state === 'published') return { tone: 'success', label: '게시 완료' };
  if (it.state === 'publishing') return { tone: 'progress', label: '게시 중' };
  if (it.state === 'partial' || it.state === 'unknown' || it.state === 'failed') return { tone: 'danger', label: RES_STATE_LABEL[it.state] };
  if (it.generating || it.wait_reason === '생성 중') return { tone: 'progress', label: '생성 중' };
  if (it.state === 'assigned') return { tone: 'success', label: '준비 완료' };
  if (it.state === 'waiting') return { tone: 'warning', label: it.wait_reason ?? '대기' };
  return { tone: 'neutral', label: it.wait_reason === '준비된 글 없음' ? '빈 예약' : (it.wait_reason ?? '빈 예약') };
}

function ReservationRow({ item, onOpen }: { item: ReservationItem; onOpen: () => void }) {
  const b = stateBadge(item);
  const moved = item.original_slot_at !== item.slot_at;
  const done = item.state === 'canceled' || item.state === 'published';
  return (
    <li className={[styles.row, done ? styles.done : ''].join(' ')}>
      <button type="button" className={styles.rowBtn} onClick={onOpen} aria-label={`${formatTime(item.slot_at)} 예약 ${item.title ?? '빈 예약'} 관리`}>
        <span className={styles.time}>
          <strong>{formatTime(item.slot_at)}</strong>
          {item.pinned && <Pin size={14} aria-label="시간 고정" />}
          {moved && <span className={styles.orig}>원래 {formatTime(item.original_slot_at)}</span>}
          {!moved && item.jitter_sec > 0 && <span className={styles.orig}>+{Math.round(item.jitter_sec / 60)}분</span>}
        </span>
        <span className={styles.body}>
          <span className={styles.title}>{item.title ?? (item.content_id ? '제목 없음' : '빈 예약')}</span>
          <span className={styles.meta}>
            <Badge tone={b.tone}>{b.label}</Badge>
            {item.content_id && item.part_count > 0 && <span>X 게시물 {item.part_count}개</span>}
            {item.image_count > 0 && <span>이미지 {item.image_count}</span>}
            {!item.content_id && item.generation_due_at && item.state !== 'canceled' && (
              <span>
                <Sparkles size={12} aria-hidden /> 생성 예정 {formatTime(item.generation_due_at)}
              </span>
            )}
            {item.unapproved_change && <span>승인본으로 게시</span>}
          </span>
          {item.delay_note && <span className={styles.note}>{item.delay_note}</span>}
          {item.block_reason && !done && <span className={styles.blocked}>시작 못 함: {item.block_reason}</span>}
          {item.run_note && !done && <span className={styles.note}>{item.run_note}</span>}
        </span>
        {!done && <MoreHorizontal size={20} aria-hidden className={styles.more} />}
      </button>
    </li>
  );
}
