import { AtSign, ChevronLeft, Cloud, ImageIcon, Sparkles } from 'lucide-react';
import type { ReactNode } from 'react';
import { useNavigate } from 'react-router';
import { useHome } from '../../api/home';
import { useIntegrations } from '../../api/queries';
import type { IntegrationStatus } from '../../api/types';
import { PageHeader } from '../../components/layout/AppShell';
import { Badge, type Tone } from '../../components/ui/Badge';
import { Card, Section } from '../../components/ui/Card';
import { Row, RowGroup } from '../../components/ui/Row';
import { IconButton } from '../../components/ui/IconButton';
import { ErrorState, LoadingList } from '../../components/ui/States';
import { XConnectActions } from './XConnectActions';
import styles from './ConnectionsPage.module.css';

export function xState(s: IntegrationStatus): { tone: Tone; label: string } {
  const c = s.x.connection;
  if (c.status === 'connected') return { tone: 'success', label: '연결됨' };
  if (c.status === 'expired') return { tone: 'warning', label: '다시 연결 필요' };
  if (c.status === 'error') return { tone: 'danger', label: '연결 오류' };
  if (!s.x.configured) return { tone: 'neutral', label: '설정 필요' };
  return { tone: 'neutral', label: '연결 안 됨' };
}

export function BackButton({ to }: { to?: string }) {
  const nav = useNavigate();
  return (
    <IconButton label="뒤로" onClick={() => (to ? nav(to) : nav(-1))}>
      <ChevronLeft size={24} />
    </IconButton>
  );
}

export function ConnectionsPage() {
  const q = useIntegrations();
  const home = useHome();
  return (
    <>
      <PageHeader title="연결" subtitle="키와 비밀번호는 서버에만 있고 여기에는 보이지 않아요" back={<BackButton to="/settings" />} />
      {q.isLoading && <LoadingList rows={3} />}
      {q.isError && <ErrorState error={q.error} onRetry={() => q.refetch()} />}
      {q.data && (
        <div className={styles.stack}>
          <Integration
            icon={<AtSign size={22} />}
            title="X 계정"
            state={xState(q.data)}
            detail={
              q.data.x.connection.status === 'connected'
                ? `@${q.data.x.connection.username} 계정에 게시해요${q.data.x.connection.status_detail ? ` · ${q.data.x.connection.status_detail}` : ''}`
                : q.data.x.configured
                  ? '연결하면 본문과 자기 답글을 게시할 수 있어요'
                  : '서버에 X 개발자 앱 정보를 넣으면 연결할 수 있어요'
            }
            env={q.data.x.configured ? undefined : 'X_CLIENT_ID · X_CLIENT_SECRET · X_REDIRECT_URI'}
          >
            <XConnectActions status={q.data} />
          </Integration>
          <Integration
            icon={<Sparkles size={22} />}
            title="AI 생성"
            state={q.data.ai.configured ? { tone: 'success', label: q.data.mode === 'mock' ? '목업' : '설정됨' } : { tone: 'neutral', label: '설정 필요' }}
            detail={
              q.data.ai.configured
                ? `글 ${q.data.ai.text_model || '모델 미지정'} · 이미지 ${q.data.ai.image_model || '모델 미지정'}`
                : '설정하면 글과 이미지를 만들 수 있어요. 없어도 직접 쓴 글은 게시돼요'
            }
            env={q.data.ai.configured ? undefined : 'AI_API_KEY · AI_TEXT_MODEL · AI_IMAGE_MODEL'}
          />
          <Integration
            icon={<Cloud size={22} />}
            title="이미지 저장소"
            state={q.data.r2.configured ? { tone: q.data.r2.dev_url ? 'warning' : 'success', label: q.data.mode === 'mock' ? '목업' : q.data.r2.dev_url ? '개발용 주소' : '설정됨' } : { tone: 'neutral', label: '설정 필요' }}
            detail={q.data.r2.configured ? `${q.data.r2.bucket || '로컬 저장소'} / ${q.data.r2.prefix}` : 'Cloudflare R2를 설정하면 이미지를 붙일 수 있어요'}
            env={q.data.r2.configured ? undefined : 'R2_ACCOUNT_ID · R2_ACCESS_KEY_ID · R2_SECRET_ACCESS_KEY · R2_BUCKET'}
          >
            <p className={styles.help}>
              <ImageIcon size={16} aria-hidden /> 공개 주소로 저장해서, 게시 전 이미지도 주소를 알면 볼 수 있어요.
              {q.data.r2.dev_url && ' r2.dev 주소는 속도 제한이 있어 운영에는 커스텀 도메인이 좋아요.'}
            </p>
          </Integration>
          <Section card title="실제 게시 조건" description="셋이 모두 갖춰져야 예약 게시가 시작돼요">
            <RowGroup>
              <Row
                label="실제 게시 허용"
                description={q.data.mode === 'mock' ? '목업 모드에서는 테스트 대역에만 기록해요' : '서버 설정 PUBLISHING_ENABLED'}
                value={<Badge tone={q.data.publishing_enabled ? 'success' : 'neutral'}>{q.data.publishing_enabled ? '허용' : '꺼짐'}</Badge>}
              />
              <Row label="X 계정 연결" value={<Badge tone={xState(q.data).tone}>{xState(q.data).label}</Badge>} />
              <Row
                label="자동 운영"
                description="홈에서 시작하고 멈춰요"
                value={home.data ? <Badge tone={home.data.automation.enabled ? 'success' : 'neutral'}>{home.data.automation.enabled ? '켜짐' : '꺼짐'}</Badge> : undefined}
                to="/"
              />
            </RowGroup>
          </Section>
        </div>
      )}
    </>
  );
}

function Integration({ icon, title, state, detail, env, children }: { icon: ReactNode; title: string; state: { tone: Tone; label: string }; detail: string; env?: string; children?: ReactNode }) {
  return (
    <Card>
      <div className={styles.row}>
        <span className={styles.icon}>{icon}</span>
        <div className={styles.text}>
          <div className={styles.titleRow}>
            <h2 className={styles.title}>{title}</h2>
            <Badge tone={state.tone}>{state.label}</Badge>
          </div>
          <p className={styles.detail}>{detail}</p>
          {env && (
            <p className={styles.env}>
              서버 환경변수 <code>{env}</code>
            </p>
          )}
        </div>
      </div>
      {children && <div className={styles.extra}>{children}</div>}
    </Card>
  );
}
