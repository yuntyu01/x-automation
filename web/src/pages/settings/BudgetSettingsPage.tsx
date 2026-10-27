import { useState } from 'react';
import { ApiError, errorMessage } from '../../api/client';
import { useUsage } from '../../api/generation';
import { useSaveSettings, useSettings } from '../../api/queries';
import type { Policy, Settings, UsageSummary } from '../../api/types';
import { PageHeader } from '../../components/layout/AppShell';
import { Button } from '../../components/ui/Button';
import { Card, Section } from '../../components/ui/Card';
import { ChoiceList } from '../../components/ui/ChoiceList';
import { ErrorState, LoadingList } from '../../components/ui/States';
import { TextField } from '../../components/ui/TextField';
import { useToast } from '../../components/ui/Toast';
import { formatMoney } from '../../lib/format';
import { BackButton } from './ConnectionsPage';
import shared from './GenerationSettingsPage.module.css';
import styles from './BudgetSettingsPage.module.css';

type Pricing = Policy['pricing'];
type Budget = Policy['budget'];

// docs/INTEGRATIONS.md에 확인 근거를 남긴 X 요율만 미리 채울 수 있게 한다 (확인 안 된 단가는 넣지 않는다)
const X_VERIFIED: Partial<Pricing> = {
  x_post_micros: 15_000,
  x_post_with_url_micros: 200_000,
  verified_at: '2026-10-27',
  source: 'docs.x.com X API 요금표 (2026-04-20 적용분, 공식 문서 저장소에서 확인)',
};

const CATEGORIES: { key: string; label: string; unit: string }[] = [
  { key: 'ai_text', label: '글 생성', unit: '건' },
  { key: 'ai_image', label: '이미지 생성', unit: '건' },
  { key: 'x_post', label: 'X 게시', unit: '개 게시물' },
  { key: 'x_media', label: 'X 미디어 업로드', unit: '건' },
  { key: 'storage', label: '저장', unit: '건' },
];

// 금액 입력은 통화 단위 소수(예: 0.015)로 받고 서버에는 백만분의 1 정수로 보낸다
const toText = (micros: number) => (micros ? String(micros / 1e6) : '');
function toMicros(v: string): number | null {
  if (v.trim() === '') return 0;
  const n = Number(v);
  if (!Number.isFinite(n) || n < 0) return null;
  return Math.round(n * 1e6);
}

export function BudgetSettingsPage() {
  const settings = useSettings();
  const usage = useUsage();
  return (
    <>
      <PageHeader title="사용량·예산" subtitle="기록된 사용 횟수로 계산한 추정치예요" back={<BackButton to="/settings" />} />
      {(settings.isLoading || usage.isLoading) && <LoadingList rows={4} />}
      {settings.isError && <ErrorState error={settings.error} onRetry={() => settings.refetch()} />}
      <div className={shared.stack}>
        {usage.data && <UsageCard u={usage.data} />}
        {settings.data && <BudgetForm key={settings.data.version} settings={settings.data} />}
      </div>
    </>
  );
}

