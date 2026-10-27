import { CalendarClock, Clock, Package, PlayCircle } from 'lucide-react';
import { useState } from 'react';
import { Link } from 'react-router';
import { ApiError, errorMessage } from '../../api/client';
import { usePrompts } from '../../api/contents';
import { useFillNow, usePlan } from '../../api/generation';
import { useSaveSettings, useSettings } from '../../api/queries';
import { TRIGGER_LABEL, type GenerationPlan, type GenerationPolicy, type Policy, type Settings } from '../../api/types';
import { GenerationQueue } from '../../components/generation/GenerationQueue';
import { PageHeader } from '../../components/layout/AppShell';
import { Badge } from '../../components/ui/Badge';
import { Button } from '../../components/ui/Button';
import { Card, CardTitle, Section } from '../../components/ui/Card';
import { ChoiceList } from '../../components/ui/ChoiceList';
import { Select } from '../../components/ui/Field';
import { Row, RowGroup } from '../../components/ui/Row';
import { Switch } from '../../components/ui/Switch';
import { ErrorState, LoadingList } from '../../components/ui/States';
import { TextField } from '../../components/ui/TextField';
import { useToast } from '../../components/ui/Toast';
import { formatDateTime, relativeTime } from '../../lib/format';
import { BackButton } from './ConnectionsPage';
import styles from './GenerationSettingsPage.module.css';

export function GenerationSettingsPage() {
  const settings = useSettings();
  return (
    <>
      <PageHeader title="생성 방식" subtitle="언제, 무엇을, 얼마나 만들지 정해요" back={<BackButton to="/settings" />} />
      {settings.isLoading && <LoadingList rows={4} />}
      {settings.isError && <ErrorState error={settings.error} onRetry={() => settings.refetch()} />}
      {/* 저장 후 서버 값으로 폼을 다시 시작한다 (version이 key) */}
      {settings.data && <GenerationForm key={settings.data.version} settings={settings.data} />}
    </>
  );
}

type Gen = GenerationPolicy;
type Img = Policy['image'];

