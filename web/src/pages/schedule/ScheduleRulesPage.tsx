import { Plus, Trash2 } from 'lucide-react';
import { useState } from 'react';
import { ApiError, errorMessage } from '../../api/client';
import { previewRules, useSaveScheduleRules, useScheduleAction, useScheduleSettings, type RulesBody } from '../../api/schedule';
import type { SchedulePlanReport, ScheduleRules, ScheduleSettings, ScheduleWindow } from '../../api/types';
import { PageHeader } from '../../components/layout/AppShell';
import { Button } from '../../components/ui/Button';
import { Card, CardTitle, Section } from '../../components/ui/Card';
import { ChoiceList } from '../../components/ui/ChoiceList';
import { IconButton } from '../../components/ui/IconButton';
import { Row, RowGroup } from '../../components/ui/Row';
import { Sheet } from '../../components/ui/Sheet';
import { ErrorState, LoadingList } from '../../components/ui/States';
import { Switch } from '../../components/ui/Switch';
import { TextField } from '../../components/ui/TextField';
import { useToast } from '../../components/ui/Toast';
import { addDays, dayLabel, formatTime, localDate, minutesToHM } from '../../lib/format';
import { BackButton } from '../settings/ConnectionsPage';
import styles from './ScheduleRulesPage.module.css';

const WEEK = ['일', '월', '화', '수', '목', '금', '토'];
const PRESETS: { label: string; days: number[] }[] = [
  { label: '매일', days: [0, 1, 2, 3, 4, 5, 6] },
  { label: '평일', days: [1, 2, 3, 4, 5] },
  { label: '주말', days: [0, 6] },
];

export function ScheduleRulesPage() {
  const q = useScheduleSettings();
  return (
    <>
      <PageHeader title="시간표" subtitle="허용 구간·하루 묶음 수·간격" back={<BackButton to="/schedule" />} />
      {q.isLoading && <LoadingList rows={4} />}
      {q.isError && <ErrorState error={q.error} onRetry={() => q.refetch()} />}
      {q.data && <RulesForm key={q.data.rules.version} data={q.data} />}
    </>
  );
}

const toMin = (hm: string) => {
  const [h, m] = hm.split(':').map(Number);
  return (h ?? 0) * 60 + (m ?? 0);
};

