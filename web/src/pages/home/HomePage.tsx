import { ArrowRight, CalendarClock, CircleAlert, FilePlus2, Info, Pause, Play, Power, Sparkles, TriangleAlert } from 'lucide-react';
import { useState } from 'react';
import { Link, useNavigate } from 'react-router';
import { errorMessage } from '../../api/client';
import { useAutomation, useHome, type HomeAction, type HomeData, type HomeSlot } from '../../api/home';
import { useSettings } from '../../api/queries';
import { RES_STATE_LABEL, type ReservationState } from '../../api/types';
import { PageHeader } from '../../components/layout/AppShell';
import { Badge, type Tone } from '../../components/ui/Badge';
import { Button } from '../../components/ui/Button';
import { Card, CardTitle, Section } from '../../components/ui/Card';
import { Row, RowGroup } from '../../components/ui/Row';
import { Sheet } from '../../components/ui/Sheet';
import { ErrorState, LoadingList } from '../../components/ui/States';
import { Switch } from '../../components/ui/Switch';
import { useToast } from '../../components/ui/Toast';
import { dayLabel, formatDate, formatMoney, formatTime, localDate } from '../../lib/format';
import styles from './HomePage.module.css';

// 운영 홈: 다음 게시, 오늘 수량, 필요한 조치, 자동 운영 상태, 가까운 예약. 모든 수치는 서버의 실제 상태다
export function HomePage() {
  const settings = useSettings();
  const home = useHome();
  const nav = useNavigate();
  return (
    <>
      <PageHeader title="홈" subtitle={formatDate(new Date().toISOString())} />
      {home.isLoading && <LoadingList rows={4} />}
      {home.isError && <ErrorState error={home.error} onRetry={() => home.refetch()} />}
      {home.data && (
        <div className={styles.stack}>
          {settings.data && !settings.data.setup_completed_at && (
            <Card className={styles.setup}>
              <CardTitle>처음이라면 설정부터 해요</CardTitle>
              <p className={styles.muted}>연결 상태, 프롬프트, 운영 모드, 시간표를 차례로 확인해요.</p>
              <Link to="/setup" className={styles.cta}>
                첫 설정 시작 <ArrowRight size={18} aria-hidden />
              </Link>
            </Card>
          )}
          <NextCard data={home.data} />
          <TodayCard data={home.data} />
          <Actions actions={home.data.actions} />
          <AutomationCard data={home.data} />
          <Upcoming slots={home.data.upcoming} />
          <div className={styles.quick}>
            <Button variant="secondary" icon={<Sparkles size={18} />} onClick={() => nav('/contents?new=generate')}>
              글 생성
            </Button>
            <Button variant="secondary" icon={<FilePlus2 size={18} />} onClick={() => nav('/contents?new=write')}>
              직접 등록
            </Button>
          </div>
          <UsageCard data={home.data} />
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
        <p className={styles.muted}>글을 만들거나 등록하면 빈 예약에 자동으로 들어가요.</p>
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
          <TriangleAlert size={16} aria-hidden /> 시작하지 못한 이유: {n.block_reason}
        </p>
      )}
    </Card>
  );
}

function TodayCard({ data }: { data: HomeData }) {
  const t = data.today;
  return (
    <Card>
      <dl className={styles.stats}>
        <div>
          <dt>오늘</dt>
          <dd>
            <strong>{t.published}</strong> / {t.target}묶음
          </dd>
        </div>
        <div>
          <dt>남은 예약</dt>
          <dd>
            <strong>{t.remaining}</strong>개{t.in_progress > 0 && <span className={styles.sub}> · 진행·확인 {t.in_progress}</span>}
          </dd>
        </div>
        <div>
          <dt>오늘 올라간 X 게시물</dt>
          <dd>
            <strong>{t.posts}</strong>개<span className={styles.sub}> (본문 + 답글)</span>
          </dd>
        </div>
        <div>
          <dt>준비된 글</dt>
          <dd>
            <Link to="/contents?stage=ready">
              <strong>{data.stock.ready}</strong>개
            </Link>
            {data.stock.review > 0 && <span className={styles.sub}> · 검토 대기 {data.stock.review}</span>}
          </dd>
        </div>
      </dl>
    </Card>
  );
}

const SEV_ICON = {
  critical: <CircleAlert size={20} className={styles.crit} aria-label="중요" />,
  warning: <TriangleAlert size={20} className={styles.warnIcon} aria-label="주의" />,
  info: <Info size={20} className={styles.infoIcon} aria-label="안내" />,
};

// 개수가 아니라 상태 하나인 조치 (연결·예산·요율표)
const SINGLE_STATE = new Set(['x_connection', 'budget', 'pricing']);

