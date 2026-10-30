import { Archive, CalendarClock, FileText, ImageIcon, Info, Link2, LogOut, Moon, Scissors, Send, ShieldCheck, SlidersHorizontal, Sparkles, Tag, Wallet } from 'lucide-react';
import { useState } from 'react';
import { useAuth } from '../../auth/AuthProvider';
import { errorMessage } from '../../api/client';
import { useIntegrations, useRuntime, useSaveSettings, useSettings } from '../../api/queries';
import { MODE_LABEL, SPLIT_LABEL, type OperationMode } from '../../api/types';
import { PageHeader } from '../../components/layout/AppShell';
import { Button } from '../../components/ui/Button';
import { ChoiceList } from '../../components/ui/ChoiceList';
import { Section } from '../../components/ui/Card';
import { Row, RowGroup } from '../../components/ui/Row';
import { Sheet } from '../../components/ui/Sheet';
import { ErrorState, LoadingList } from '../../components/ui/States';
import { TextField } from '../../components/ui/TextField';
import { useToast } from '../../components/ui/Toast';
import { useTheme, type ThemePref } from '../../lib/theme';
import { MODE_OPTIONS, generationSummary } from './labels';
import styles from './SettingsPage.module.css';

const THEME_LABEL: Record<ThemePref, string> = { system: '시스템 설정', light: '라이트', dark: '다크' };