function RulesForm({ data }: { data: ScheduleSettings }) {
  const [rules, setRules] = useState<ScheduleRules>(data.rules);
  const [windows, setWindows] = useState<ScheduleWindow[]>(data.windows);
  const [fields, setFields] = useState<Record<string, string>>({});
  const [scope, setScope] = useState<RulesBody['scope']>('tomorrow');
  const [report, setReport] = useState<SchedulePlanReport | null>(null);
  const [busy, setBusy] = useState(false);
  const save = useSaveScheduleRules();
  const toast = useToast();
  const dirty = JSON.stringify(rules) !== JSON.stringify(data.rules) || JSON.stringify(windows) !== JSON.stringify(data.windows);
  const r = <K extends keyof ScheduleRules>(k: K, v: ScheduleRules[K]) => setRules((p) => ({ ...p, [k]: v }));
  const w = (i: number, patch: Partial<ScheduleWindow>) => setWindows((ws) => ws.map((x, j) => (j === i ? { ...x, ...patch } : x)));
  const num = (v: string) => (v === '' ? 0 : Number(v));

  async function preview() {
    setBusy(true);
    setFields({});
    try {
      const seed = Math.floor(Math.random() * 2 ** 31) + 1;
      setReport(await previewRules({ rules, windows, scope, seed }));
    } catch (e) {
      if (e instanceof ApiError && Object.keys(e.fields).length) {
        setFields(e.fields);
        toast.show('입력값을 확인해 주세요', 'error');
      } else toast.show(errorMessage(e), 'error');
    } finally {
      setBusy(false);
    }
  }

  async function apply() {
    if (!report) return;
    try {
      await save.mutateAsync({ rules, windows, scope, seed: report.seed });
      toast.show(scope === 'later' ? '저장했어요. 앞으로 새로 만드는 예약부터 적용돼요' : '저장하고 예약을 다시 배치했어요', 'success');
      setReport(null);
    } catch (e) {
      toast.show(e instanceof ApiError && e.status === 409 ? '다른 곳에서 먼저 바뀌었어요. 새로고침 후 다시 해 주세요' : errorMessage(e), 'error');
    }
  }

  return (
    <div className={styles.stack}>
      <Section title="허용 구간" description="이 시간 안에서만 자동으로 새 묶음을 시작해요. 시작 포함·끝 제외, 끝이 시작보다 이르면 다음 날로 이어져요">
        {windows.map((win, i) => {
          const overnight = win.end_minute <= win.start_minute;
          return (
            <Card key={i} className={styles.window}>
              <div className={styles.windowHead}>
                <TextField label="이름" value={win.label} onChange={(e) => w(i, { label: e.target.value })} error={fields[`windows.${i}.label`]} />
                <Switch label={`${win.label} 구간 사용`} checked={win.enabled} onChange={(v) => w(i, { enabled: v })} />
                <IconButton label={`${win.label} 구간 삭제`} onClick={() => setWindows((ws) => ws.filter((_, j) => j !== i))}>
                  <Trash2 size={18} />
                </IconButton>
              </div>
              <div className={styles.times}>
                <TextField label="시작" type="time" value={minutesToHM(win.start_minute)} onChange={(e) => w(i, { start_minute: toMin(e.target.value) })} error={fields[`windows.${i}.start_minute`]} />
                <TextField
                  label="끝"
                  type="time"
                  value={minutesToHM(win.end_minute)}
                  onChange={(e) => w(i, { end_minute: toMin(e.target.value) || (win.start_minute > 0 ? 0 : 1440) })}
                  error={fields[`windows.${i}.end_minute`]}
                  hint={overnight ? `다음 날 ${minutesToHM(win.end_minute)}까지` : undefined}
                />
              </div>
              <div className={styles.days} role="group" aria-label={`${win.label} 적용 요일`}>
                {WEEK.map((d, k) => (
                  <button
                    key={d}
                    type="button"
                    aria-pressed={win.weekdays.includes(k)}
                    className={[styles.day, win.weekdays.includes(k) ? styles.dayOn : ''].join(' ')}
                    onClick={() => w(i, { weekdays: win.weekdays.includes(k) ? win.weekdays.filter((x) => x !== k) : [...win.weekdays, k].sort() })}
                  >
                    {d}
                  </button>
                ))}
                <span className={styles.presets}>
                  {PRESETS.map((p) => (
                    <button key={p.label} type="button" className={styles.preset} onClick={() => w(i, { weekdays: p.days })}>
                      {p.label}
                    </button>
                  ))}
                </span>
              </div>
              {fields[`windows.${i}.weekdays`] && <p className={styles.err}>{fields[`windows.${i}.weekdays`]}</p>}
            </Card>
          );
        })}
        <Button
          variant="secondary"
          icon={<Plus size={18} />}
          onClick={() => setWindows((ws) => [...ws, { label: `구간 ${ws.length + 1}`, weekdays: [0, 1, 2, 3, 4, 5, 6], start_minute: 1200, end_minute: 1320, enabled: true }])}
        >
          구간 추가
        </Button>
        <p className={styles.muted}>겹치거나 맞닿은 구간은 합쳐서 계산해 같은 시각을 두 번 쓰지 않아요.</p>
      </Section>

      <Section title="수량과 간격" description="다음 예약 = 이전 예약 + 기본 간격 + 추가 지연(0 이상). 지연값은 예약을 만들 때 한 번 정해 저장해요">
        <Card>
          <div className={styles.grid}>
            <TextField label="하루 묶음 수" type="number" inputMode="numeric" value={String(rules.daily_target)} onChange={(e) => r('daily_target', num(e.target.value))} error={fields['rules.daily_target']} hint="본문+답글 한 묶음 기준. X 게시물 수는 더 많아요" />
            <TextField label="기본 간격 (분)" type="number" inputMode="numeric" value={String(rules.base_interval_min)} onChange={(e) => r('base_interval_min', num(e.target.value))} error={fields['rules.base_interval_min']} />
            <TextField label="추가 지연 최소 (분)" type="number" inputMode="numeric" value={String(rules.jitter_min_min)} onChange={(e) => r('jitter_min_min', num(e.target.value))} />
            <TextField label="추가 지연 최대 (분)" type="number" inputMode="numeric" value={String(rules.jitter_max_min)} onChange={(e) => r('jitter_max_min', num(e.target.value))} error={fields['rules.jitter_max_min']} />
            <TextField label="답글 간격 (초)" type="number" inputMode="numeric" value={String(rules.reply_gap_sec)} onChange={(e) => r('reply_gap_sec', num(e.target.value))} error={fields['rules.reply_gap_sec']} hint="본문 게시 확인 뒤 답글까지 기다리는 시간" />
            <TextField label="구간 끝 여유 (분)" type="number" inputMode="numeric" value={String(rules.end_margin_min)} onChange={(e) => r('end_margin_min', num(e.target.value))} error={fields['rules.end_margin_min']} hint="구간이 끝나기 직전에는 새 묶음을 시작하지 않아요" />
          </div>
          <p className={styles.muted}>
            저장된 설정으로는 하루 약 {data.capacity[0]}~{data.capacity[1]}개까지 넣을 수 있어요. 넣을 수 없는 수량은 억지로 채우지 않아요. 추가 지연은 간격을 자연스럽게 하려는 것이고 계정 제한을 피하는 보장은 아니에요.
          </p>
        </Card>
        <RowGroup>
          <Row
            label="준비 안 된 글은 준비된 글로 대체"
            description="끄면 그 예약은 건너뛰고, 글이 준비되면 다음 빈 예약에 들어가요"
            trailing={<Switch label="미준비 시 대체" checked={rules.substitute_unready} onChange={(v) => r('substitute_unready', v)} />}
          />
        </RowGroup>
      </Section>

      <Exceptions data={data} />

      <Section title="적용">
        <ChoiceList
          name="적용 범위"
          value={scope}
          onChange={(v) => {
            setScope(v);
            setReport(null);
          }}
          options={[
            { value: 'tomorrow', label: '내일부터 다시 배치', description: '오늘 남은 예약은 그대로 두고 내일부터 새 설정으로 다시 만들어요' },
            { value: 'now', label: '지금 이후 모두 다시 배치', description: '게시 중·완료 예약과 시간 고정 예약은 그대로 둬요' },
            { value: 'later', label: '이미 만든 예약은 그대로', description: '앞으로 새로 만드는 예약부터 적용해요' },
          ]}
        />
        <Button size="lg" block busy={busy} onClick={preview}>
          {dirty ? '바뀐 설정 미리보기' : '미리보기'}
        </Button>
      </Section>

      <Sheet
        open={!!report}
        onClose={() => setReport(null)}
        title="이대로 적용할까요?"
        description="글·이미지는 다시 만들지 않고 시간만 바꿔요. 줄어서 남는 글은 보관함에 그대로 있어요."
        size="lg"
        footer={
          <>
            <Button variant="secondary" onClick={() => setReport(null)}>
              취소
            </Button>
            <Button busy={save.isPending} onClick={apply}>
              적용
            </Button>
          </>
        }
      >
        {report && <ReportView report={report} scope={scope} />}
      </Sheet>
    </div>
  );
}

