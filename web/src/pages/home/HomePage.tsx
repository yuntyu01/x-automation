import { ArrowRight, Power } from 'lucide-react';
import { Link } from 'react-router';
import { useIntegrations, useRuntime, useSettings } from '../../api/queries';
import { PageHeader } from '../../components/layout/AppShell';
import { Badge } from '../../components/ui/Badge';
import { Card, CardTitle } from '../../components/ui/Card';
import { ErrorState, LoadingList } from '../../components/ui/States';
import { formatDate } from '../../lib/format';
import { blockers } from './blockers';
import styles from './HomePage.module.css';

export function HomePage() {
  const settings = useSettings();
  const runtime = useRuntime();
  const integrations = useIntegrations();

  return (
    <>
      <PageHeader title="홈" subtitle={formatDate(new Date().toISOString())} />
      {settings.isLoading && <LoadingList rows={3} />}
      {settings.isError && <ErrorState error={settings.error} onRetry={() => settings.refetch()} />}
      {settings.data && (
        <div className={styles.stack}>
          {!settings.data.setup_completed_at && (
            <Card className={styles.setup}>
              <CardTitle>처음이라면 설정부터 해요</CardTitle>
              <p className={styles.muted}>연결 상태, 프롬프트, 운영 모드, 시간표를 차례로 확인해요.</p>
              <Link to="/setup" className={styles.cta}>
                첫 설정 시작 <ArrowRight size={18} aria-hidden />
              </Link>
            </Card>
          )}
          {runtime.data && integrations.data && (
            <Card>
              <CardTitle action={<Badge tone={settings.data.automation_enabled ? 'success' : 'neutral'}>{settings.data.automation_enabled ? '켜짐' : '꺼짐'}</Badge>}>
                <span className={styles.inline}>
                  <Power size={20} aria-hidden /> 자동 운영
                </span>
              </CardTitle>
              {(() => {
                const list = blockers({
                  publishingEnabled: runtime.data.publishing_enabled,
                  mock: runtime.data.integration_mode === 'mock',
                  xConnected: integrations.data.x.connection.status === 'connected',
                  automation: settings.data.automation_enabled,
                  publishingPaused: settings.data.publishing_paused,
                });
                return list.length ? (
                  <ul className={styles.reasons}>
                    {list.map((r) => (
                      <li key={r}>{r}</li>
                    ))}
                  </ul>
                ) : (
                  <p className={styles.muted}>예약된 시각에 맞춰 게시하고 있어요.</p>
                );
              })()}
            </Card>
          )}
        </div>
      )}
    </>
  );
}
