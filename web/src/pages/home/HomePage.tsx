import { ArrowRight, CalendarClock, CircleAlert, FilePlus2, Info, Pause, Play, Power, Sparkles, TriangleAlert, Wallet } from 'lucide-react';
import { useState } from 'react';
import { Link } from 'react-router';
import { errorMessage } from '../../api/client';
import { useAutomation, useHome, type HomeAction, type HomeData, type HomeSlot } from '../../api/home';
import { useSettings } from '../../api/queries';
import { RES_STATE_LABEL, type ReservationState } from '../../api/types';
import { PageHeader } from '../../components/layout/AppShell';
import { Badge, type Tone } from '../../components/ui/Badge';
import { Button } from '../../components/ui/Button';
import { Card, Section } from '../../components/ui/Card';
import { Row, RowGroup } from '../../components/ui/Row';
import { Sheet } from '../../components/ui/Sheet';
import { ErrorState, LoadingList } from '../../components/ui/States';
import { Switch } from '../../components/ui/Switch';
import { useToast } from '../../components/ui/Toast';
import { dayLabel, formatDate, formatMoney, formatTime, localDate } from '../../lib/format';
import styles from './HomePage.module.css';

// 운영 홈: 다음 게시 → 오늘 수량 → 필요한 조치 → 자동 운영 순으로, 화면마다 지금 알아야 할 것 하나를 먼저 보여 준다.
// 모든 수치는 서버의 실제 상태다
export function HomePage() {
  const settings = useSettings();
  const home = useHome();
  return (
    <>
      <PageHeader title="홈" subtitle={formatDate(new Date().toISOString())} />
      {home.isLoading && <LoadingList rows={4} />}
      {home.isError && <ErrorState error={home.error} onRetry={() => home.refetch()} />}
      {home.data && (
        <div className={styles.stack}>
          {settings.data && !settings.data.setup_completed_at && (
            <Card className={styles.setup}>
              <p className={styles.setupTitle}>처음이라면 설정부터 해요</p>
              <p className={styles.setupBody}>연결부터 시간표까지 5단계면 끝나요.</p>
              <Link to="/setup" className={styles.setupCta}>
                첫 설정 시작 <ArrowRight size={18} aria-hidden />
              </Link>
            </Card>
          )}
          <NextCard data={home.data} />
          <TodayCard data={home.data} />
          <Actions actions={home.data.actions} />
          <AutomationCard data={home.data} />
          <Upcoming slots={home.data.upcoming} />
          <Quick />
          <UsageRow data={home.data} />
        </div>
      )}
    </>
  );
}

function slotTone(s: HomeSlot): Tone {
  if (s.state === 'publishing') return 'progress';
  if (s.block_reason) return 'warning';
  if (s.state === 'assigned') return 'success';
  if (s.state === 'waiting') return 'warning';
  return 'neutral';
}

function slotLabel(s: HomeSlot): string {
  if (s.generating) return '생성 중';
  if (s.state === 'waiting') return s.wait_reason ?? '대기';
  if (s.state === 'open') return s.generation_due_at ? `생성 예정 ${formatTime(s.generation_due_at)}` : '빈 예약';
  return RES_STATE_LABEL[s.state as ReservationState] ?? s.state;
}

function NextCard({ data }: { data: HomeData }) {
  const n = data.next;
  if (!n)
    return (
      <Card>
        <p className={styles.eyebrow}>다음 게시</p>
        <p className={styles.bigEmpty}>예정된 게시가 없어요</p>
        <p className={styles.muted}>글을 등록하면 빈 예약에 알아서 들어가요.</p>
      </Card>
    );
  const today = localDate(new Date(data.now));
  const date = localDate(new Date(n.slot_at));
  return (
    <Card>
      <Link to={`/schedule?date=${date}`} className={styles.nextLink}>
        <p className={styles.eyebrow}>다음 게시</p>
        <p className={styles.big}>
          {date !== today && <span className={styles.bigDay}>{dayLabel(date)} </span>}
          {formatTime(n.slot_at)}
        </p>
        <p className={styles.nextTitle}>{n.title || n.preview || '제목 없음'}</p>
        <span className={styles.meta}>
          <Badge tone={slotTone(n)}>{slotLabel(n)}</Badge>
          {n.part_count > 0 && <span>X 게시물 {n.part_count}개</span>}
        </span>
      </Link>
      {n.block_reason && (
        <p className={styles.blocked} role="note">
          <TriangleAlert size={16} aria-hidden /> {n.block_reason}
        </p>
      )}
    </Card>
  );
}