export function SettingsPage() {
  const settings = useSettings();
  const runtime = useRuntime();
  const integrations = useIntegrations();
  const save = useSaveSettings();
  const theme = useTheme();
  const auth = useAuth();
  const toast = useToast();
  const [sheet, setSheet] = useState<'mode' | 'theme' | 'retention' | null>(null);
  const [days, setDays] = useState('');

  async function changeMode(mode: OperationMode) {
    if (!settings.data) return;
    try {
      await save.mutateAsync({ ...settings.data, operation_mode: mode });
      toast.show(`운영 모드를 '${MODE_LABEL[mode]}'(으)로 바꿨어요`, 'success');
      setSheet(null);
    } catch (e) {
      toast.show(errorMessage(e), 'error');
    }
  }

  async function saveRetention() {
    if (!settings.data) return;
    const n = Number(days);
    try {
      await save.mutateAsync({ ...settings.data, policy: { ...settings.data.policy, retention: { days: n } } });
      toast.show(`게시 완료 후 ${n}일 보관으로 바꿨어요`, 'success');
      setSheet(null);
    } catch (e) {
      toast.show(errorMessage(e), 'error');
    }
  }

  const x = integrations.data?.x;
  const xValue = !x
    ? ''
    : x.connection.status === 'connected'
      ? `@${x.connection.username ?? ''}`
      : x.connection.status === 'expired'
        ? '다시 연결 필요'
        : '연결 안 됨';

  return (
    <>
      <PageHeader title="설정" />
      {settings.isLoading && <LoadingList rows={4} />}
      {settings.isError && <ErrorState error={settings.error} onRetry={() => settings.refetch()} />}
      {settings.data && (
        <div className={styles.stack}>
          <Section card title="운영">
            <RowGroup>
              <Row icon={<SlidersHorizontal size={20} />} label="운영 모드" value={MODE_LABEL[settings.data.operation_mode]} onClick={() => setSheet('mode')} />
              <Row icon={<Sparkles size={20} />} label="생성 방식" value={generationSummary(settings.data.policy.generation.timing)} to="/settings/generation" />
              <Row
                icon={<Scissors size={20} />}
                label="글 나누기"
                value={`${SPLIT_LABEL[settings.data.policy.thread.split_mode]}${settings.data.policy.thread.long_post_enabled ? ' · 장문' : ''}`}
                to="/settings/thread"
              />
              <Row icon={<FileText size={20} />} label="프롬프트" to="/prompts" />
              <Row icon={<ImageIcon size={20} />} label="이미지 보관함" description="업로드·생성 이미지, 정리 예정" to="/images" />
              <Row
                icon={<Archive size={20} />}
                label="게시 후 이미지 보관"
                description="저장소에서 지우기까지"
                value={`${settings.data.policy.retention.days}일`}
                onClick={() => {
                  setDays(String(settings.data!.policy.retention.days));
                  setSheet('retention');
                }}
              />
              <Row icon={<CalendarClock size={20} />} label="시간표" to="/schedule/rules" />
              <Row icon={<Send size={20} />} label="게시 기록" description="진행 중·확인 필요·최근 게시" to="/publish" />
            </RowGroup>
          </Section>
          <Section card title="연결">
            <RowGroup>
              <Row icon={<Link2 size={20} />} label="연결 상태" description="X·AI·저장소" value={xValue} to="/settings/connections" />
              <Row icon={<Wallet size={20} />} label="사용량·예산" description="추정 사용량·월 한도·요율표" to="/settings/budget" />
              <Row icon={<Wallet size={20} />} label="AI 비용" description="Gemini와 GPT 무료 한도·요금 비교" to="/ai-costs" />
            </RowGroup>
          </Section>
          <Section card title="화면">
            <RowGroup>
              <Row icon={<Moon size={20} />} label="테마" value={THEME_LABEL[theme.pref]} onClick={() => setSheet('theme')} />
            </RowGroup>
          </Section>
          <Section card title="정보">
            <RowGroup>
              <Row
                icon={<Info size={20} />}
                label="실행 환경"
                value={runtime.data ? `${runtime.data.env} · ${runtime.data.integration_mode === 'mock' ? '목업' : '실제 연동'}` : ''}
              />
              <Row icon={<ShieldCheck size={20} />} label="실제 게시" description="서버 설정으로 켜고 꺼요" value={runtime.data?.publishing_enabled ? '허용' : '꺼짐'} />
              <Row icon={<Tag size={20} />} label="버전" value={runtime.data?.version ?? ''} />
            </RowGroup>
          </Section>
          <RowGroup>
            <Row icon={<LogOut size={20} />} label="로그아웃" tone="danger" onClick={() => auth.logout().catch((e) => toast.show(`로그아웃하지 못했어요: ${errorMessage(e)}`, 'error'))} />
          </RowGroup>
        </div>
      )}

      <Sheet open={sheet === 'mode'} onClose={() => setSheet(null)} title="운영 모드" description="언제든 바꿀 수 있어요. 이미 승인된 글은 그대로예요.">
        {settings.data && <ChoiceList name="운영 모드" value={settings.data.operation_mode} options={MODE_OPTIONS} onChange={changeMode} />}
      </Sheet>
      <Sheet
        open={sheet === 'retention'}
        onClose={() => setSheet(null)}
        title="게시 후 이미지 보관"
        description="마지막 게시 완료 시각부터 세요. 업로드 날짜가 아니에요. 바꾸면 이미 게시한 이미지에도 새 기간이 적용돼요."
        footer={
          <>
            <Button variant="secondary" onClick={() => setSheet(null)}>
              취소
            </Button>
            <Button busy={save.isPending} disabled={!/^\d+$/.test(days) || Number(days) < 1 || Number(days) > 365} onClick={saveRetention}>
              저장
            </Button>
          </>
        }
      >
        <TextField label="보관 일수 (1~365)" type="number" inputMode="numeric" min={1} max={365} value={days} onChange={(e) => setDays(e.target.value)} suffix="일" />
      </Sheet>
      <Sheet
        open={sheet === 'theme'}
        onClose={() => setSheet(null)}
        title="테마"
        footer={
          <Button variant="secondary" onClick={() => setSheet(null)}>
            닫기
          </Button>
        }
      >
        <ChoiceList
          name="테마"
          value={theme.pref}
          onChange={theme.setPref}
          options={[
            { value: 'system', label: THEME_LABEL.system, description: '기기 설정을 따라요' },
            { value: 'light', label: THEME_LABEL.light },
            { value: 'dark', label: THEME_LABEL.dark },
          ]}
        />
      </Sheet>
    </>
  );
}