function UsageCard({ u }: { u: UsageSummary }) {
  const used = u.spent_micros + u.held_micros;
  const pct = u.limit_micros > 0 ? Math.min(100, Math.round((used / u.limit_micros) * 100)) : 0;
  return (
    <Section title={`이번 달 (${u.period}) 추정 사용량`}>
      <Card>
        {u.pricing_known ? (
          <p className={styles.total}>
            <strong>{formatMoney(u.spent_micros, u.currency)}</strong>
            {u.held_micros > 0 && <span className={styles.sub}> + 진행 중 예약 {formatMoney(u.held_micros, u.currency)}</span>}
          </p>
        ) : (
          <p className={styles.warn}>요율표에 확인일이 없어 금액 대신 사용 횟수만 보여요.</p>
        )}
        {u.limit_micros > 0 && (
          <div className={styles.meterBox}>
            <div className={styles.meter} role="meter" aria-label="월 한도 사용률" aria-valuemin={0} aria-valuemax={100} aria-valuenow={pct}>
              <span className={[styles.fill, u.exceeded ? styles.over : pct >= 80 ? styles.near : ''].join(' ')} style={{ width: `${pct}%` }} />
            </div>
            <p className={styles.sub}>
              한도 {formatMoney(u.limit_micros, u.currency)}의 {pct}%{u.exceeded && ` · 한도 도달: ${u.on_limit === 'stop_all' ? '생성과 새 게시를 멈췄어요' : '생성을 멈췄어요'}`}
            </p>
          </div>
        )}
        <dl className={styles.cats}>
          {CATEGORIES.map((c) => (
            <div key={c.key}>
              <dt>{c.label}</dt>
              <dd>
                {u.counts[c.key] ?? 0}
                {c.unit}
                {u.pricing_known && <span className={styles.sub}> · {formatMoney(u.by_category[c.key] ?? 0, u.currency)}</span>}
              </dd>
            </div>
          ))}
        </dl>
        {u.uncertain_count > 0 && (
          <p className={styles.sub}>결과를 모르는 요청 {u.uncertain_count}건은 과금됐을 수 있어 포함했어요. 게시 기록에서 확인하면 정리돼요.</p>
        )}
        <p className={styles.caption}>
          사용 횟수 × 요율표 단가로 계산했어요. 환율·세금·할인·무료 크레딧은 빠져 있어요. 실제 청구액은 각 서비스 콘솔에서 볼 수 있어요.
        </p>
      </Card>
    </Section>
  );
}

const PRICE_FIELDS: { key: keyof Pricing; label: string; hint: string }[] = [
  { key: 'x_post_micros', label: 'X 게시물 1개', hint: '본문·답글 각각' },
  { key: 'x_post_with_url_micros', label: 'X 링크 포함 게시물 1개', hint: '링크가 있으면 이 단가로 계산' },
  { key: 'x_media_micros', label: 'X 미디어 업로드 1건', hint: '모르면 비워 둬도 돼요' },
  { key: 'text_input_per_mtok_micros', label: '글 생성 입력 100만 토큰', hint: '' },
  { key: 'text_output_per_mtok_micros', label: '글 생성 출력 100만 토큰', hint: '' },
  { key: 'image_per_image_micros', label: '이미지 1장', hint: '' },
];

