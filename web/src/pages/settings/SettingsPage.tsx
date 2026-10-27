import { CalendarClock, FileText, ImageIcon, Info, Link2, LogOut, Moon, Scissors, SlidersHorizontal, Sparkles, Send, Wallet } from 'lucide-react';
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
  const [sheet, setSheet] = useState<'mode' | 'theme' | null>(null);

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
          <Section title="운영">
            <RowGroup>
              <Row icon={<SlidersHorizontal size={20} />} label="운영 모드" value={MODE_LABEL[settings.data.operation_mode]} onClick={() => setSheet('mode')} />
              <Row icon={<Sparkles size={20} />} label="생성 방식" value={generationSummary(settings.data.policy.generation.timing)} to="/settings/generation" />
              <Row
                icon={<Scissors size={20} />}
                label="나누기·글자 수"
                value={`${SPLIT_LABEL[settings.data.policy.thread.split_mode]}${settings.data.policy.thread.long_post_enabled ? ' · 장문' : ''}`}
                to="/settings/thread"
              />
              <Row icon={<FileText size={20} />} label="프롬프트" to="/prompts" />
              <Row icon={<ImageIcon size={20} />} label="이미지 보관함" description="업로드·생성 이미지, 정리 예정" to="/images" />
              <Row icon={<CalendarClock size={20} />} label="시간표" to="/schedule/rules" />
              <Row icon={<Send size={20} />} label="게시 기록" description="진행 중·확인 필요·최근 게시" to="/publish" />
            </RowGroup>
          </Section>
          <Section title="연결">
            <RowGroup>
              <Row icon={<Link2 size={20} />} label="X·AI·이미지 저장소" description="연결 상태와 필요한 설정" value={xValue} to="/settings/connections" />
              <Row icon={<Wallet size={20} />} label="사용량·예산" description="이번 달 추정 사용량, 월 한도, 요율표" to="/settings/budget" />
            </RowGroup>
          </Section>
          <Section title="화면">
            <RowGroup>
              <Row icon={<Moon size={20} />} label="테마" value={THEME_LABEL[theme.pref]} onClick={() => setSheet('theme')} />
            </RowGroup>
          </Section>
          <Section title="정보">
            <RowGroup>
              <Row
                icon={<Info size={20} />}
                label="실행 환경"
                value={runtime.data ? `${runtime.data.env} · ${runtime.data.integration_mode === 'mock' ? '목업' : '실제 연동'}` : ''}
              />
              <Row label="실제 게시 허용 (서버 설정)" value={runtime.data?.publishing_enabled ? '허용' : '차단'} />
              <Row label="버전" value={runtime.data?.version ?? ''} />
            </RowGroup>
          </Section>
          <RowGroup>
            <Row icon={<LogOut size={20} />} label="로그아웃" tone="danger" onClick={() => void auth.logout()} />
          </RowGroup>
        </div>
      )}

      <Sheet open={sheet === 'mode'} onClose={() => setSheet(null)} title="운영 모드" description="언제든 바꿀 수 있어요. 이미 승인된 글은 그대로예요.">
        {settings.data && <ChoiceList name="운영 모드" value={settings.data.operation_mode} options={MODE_OPTIONS} onChange={changeMode} />}
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