function GenerationForm({ settings }: { settings: Settings }) {
  const save = useSaveSettings();
  const toast = useToast();
  const prompts = usePrompts(false);
  const [gen, setGen] = useState<Gen>(settings.policy.generation);
  const [img, setImg] = useState<Img>(settings.policy.image);
  const [fields, setFields] = useState<Record<string, string>>({});
  const dirty = JSON.stringify(gen) !== JSON.stringify(settings.policy.generation) || JSON.stringify(img) !== JSON.stringify(settings.policy.image);
  const g = <K extends keyof Gen>(k: K, v: Gen[K]) => setGen((p) => ({ ...p, [k]: v }));
  const i = <K extends keyof Img>(k: K, v: Img[K]) => setImg((p) => ({ ...p, [k]: v }));
  const num = (v: string) => (v === '' ? 0 : Number(v));

  async function submit() {
    setFields({});
    try {
      await save.mutateAsync({ ...settings, policy: { ...settings.policy, generation: gen, image: img } });
      toast.show('생성 방식을 저장했어요', 'success');
    } catch (e) {
      if (e instanceof ApiError && Object.keys(e.fields).length) {
        setFields(e.fields);
        toast.show('입력값을 확인해 주세요', 'error');
      } else if (e instanceof ApiError && e.status === 409) toast.show('다른 곳에서 먼저 바뀌었어요. 새로고침 후 다시 저장해 주세요', 'error');
      else toast.show(errorMessage(e), 'error');
    }
  }

  const promptOptions = [{ value: '', label: '고르지 않음 (자동 생성 안 함)' }, ...(prompts.data?.items.filter((p) => p.active).map((p) => ({ value: p.id, label: p.name })) ?? [])];
  const publishOnly = settings.operation_mode === 'publish_only';

  return (
    <div className={styles.stack}>
      {publishOnly && <Card className={styles.note}>지금은 &lsquo;게시만 자동&rsquo; 모드라 글을 생성하지 않아요. 이미지 생성만 쓸 수 있어요. 운영 모드는 설정에서 바꿔요.</Card>}
      <Section title="무엇을 만들까요" description="글과 이미지는 따로 켜고 끌 수 있어요">
        <RowGroup>
          <Row label="글 생성" description="프롬프트로 본문·답글이 될 글을 만들어요" trailing={<Switch label="글 생성" checked={gen.text_enabled} onChange={(v) => g('text_enabled', v)} />} />
          <Row label="이미지 생성" description="생성 글에 붙이거나, 직접 쓴 글에 이미지만 만들어요" trailing={<Switch label="이미지 생성" checked={gen.image_enabled} onChange={(v) => g('image_enabled', v)} />} />
        </RowGroup>
      </Section>

      <Section title="언제 만들까요">
        <ChoiceList
          name="생성 시점"
          value={gen.timing}
          onChange={(v) => g('timing', v)}
          options={[
            { value: 'prepare', label: '미리 자동 생성', description: '정한 규칙에 따라 예약보다 먼저 준비해요' },
            { value: 'on_publish', label: '게시할 때', description: '예약 시각에 생성을 시작해요. 생성 시간만큼 실제 게시가 늦어질 수 있어요' },
            { value: 'manual', label: '버튼을 눌렀을 때', description: '자동으로 만들지 않아요. 콘텐츠 화면의 새 글 → AI로 생성에서 만들어요' },
          ]}
        />
      </Section>

      {gen.timing === 'prepare' && (
        <Section title="미리 생성 규칙" description="하나를 주 전략으로 골라요. 이미 준비된 글을 먼저 쓰고 부족한 만큼만 만들어요">
          <ChoiceList
            name="미리 생성 규칙"
            value={gen.prepare_strategy}
            onChange={(v) => g('prepare_strategy', v)}
            options={[
              { value: 'daily_at', label: '매일 지정 시각', description: '정한 시각에 다음 날(확보 기간)의 빈 예약만큼 준비해요' },
              { value: 'before_slot', label: '예약 전 N분', description: '각 예약 N분 전에 그 예약에 쓸 글을 만들어요' },
              { value: 'stock', label: '재고가 부족할 때', description: '준비 완료 글이 기준보다 적으면 목표 수량까지 채워요' },
            ]}
          />
          <Card>
            <div className={styles.grid}>
              {gen.prepare_strategy === 'daily_at' && (
                <>
                  <TextField label="지정 시각" type="time" value={gen.daily_at} onChange={(e) => g('daily_at', e.target.value)} error={fields['generation.daily_at']} hint={`시간대: ${settings.timezone}`} />
                  <TextField
                    label="확보 기간 (일)"
                    type="number"
                    inputMode="numeric"
                    min={1}
                    max={7}
                    value={String(gen.horizon_days)}
                    onChange={(e) => g('horizon_days', num(e.target.value))}
                    error={fields['generation.horizon_days']}
                    hint="1이면 다음 날 예약까지 준비해요"
                  />
                </>
              )}
              {gen.prepare_strategy === 'before_slot' && (
                <TextField
                  label="예약 몇 분 전에"
                  type="number"
                  inputMode="numeric"
                  min={5}
                  value={String(gen.before_minutes)}
                  onChange={(e) => g('before_minutes', num(e.target.value))}
                  error={fields['generation.before_minutes']}
                  hint="생성이 끝나기 충분한 시간을 두세요 (이미지는 더 오래 걸려요)"
                />
              )}
              {gen.prepare_strategy === 'stock' && (
                <>
                  <TextField label="기준 (이보다 적으면 보충)" type="number" inputMode="numeric" min={0} value={String(gen.stock_min)} onChange={(e) => g('stock_min', num(e.target.value))} />
                  <TextField
                    label="목표 재고"
                    type="number"
                    inputMode="numeric"
                    min={1}
                    value={String(gen.stock_target)}
                    onChange={(e) => g('stock_target', num(e.target.value))}
                    error={fields['generation.stock_target']}
                  />
                </>
              )}
            </div>
          </Card>
        </Section>
      )}
      {gen.timing !== 'manual' && (
        <RowGroup>
          <Row
            label="시작할 때 바로 채우기"
            description="자동 운영을 처음 켤 때 정기 생성까지 기다리지 않고 부족분을 채워요"
            trailing={<Switch label="시작할 때 바로 채우기" checked={gen.fill_on_start} onChange={(v) => g('fill_on_start', v)} />}
          />
        </RowGroup>
      )}

      <Section title="자동 생성에 쓸 프롬프트">
        <Card>
          <div className={styles.grid}>
            <Select label="기본 프롬프트" value={gen.default_prompt_id} onChange={(e) => g('default_prompt_id', e.target.value)} options={promptOptions} hint="변수는 프롬프트의 기본값이 들어가요" />
            <p className={styles.muted}>
              프롬프트는 <Link to="/prompts">프롬프트 화면</Link>에서 만들고 고쳐요.
            </p>
          </div>
        </Card>
      </Section>

      <Section title="모델·품질" description="비워 두면 서버 환경변수의 기본 모델을 써요. 모델 이름과 지원 옵션은 공급자 문서로 확인하세요">
        <Card>
          <div className={styles.grid}>
            <TextField label="글 모델" value={gen.text_model} onChange={(e) => g('text_model', e.target.value.trim())} placeholder="서버 기본값" />
            <TextField label="이미지 모델" value={img.model} onChange={(e) => i('model', e.target.value.trim())} placeholder="서버 기본값" />
            {/* 품질·크기 값은 공급자·모델마다 다르다. 흔한 값을 제안만 하고 서버가 그대로 전달한다 */}
            <TextField label="이미지 품질" value={img.quality} onChange={(e) => i('quality', e.target.value.trim())} list="image-quality-options" hint="모델이 지원하는 값을 쓰세요 (예: low·medium·high)" />
            <datalist id="image-quality-options">
              {['low', 'medium', 'high'].map((v) => (
                <option key={v} value={v} />
              ))}
            </datalist>
            <TextField label="이미지 크기" value={img.size} onChange={(e) => i('size', e.target.value.trim())} list="image-size-options" hint="가로x세로 픽셀 (예: 1024x1024)" />
            <datalist id="image-size-options">
              {['1024x1024', '1536x1024', '1024x1536'].map((v) => (
                <option key={v} value={v} />
              ))}
            </datalist>
            <Select label="묶음당 이미지" value={String(img.count)} onChange={(e) => i('count', Number(e.target.value))} options={[1, 2, 3, 4].map((n) => ({ value: String(n), label: `${n}장` }))} error={fields['image.count']} />
            <Select
              label="붙일 위치"
              value={String(img.placement)}
              onChange={(e) => i('placement', Number(e.target.value))}
              options={[0, 1, 2].map((n) => ({ value: String(n), label: n === 0 ? '본문' : `답글 ${n}` }))}
              hint="부분이 그보다 적으면 마지막 부분에 붙여요"
            />
            <Select
              label="자동 생성 글의 이미지"
              value={img.default_mode}
              onChange={(e) => i('default_mode', e.target.value as Img['default_mode'])}
              options={[
                { value: 'none', label: '이미지 없이' },
                { value: 'ai', label: 'AI 이미지 함께 생성' },
                { value: 'upload', label: '직접 업로드 (생성 후 붙이기)' },
              ]}
            />
          </div>
        </Card>
        <RowGroup>
          <Row
            label="이미지가 실패하면 텍스트만으로 게시"
            description="끄면 이미지가 없는 글은 게시하지 않고 '작성 중'으로 남겨요"
            trailing={<Switch label="이미지 실패 시 텍스트만 게시" checked={gen.text_only_fallback} onChange={(v) => g('text_only_fallback', v)} />}
          />
          <Row
            label="AI 이미지 게시에 'AI로 만듦' 표시"
            description="AI로 만든 이미지가 붙은 게시물에 X의 AI 생성 표시(made_with_ai)를 함께 보내요. 직접 올린 이미지에는 붙이지 않아요"
            trailing={<Switch label="AI 생성 표시" checked={img.made_with_ai} onChange={(v) => i('made_with_ai', v)} />}
          />
        </RowGroup>
      </Section>

      <Section title="한도" description="비용을 아끼기 위한 안전장치예요">
        <Card>
          <div className={styles.grid}>
            <TextField label="하루 생성 상한 (묶음)" type="number" inputMode="numeric" value={String(gen.daily_limit)} onChange={(e) => g('daily_limit', num(e.target.value))} error={fields['generation.daily_limit']} />
            <TextField
              label="한 글의 재생성 상한"
              type="number"
              inputMode="numeric"
              value={String(gen.max_regenerations)}
              onChange={(e) => g('max_regenerations', num(e.target.value))}
              error={fields['generation.max_regenerations']}
            />
            <TextField label="동시 생성 수" type="number" inputMode="numeric" value={String(gen.max_concurrent)} onChange={(e) => g('max_concurrent', num(e.target.value))} error={fields['generation.max_concurrent']} hint="서버 재시작 후 적용돼요" />
            <TextField label="실패 시 재시도 상한" type="number" inputMode="numeric" value={String(gen.max_attempts)} onChange={(e) => g('max_attempts', num(e.target.value))} error={fields['generation.max_attempts']} />
          </div>
        </Card>
      </Section>

      {dirty && (
        <div className={styles.saveBar} role="region" aria-label="저장">
          <span>저장하지 않은 변경이 있어요</span>
          <Button
            variant="secondary"
            onClick={() => {
              setGen(settings.policy.generation);
              setImg(settings.policy.image);
              setFields({});
            }}
          >
            되돌리기
          </Button>
          <Button busy={save.isPending} onClick={submit}>
            저장
          </Button>
        </div>
      )}

      <PlanCard dirty={dirty} />
      <GenerationQueue />
    </div>
  );
}