function BudgetForm({ settings }: { settings: Settings }) {
  const save = useSaveSettings();
  const toast = useToast();
  const [budget, setBudget] = useState<Budget>(settings.policy.budget);
  const [limit, setLimit] = useState(toText(settings.policy.budget.monthly_limit_micros));
  const [pricing, setPricing] = useState<Pricing>(settings.policy.pricing);
  const [prices, setPrices] = useState<Record<string, string>>(() => Object.fromEntries(PRICE_FIELDS.map((f) => [f.key, toText(settings.policy.pricing[f.key] as number)])));
  const [fields, setFields] = useState<Record<string, string>>({});
  const cur = pricing.currency || 'USD';

  const limitMicros = toMicros(limit);
  const priceMicros = Object.fromEntries(PRICE_FIELDS.map((f) => [f.key, toMicros(prices[f.key] ?? '')]));
  const nextPricing: Pricing = { ...pricing, ...(Object.fromEntries(Object.entries(priceMicros).map(([k, v]) => [k, v ?? 0])) as Partial<Pricing>) };
  const nextBudget: Budget = { ...budget, monthly_limit_micros: limitMicros ?? 0 };
  const dirty = JSON.stringify(nextBudget) !== JSON.stringify(settings.policy.budget) || JSON.stringify(nextPricing) !== JSON.stringify(settings.policy.pricing);
  const invalid = limitMicros === null || Object.values(priceMicros).some((v) => v === null);

  function applyPreset() {
    setPricing((p) => ({ ...p, ...X_VERIFIED, currency: 'USD' }));
    setPrices((p) => ({ ...p, x_post_micros: toText(X_VERIFIED.x_post_micros!), x_post_with_url_micros: toText(X_VERIFIED.x_post_with_url_micros!) }));
  }

  async function submit() {
    if (invalid) return;
    setFields({});
    try {
      await save.mutateAsync({ ...settings, policy: { ...settings.policy, budget: nextBudget, pricing: nextPricing } });
      toast.show('예산·요율표를 저장했어요', 'success');
    } catch (e) {
      if (e instanceof ApiError && Object.keys(e.fields).length) setFields(e.fields);
      toast.show(errorMessage(e), 'error');
    }
  }

  return (
    <>
      <Section title="월 한도" description="실행 전에 추정 비용을 먼저 잡아 두어 한도를 넘지 않아요. 비우면 기록만 해요">
        <Card>
          <TextField
            label={`월 한도 (${cur})`}
            inputMode="decimal"
            value={limit}
            onChange={(e) => setLimit(e.target.value)}
            placeholder="예: 20"
            error={limitMicros === null ? '0 이상의 숫자를 넣어 주세요' : fields['budget.monthly_limit_micros']}
            hint="설정한 시간대 기준으로 매달 1일에 새로 시작해요"
          />
        </Card>
        <ChoiceList
          name="한도에 도달하면"
          value={budget.on_limit}
          onChange={(v) => setBudget((b) => ({ ...b, on_limit: v }))}
          options={[
            { value: 'stop_generation', label: '생성만 멈추기', description: '이미 만든 글은 계속 게시해요 (게시 비용은 계속 기록)' },
            { value: 'stop_all', label: '생성과 새 게시 모두 멈추기', description: '시작한 묶음은 답글까지 끝내고, 새 묶음은 시작하지 않아요' },
          ]}
        />
      </Section>

      <Section title="요율표" description="추정 비용에 쓰는 단가예요. 요금은 바뀔 수 있어 확인일과 출처를 같이 적어 둬요">
        <Card>
          {!pricing.verified_at && <p className={styles.warn}>확인일이 없으면 금액을 계산하지 않고 "단가 미확인"으로 보여요.</p>}
          <div className={styles.preset}>
            <Button size="sm" variant="soft" onClick={applyPreset}>
              확인된 X 단가 넣기
            </Button>
            <span className={styles.sub}>게시물 $0.015 · 링크 포함 $0.200 (2026-10-27 확인)</span>
          </div>
          <div className={shared.grid}>
            {PRICE_FIELDS.map((f) => (
              <TextField
                key={f.key}
                label={`${f.label} (${cur})`}
                inputMode="decimal"
                value={prices[f.key] ?? ''}
                onChange={(e) => setPrices((p) => ({ ...p, [f.key]: e.target.value }))}
                hint={f.hint || undefined}
                error={priceMicros[f.key] === null ? '0 이상의 숫자를 넣어 주세요' : fields[`pricing.${f.key}`]}
              />
            ))}
            <TextField label="확인일" type="date" value={pricing.verified_at} onChange={(e) => setPricing((p) => ({ ...p, verified_at: e.target.value }))} />
            <TextField label="요율 버전" value={pricing.version} onChange={(e) => setPricing((p) => ({ ...p, version: e.target.value }))} placeholder="예: 2026-10" />
          </div>
          <TextField label="출처" value={pricing.source} onChange={(e) => setPricing((p) => ({ ...p, source: e.target.value }))} placeholder="요금 페이지 주소나 설명" />
          <p className={styles.caption}>
            글 생성 단가는 모델마다 달라요. 쓰는 모델의 공식 요금표를 확인해 넣어 주세요. 확인하지 못한 값은 비워 두면 그 항목은 0으로 계산돼요.
          </p>
        </Card>
      </Section>

      {dirty && (
        <div className={shared.saveBar} role="region" aria-label="저장">
          <span>저장하지 않은 변경이 있어요</span>
          <Button
            variant="secondary"
            onClick={() => {
              setBudget(settings.policy.budget);
              setLimit(toText(settings.policy.budget.monthly_limit_micros));
              setPricing(settings.policy.pricing);
              setPrices(Object.fromEntries(PRICE_FIELDS.map((f) => [f.key, toText(settings.policy.pricing[f.key] as number)])));
            }}
          >
            되돌리기
          </Button>
          <Button busy={save.isPending} disabled={invalid} onClick={submit}>
            저장
          </Button>
        </div>
      )}
    </>
  );
}