function Actions({ actions }: { actions: HomeAction[] }) {
  return (
    <Section title="필요한 조치">
      {actions.length === 0 ? (
        <Card>
          <p className={styles.muted}>지금은 처리할 일이 없어요.</p>
        </Card>
      ) : (
        <RowGroup>
          {actions.map((a) => (
            <Row key={a.key} icon={SEV_ICON[a.severity]} label={a.title} description={a.body} value={SINGLE_STATE.has(a.key) ? undefined : `${a.count}개`} to={a.link} />
          ))}
        </RowGroup>
      )}
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

  return (
    <Card>
      <CardTitle action={<Badge tone={a.enabled ? (otherBlockers.length ? 'warning' : 'success') : 'neutral'}>{a.enabled ? (otherBlockers.length ? '켜짐 · 멈춤 있음' : '켜짐') : '꺼짐'}</Badge>}>
        <span className={styles.inline}>
          <Power size={20} aria-hidden /> 자동 운영
        </span>
      </CardTitle>
      {a.mock && <p className={styles.mock}>목업 모드라 실제 X에는 게시하지 않아요.</p>}
      {a.enabled && otherBlockers.length === 0 && <p className={styles.muted}>예약된 시각에 맞춰 게시하고 있어요.</p>}
      {(a.enabled ? otherBlockers : a.publish_blockers).length > 0 && (
        <div className={styles.reasonBox}>
          <p className={styles.reasonTitle}>예약 게시가 멈춘 이유</p>
          <ul className={styles.reasons}>
            {(a.enabled ? otherBlockers : a.publish_blockers).map((r) => (
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
      <div className={styles.automationActions}>
        {a.enabled ? (
          <Button variant="secondary" icon={<Pause size={18} />} onClick={() => setConfirm('stop')}>
            자동 운영 멈추기
          </Button>
        ) : (
          <Button icon={<Play size={18} />} onClick={() => setConfirm('start')}>
            자동 운영 시작
          </Button>
        )}
      </div>
      <RowGroup>
        <Row
          label="생성 일시정지"
          description="자동·버튼 생성 모두 멈춰요. 이미 만든 글은 그대로 게시해요"
          trailing={<Switch label="생성 일시정지" checked={a.generation_paused} disabled={act.isPending} onChange={(v) => set({ generation_paused: v }, v ? '생성을 멈췄어요' : '생성을 다시 시작했어요')} />}
        />
        <Row
          label="게시 일시정지"
          description="새 묶음과 남은 답글을 보내지 않아요. 지난 예약은 다음 빈 시간으로 옮겨요"
          trailing={<Switch label="게시 일시정지" checked={a.publishing_paused} disabled={act.isPending} onChange={(v) => set({ publishing_paused: v }, v ? '게시를 멈췄어요' : '게시를 다시 시작했어요')} />}
        />
      </RowGroup>

      <Sheet
        open={confirm === 'start'}
        onClose={() => setConfirm(null)}
        title="자동 운영을 시작할까요?"
        description="예약 시각마다 X에 본문과 답글을 올리고, 설정한 방식대로 글을 미리 만들어요. 언제든 여기서 멈출 수 있어요."
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
        description="새 예약 게시와 자동 생성을 멈춰요. 본문이 이미 올라간 묶음의 남은 답글은 다시 시작하면 이어서 올려요."
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
    </Card>
  );
}

function Upcoming({ slots }: { slots: HomeSlot[] }) {
  return (
    <Section title="가까운 예약" action={<Link to="/schedule" className={styles.more}>예약 전체</Link>}>
      {slots.length === 0 ? (
        <Card>
          <p className={styles.muted}>앞으로 잡힌 예약이 없어요. 시간표 설정을 확인해 주세요.</p>
        </Card>
      ) : (
        <RowGroup>
          {slots.map((s) => (
            <Row
              key={s.id}
              icon={<CalendarClock size={20} />}
              label={
                <span className={styles.slotLabel}>
                  <strong>{formatTime(s.slot_at)}</strong> {s.title || (s.content_id ? '제목 없음' : '빈 예약')}
                </span>
              }
              value={<Badge tone={slotTone(s)}>{slotLabel(s)}</Badge>}
              to={`/schedule?date=${localDate(new Date(s.slot_at))}`}
            />
          ))}
        </RowGroup>
      )}
    </Section>
  );
}

const CATEGORY_LABEL: Record<string, string> = { ai_text: '글 생성', ai_image: '이미지 생성', x_post: 'X 게시', x_media: 'X 미디어', storage: '저장' };

function UsageCard({ data }: { data: HomeData }) {
  const u = data.usage;
  const total = u.spent_micros;
  return (
    <Card>
      <CardTitle action={<Link to="/settings/budget" className={styles.more}>사용량·예산</Link>}>이번 달 추정 사용량</CardTitle>
      {u.pricing_known ? (
        <p className={styles.usage}>
          <strong>{formatMoney(total, u.currency)}</strong>
          {u.limit_micros > 0 && <span className={styles.sub}> / 한도 {formatMoney(u.limit_micros, u.currency)}</span>}
        </p>
      ) : (
        <p className={styles.muted}>요율표가 비어 있어 금액 대신 사용량만 보여요.</p>
      )}
      <p className={styles.muted}>
        {Object.keys(u.counts).length === 0
          ? '이번 달 사용 기록이 아직 없어요.'
          : Object.entries(u.counts)
              .map(([k, n]) => `${CATEGORY_LABEL[k] ?? k} ${n}건`)
              .join(' · ')}
      </p>
      <p className={styles.caption}>앱이 계산한 추정치예요. 실제 청구액은 각 서비스 콘솔에서 확인하세요.</p>
    </Card>
  );
}