function PlanCard({ dirty }: { dirty: boolean }) {
  const plan = usePlan();
  const fill = useFillNow();
  const toast = useToast();
  if (plan.isLoading) return <LoadingList rows={2} />;
  if (plan.isError) return <ErrorState error={plan.error} onRetry={() => plan.refetch()} />;
  const p = plan.data;
  if (!p) return null;
  return (
    <Card aria-labelledby="plan-title">
      <CardTitle id="plan-title" action={<Badge tone={p.active ? 'success' : 'neutral'}>{p.active ? '자동 생성 동작 중' : '자동 생성 멈춤'}</Badge>}>
        자동 생성 계획
      </CardTitle>
      {dirty && <p className={styles.muted}>저장한 설정 기준이에요. 바꾼 내용은 저장 후 반영돼요.</p>}
      {p.blockers.length > 0 && (
        <ul className={styles.blockers}>
          {p.blockers.map((b) => (
            <li key={b}>{b}</li>
          ))}
        </ul>
      )}
      <div className={styles.planHead}>
        <PlanIcon p={p} />
        <div>
          <strong>{p.next_run_note}</strong>
          {p.active && p.next_run_at && <span className={styles.muted}>다음 생성 예정: {formatDateTime(p.next_run_at)}</span>}
        </div>
      </div>
      <dl className={styles.counts}>
        {p.window_start && (
          <div>
            <dt>빈 예약</dt>
            <dd>{p.counts.needed}</dd>
          </div>
        )}
        <div>
          <dt>준비 완료</dt>
          <dd>{p.counts.ready}</dd>
        </div>
        <div>
          <dt>생성 중</dt>
          <dd>{p.counts.in_flight + p.counts.bound}</dd>
        </div>
        <div>
          <dt>검토 대기</dt>
          <dd>{p.counts.review_pending}</dd>
        </div>
        <div>
          <dt>새로 만들 수</dt>
          <dd>{p.counts.shortfall}</dd>
        </div>
        <div>
          <dt>오늘 남은 한도</dt>
          <dd>{p.counts.remaining_today}</dd>
        </div>
      </dl>
      {p.window_start && p.window_end && (
        <p className={styles.muted}>
          계산 기간: {formatDateTime(p.window_start)} ~ {formatDateTime(p.window_end)} · 검토 대기 AI 글은 이미 확보된 것으로 셈해요
        </p>
      )}
      {p.counts.capped && <p className={styles.warn}>오늘 생성 상한 때문에 부족분을 다 채우지 못해요.</p>}
      {p.upcoming.length > 0 && (p.timing === 'on_publish' || p.strategy === 'before_slot' || p.strategy === 'daily_at') && p.timing !== 'manual' && (
        <>
          <h3 className={styles.sub}>다가오는 빈 예약</h3>
          <ul className={styles.upcoming}>
            {p.upcoming.map((u) => (
              <li key={u.reservation_id}>
                <span>게시 {formatDateTime(u.slot_at)}</span>
                <span className={styles.muted}>{u.generating ? '생성 중' : u.due_at ? `생성 예정 ${formatDateTime(u.due_at)}` : ''}</span>
              </li>
            ))}
          </ul>
        </>
      )}
      {p.recent.length > 0 && (
        <>
          <h3 className={styles.sub}>최근 자동 판단</h3>
          <ul className={styles.upcoming}>
            {p.recent.map((m) => (
              <li key={m.key}>
                <span>
                  {TRIGGER_LABEL[m.trigger]} · {m.note ?? ''}
                </span>
                <span className={styles.muted}>{relativeTime(m.created_at)}</span>
              </li>
            ))}
          </ul>
        </>
      )}
      <Button
        variant="secondary"
        icon={<PlayCircle size={18} />}
        busy={fill.isPending}
        onClick={async () => {
          try {
            const r = await fill.mutateAsync();
            toast.show(r.request ? `부족분 ${r.request.quantity}개 생성을 시작했어요` : '지금은 새로 만들 부족분이 없어요', r.request ? 'success' : 'info');
            void plan.refetch();
          } catch (e) {
            toast.show(errorMessage(e), 'error');
          }
        }}
      >
        지금 부족분 채우기
      </Button>
    </Card>
  );
}

function PlanIcon({ p }: { p: GenerationPlan }) {
  if (p.timing === 'on_publish' || p.strategy === 'before_slot') return <Clock size={22} aria-hidden />;
  if (p.strategy === 'stock') return <Package size={22} aria-hidden />;
  return <CalendarClock size={22} aria-hidden />;
}