function ReportView({ report, scope }: { report: SchedulePlanReport; scope: RulesBody['scope'] }) {
  return (
    <div className={styles.report}>
      {scope !== 'later' && (
        <p className={styles.muted}>
          다시 만드는 예약 {report.canceled}개 → 새 예약 {report.slots.length}개 · 글 다시 넣기 {report.reassigned}개
          {report.released > 0 && ` · 자리가 모자라 보관함으로 ${report.released}개`}
          {report.pinned > 0 && ` · 시간 고정 ${report.pinned}개는 그대로`}
        </p>
      )}
      <table className={styles.table}>
        <thead>
          <tr>
            <th scope="col">날짜</th>
            <th scope="col">묶음</th>
            <th scope="col">하루 최대</th>
          </tr>
        </thead>
        <tbody>
          {report.days.map((d) => (
            <tr key={d.date}>
              <td>{dayLabel(d.date)}</td>
              <td>
                {d.excluded ? '쉬는 날' : scope === 'later' ? '-' : `${d.count}/${d.target}`}
                {d.shortfall > 0 && <span className={styles.short}> {d.shortfall}개 부족</span>}
              </td>
              <td>
                {d.capacity_min === d.capacity_max ? d.capacity_max : `${d.capacity_min}~${d.capacity_max}`}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      {report.days.some((d) => d.shortfall > 0) && (
        <p className={styles.warnBox}>하루 목표를 다 넣을 수 없는 날이 있어요. 구간을 늘리거나, 기본 간격·추가 지연을 줄이거나, 하루 묶음 수를 줄이세요.</p>
      )}
      {report.slots.length > 0 && (
        <>
          <h3 className={styles.sub}>새 예약 시각</h3>
          {Object.entries(
            report.slots.reduce<Record<string, typeof report.slots>>((acc, s) => {
              (acc[s.local_date] ??= []).push(s);
              return acc;
            }, {}),
          ).map(([date, slots]) => (
            <div key={date} className={styles.slotDay}>
              <strong>{dayLabel(date)}</strong>
              <p className={styles.slotTimes}>{slots.map((s) => formatTime(s.at)).join(' · ')}</p>
            </div>
          ))}
        </>
      )}
    </div>
  );
}

function Exceptions({ data }: { data: ScheduleSettings }) {
  const act = useScheduleAction();
  const toast = useToast();
  const [date, setDate] = useState(addDays(localDate(), 1));
  const [note, setNote] = useState('');
  async function add() {
    try {
      const r = await act.mutateAsync({ path: '/api/schedule/exceptions', body: { date, note } });
      toast.show(`쉬는 날을 추가했어요 (예약 ${(r as { closed?: number }).closed ?? 0}개 건너뜀)`, 'success');
      setNote('');
    } catch (e) {
      toast.show(errorMessage(e), 'error');
    }
  }
  return (
    <Section title="쉬는 날" description="그날은 자동 예약을 만들지 않아요. 시간 고정 예약은 남아요">
      <Card>
        <CardTitle>날짜 추가</CardTitle>
        <div className={styles.grid}>
          <TextField label="날짜" type="date" value={date} onChange={(e) => setDate(e.target.value)} />
          <TextField label="메모 (선택)" value={note} onChange={(e) => setNote(e.target.value)} placeholder="예: 휴가" />
        </div>
        <Button variant="secondary" busy={act.isPending} onClick={add}>
          쉬는 날 추가
        </Button>
      </Card>
      {data.exceptions.length > 0 && (
        <RowGroup>
          {data.exceptions.map((e) => (
            <Row
              key={e.date}
              label={dayLabel(e.date)}
              description={e.note || undefined}
              trailing={
                <IconButton
                  label={`${dayLabel(e.date)} 쉬는 날 해제`}
                  onClick={async () => {
                    try {
                      await act.mutateAsync({ path: `/api/schedule/exceptions/${e.date}`, method: 'DELETE' });
                      toast.show('쉬는 날을 해제하고 그날 예약을 다시 만들었어요', 'success');
                    } catch (err) {
                      toast.show(errorMessage(err), 'error');
                    }
                  }}
                >
                  <Trash2 size={18} />
                </IconButton>
              }
            />
          ))}
        </RowGroup>
      )}
    </Section>
  );
}
