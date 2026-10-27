import { Check } from 'lucide-react';
import { Link, useNavigate, useSearchParams } from 'react-router';
import { useQueryClient } from '@tanstack/react-query';
import { errorMessage, post } from '../../api/client';
import { keys, useIntegrations, useSaveSettings, useSettings } from '../../api/queries';
import { MODE_LABEL, type OperationMode, type Settings } from '../../api/types';
import { PageHeader } from '../../components/layout/AppShell';
import { Badge } from '../../components/ui/Badge';
import { Button } from '../../components/ui/Button';
import { ChoiceList } from '../../components/ui/ChoiceList';
import { ErrorState, LoadingList } from '../../components/ui/States';
import { useToast } from '../../components/ui/Toast';
import { BackButton, xState } from '../settings/ConnectionsPage';
import { MODE_OPTIONS } from '../settings/labels';
import { SetupPromptStep, SetupScheduleStep } from './steps';
import { StepCard } from './StepCard';
import styles from './SetupPage.module.css';

const STEPS = ['연결 상태', '프롬프트', '운영 모드', '시간표', '확인'] as const;

export function SetupPage() {
  const [params, setParams] = useSearchParams();
  const step = Math.min(Math.max(Number(params.get('step') ?? 0) || 0, 0), STEPS.length - 1);
  const settings = useSettings();
  const integrations = useIntegrations();
  const save = useSaveSettings();
  const toast = useToast();
  const nav = useNavigate();
  const qc = useQueryClient();

  const go = (n: number) => setParams({ step: String(n) }, { replace: false });

  async function setMode(mode: OperationMode) {
    if (!settings.data) return;
    try {
      await save.mutateAsync({ ...settings.data, operation_mode: mode });
    } catch (e) {
      toast.show(errorMessage(e), 'error');
    }
  }

  async function finish() {
    try {
      const s = await post<Settings>('/api/setup/complete');
      qc.setQueryData(keys.settings, s);
      toast.show('첫 설정을 마쳤어요. 자동 운영은 홈에서 직접 시작해요', 'success');
      nav('/');
    } catch (e) {
      toast.show(errorMessage(e), 'error');
    }
  }

  return (
    <>
      <PageHeader title="첫 설정" subtitle={`${step + 1} / ${STEPS.length} · ${STEPS[step]}`} back={<BackButton to="/" />} />
      <ol className={styles.progress} aria-label="설정 단계">
        {STEPS.map((s, i) => (
          <li key={s} className={[styles.dot, i < step ? styles.done : '', i === step ? styles.current : ''].join(' ')} aria-current={i === step ? 'step' : undefined}>
            <span className="sr-only">
              {i + 1}단계 {s} {i < step ? '완료' : ''}
            </span>
          </li>
        ))}
      </ol>
      {(settings.isLoading || integrations.isLoading) && <LoadingList rows={2} />}
      {settings.isError && <ErrorState error={settings.error} onRetry={() => settings.refetch()} />}
      {settings.data && integrations.data && (
        <div className={styles.body}>
          {step === 0 && (
            <StepCard title="연결 상태를 확인해요" desc="연결이 없어도 설정은 끝낼 수 있어요. 실제 게시 전에는 X 연결이 필요해요.">
              <ul className={styles.checks}>
                <CheckItem label="X 계정" state={xState(integrations.data)} />
                <CheckItem
                  label="AI 생성"
                  state={integrations.data.ai.configured ? { tone: 'success', label: integrations.data.mode === 'mock' ? '목업' : '설정됨' } : { tone: 'neutral', label: '선택 사항' }}
                />
                <CheckItem
                  label="이미지 저장소"
                  state={integrations.data.r2.configured ? { tone: 'success', label: integrations.data.mode === 'mock' ? '목업' : '설정됨' } : { tone: 'neutral', label: '선택 사항' }}
                />
              </ul>
              <Link to="/settings/connections" className={styles.link}>
                연결 자세히 보기
              </Link>
            </StepCard>
          )}
          {step === 1 && <SetupPromptStep />}
          {step === 2 && (
            <StepCard title="어떻게 운영할까요?" desc="나중에 설정에서 바꿀 수 있어요.">
              <ChoiceList name="운영 모드" value={settings.data.operation_mode} options={MODE_OPTIONS} onChange={setMode} />
            </StepCard>
          )}
          {step === 3 && <SetupScheduleStep />}
          {step === 4 && (
            <StepCard title="이대로 시작할까요?" desc="설정을 마쳐도 자동 운영은 꺼진 상태로 남아요. 홈에서 직접 켜야 게시가 시작돼요.">
              <dl className={styles.summary}>
                <dt>운영 모드</dt>
                <dd>{MODE_LABEL[settings.data.operation_mode]}</dd>
                <dt>X 계정</dt>
                <dd>{xState(integrations.data).label}</dd>
                <dt>실제 게시 허용 (서버)</dt>
                <dd>{integrations.data.publishing_enabled ? '켜짐' : '꺼짐'}</dd>
              </dl>
            </StepCard>
          )}
          <div className={styles.footer}>
            {step > 0 && (
              <Button variant="secondary" size="lg" onClick={() => go(step - 1)}>
                이전
              </Button>
            )}
            {step < STEPS.length - 1 ? (
              <Button size="lg" onClick={() => go(step + 1)}>
                다음
              </Button>
            ) : (
              <Button size="lg" onClick={finish} icon={<Check size={20} />}>
                설정 완료
              </Button>
            )}
          </div>
        </div>
      )}
    </>
  );
}

function CheckItem({ label, state }: { label: string; state: { tone: 'success' | 'warning' | 'danger' | 'neutral' | 'info' | 'progress' | 'scheduled'; label: string } }) {
  return (
    <li className={styles.check}>
      <span>{label}</span>
      <Badge tone={state.tone}>{state.label}</Badge>
    </li>
  );
}