function TodayCard({ data }: { data: HomeData }) {
  const t = data.today;
  return (
    <Card>
      <p className={styles.cardHead}>오늘</p>
      <dl className={styles.stats}>
        <div>
          <dt>게시한 묶음</dt>
          <dd>
            <strong>{t.published}</strong> / {t.target}묶음
          </dd>
        </div>
        <div>
          <dt>남은 예약</dt>
          <dd>
            <strong>{t.remaining}</strong>개{t.in_progress > 0 && <span className={styles.sub}> · 진행 {t.in_progress}</span>}
          </dd>
        </div>
        <div>
          <dt>올라간 X 게시물</dt>
          <dd>
            <strong>{t.posts}</strong>개
          </dd>
        </div>
        <div>
          <dt>준비된 글</dt>
          <dd>
            <Link to="/contents?stage=ready" className={styles.statLink}>
              <strong>{data.stock.ready}</strong>개
            </Link>
            {data.stock.review > 0 && <span className={styles.sub}> · 검토 {data.stock.review}</span>}
          </dd>
        </div>
      </dl>
    </Card>
  );
}

const SEV_ICON = {
  critical: <CircleAlert aria-label="중요" />,
  warning: <TriangleAlert aria-label="주의" />,
  info: <Info aria-label="안내" />,
};
const SEV_TONE = { critical: 'red', warning: 'orange', info: 'blue' } as const;

// 개수가 아니라 상태 하나인 조치 (연결·예산·요율표)
const SINGLE_STATE = new Set(['x_connection', 'budget', 'pricing']);

// 처리할 일이 없으면 영역 자체를 보여 주지 않는다
function Actions({ actions }: { actions: HomeAction[] }) {
  if (actions.length === 0) return null;
  return (
    <Section title="필요한 조치">
      <RowGroup>
        {actions.map((a) => (
          <Row
            key={a.key}
            icon={SEV_ICON[a.severity]}
            iconTone={SEV_TONE[a.severity]}
            label={a.title}
            description={a.body}
            value={SINGLE_STATE.has(a.key) ? undefined : `${a.count}개`}
            to={a.link}
          />
        ))}
      </RowGroup>
    </Section>
  );
}

function AutomationCard({ data }: { data: HomeData }) {
  const a = data.automation;
  const act = useAutomation();
  const toast = useToast();
  const [confirm, setConfirm] = useState<'start' | 'stop' | null>(null);
  const otherBlockers = a.publish_blockers.filter((b) => !b.includes('자동 운영'));

  async function set(body: Parameters<typeof act.mutateAsync>[0], done: string) {
    try {
      const r = await act.mutateAsync(body);
      toast.show(r.fill_note ? `${done} · ${r.fill_note}` : r.fill_shortfall ? `${done} · 부족한 글 ${r.fill_shortfall}개를 만들기 시작했어요` : done, 'success');
      setConfirm(null);
    } catch (e) {
      toast.show(errorMessage(e), 'error');
    }
  }

  const blockers = a.enabled ? otherBlockers : a.publish_blockers;
  const running = a.enabled && otherBlockers.length === 0;
  return (
    <Section title="자동 운영">
      <Card>
        <div className={styles.autoHead}>
          <span className={[styles.autoIcon, running ? styles.autoOn : a.enabled ? styles.autoWarn : ''].join(' ')}>
            <Power size={22} aria-hidden />
          </span>
          <div className={styles.autoText}>
            <p className={styles.autoTitle}>{running ? '예약 시각에 맞춰 게시 중이에요' : a.enabled ? '켜져 있지만 게시가 멈췄어요' : '아직 꺼져 있어요'}</p>
            <p className={styles.muted}>{a.enabled ? '언제든 멈출 수 있어요' : '시작해야 예약 게시와 자동 생성이 돌아가요'}</p>
          </div>
        </div>
        {blockers.length > 0 && (
          <div className={styles.reasonBox}>
            <p className={styles.reasonTitle}>게시가 멈춘 이유</p>
            <ul className={styles.reasons}>
              {blockers.map((r) => (
                <li key={r}>{r}</li>
              ))}
            </ul>
          </div>
        )}
        {a.generation_blockers.length > 0 && (
          <div className={styles.reasonBox}>
            <p className={styles.reasonTitle}>자동 생성이 멈춘 이유</p>
            <ul className={styles.reasons}>
              {a.generation_blockers.map((r) => (
                <li key={r}>{r}</li>
              ))}
            </ul>
          </div>
        )}
        {a.enabled ? (
          <Button variant="secondary" block icon={<Pause size={18} />} onClick={() => setConfirm('stop')}>
            자동 운영 멈추기
          </Button>
        ) : (
          <Button block icon={<Play size={18} />} onClick={() => setConfirm('start')}>
            자동 운영 시작
          </Button>
        )}
      </Card>
      <RowGroup>
        <Row
          label="생성 일시정지"
          description="만든 글은 계속 게시해요"
          trailing={<Switch label="생성 일시정지" checked={a.generation_paused} disabled={act.isPending} onChange={(v) => set({ generation_paused: v }, v ? '생성을 멈췄어요' : '생성을 다시 시작했어요')} />}
        />
        <Row
          label="게시 일시정지"
          description="지난 예약은 다음 빈 시간으로 옮겨요"
          trailing={<Switch label="게시 일시정지" checked={a.publishing_paused} disabled={act.isPending} onChange={(v) => set({ publishing_paused: v }, v ? '게시를 멈췄어요' : '게시를 다시 시작했어요')} />}
        />
      </RowGroup>

      <Sheet
        open={confirm === 'start'}
        onClose={() => setConfirm(null)}
        title="자동 운영을 시작할까요?"
        description="예약 시각마다 본문과 답글을 X에 올리고, 설정한 방식대로 글을 미리 만들어요."
        footer={
          <>
            <Button variant="secondary" onClick={() => setConfirm(null)}>
              취소
            </Button>
            <Button busy={act.isPending} icon={<Play size={18} />} onClick={() => set({ automation_enabled: true }, '자동 운영을 시작했어요')}>
              시작
            </Button>
          </>
        }
      >
        {otherBlockers.length > 0 ? (
          <div className={styles.reasonBox}>
            <p className={styles.reasonTitle}>시작해도 아래 이유로 아직 게시하지 않아요</p>
            <ul className={styles.reasons}>
              {otherBlockers.map((r) => (
                <li key={r}>{r}</li>
              ))}
            </ul>
          </div>
        ) : (
          <p className={styles.muted}>{a.mock ? '목업 모드라 테스트 대역에만 기록돼요.' : '연결된 X 계정에 실제로 게시해요.'}</p>
        )}
      </Sheet>
      <Sheet
        open={confirm === 'stop'}
        onClose={() => setConfirm(null)}
        title="자동 운영을 멈출까요?"
        description="새 예약 게시와 자동 생성을 멈춰요. 이미 올라간 본문의 남은 답글은 다시 시작하면 이어서 올려요."
        footer={
          <>
            <Button variant="secondary" onClick={() => setConfirm(null)}>
              취소
            </Button>
            <Button variant="danger" busy={act.isPending} onClick={() => set({ automation_enabled: false }, '자동 운영을 멈췄어요')}>
              멈추기
            </Button>
          </>
        }
      >
        <p className={styles.muted}>지금 게시 중인 요청은 끝까지 결과를 저장해요.</p>
      </Sheet>
    </Section>
  );
}

function Upcoming({ slots }: { slots: HomeSlot[] }) {
  return (
    <Section title="가까운 예약" action={<Link to="/schedule" className={styles.more}>예약 전체</Link>}>
      {slots.length === 0 ? (
        <Card>
          <p className={styles.muted}>앞으로 잡힌 예약이 없어요. 시간표에서 허용 구간을 확인해 보세요.</p>
        </Card>
      ) : (
        <RowGroup>
          {slots.map((s) => (
            <Row
              key={s.id}
              icon={<CalendarClock />}
              label={
                <span className={styles.slotLabel}>
                  <strong>{formatTime(s.slot_at)}</strong> {s.title || (s.content_id ? '제목 없음' : '빈 예약')}
                </span>
              }
              description={s.state === 'open' && !s.block_reason && !s.generating ? slotLabel(s) : undefined}
              value={s.state === 'open' && !s.block_reason && !s.generating ? undefined : <Badge tone={slotTone(s)}>{slotLabel(s)}</Badge>}
              to={`/schedule?date=${localDate(new Date(s.slot_at))}`}
            />
          ))}
        </RowGroup>
      )}
    </Section>
  );
}

const CATEGORY_LABEL: Record<string, string> = { ai_text: '글 생성', ai_image: '이미지 생성', x_post: 'X 게시', x_media: 'X 미디어', storage: '저장' };

// 바로 하기: 홈에서 자주 시작하는 두 가지
function Quick() {
  return (
    <div className={styles.quick}>
      <Link to="/contents?new=generate" className={styles.tile}>
        <span className={[styles.tileIcon, styles.tileBlue].join(' ')}>
          <Sparkles size={22} aria-hidden />
        </span>
        글 생성
      </Link>
      <Link to="/contents?new=write" className={styles.tile}>
        <span className={styles.tileIcon}>
          <FilePlus2 size={22} aria-hidden />
        </span>
        직접 등록
      </Link>
    </div>
  );
}

function UsageRow({ data }: { data: HomeData }) {
  const u = data.usage;
  const counts = Object.entries(u.counts)
    .map(([k, n]) => `${CATEGORY_LABEL[k] ?? k} ${n}건`)
    .join(' · ');
  return (
    <RowGroup>
      <Row
        icon={<Wallet />}
        label="이번 달 추정 사용량"
        description={counts || '아직 사용 기록이 없어요'}
        value={u.pricing_known ? `${formatMoney(u.spent_micros, u.currency)}${u.limit_micros > 0 ? ` / ${formatMoney(u.limit_micros, u.currency)}` : ''}` : undefined}
        to="/settings/budget"
      />
    </RowGroup>
  );
}
