import { AtSign, ChevronLeft, Cloud, ImageIcon, Sparkles } from 'lucide-react';
import type { ReactNode } from 'react';
import { useNavigate } from 'react-router';
import { useIntegrations } from '../../api/queries';
import type { IntegrationStatus } from '../../api/types';
import { PageHeader } from '../../components/layout/AppShell';
import { Badge, type Tone } from '../../components/ui/Badge';
import { Card } from '../../components/ui/Card';
import { IconButton } from '../../components/ui/IconButton';
import { ErrorState, LoadingList } from '../../components/ui/States';
import { XConnectActions } from './XConnectActions';
import styles from './ConnectionsPage.module.css';

export function xState(s: IntegrationStatus): { tone: Tone; label: string } {
  const c = s.x.connection;
  if (c.status === 'connected') return { tone: 'success', label: '연결됨' };
  if (c.status === 'expired') return { tone: 'warning', label: '다시 연결 필요' };
  if (c.status === 'error') return { tone: 'danger', label: '연결 오류' };
  if (!s.x.configured) return { tone: 'neutral', label: '서버 설정 필요' };
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
  return (
    <>
      <PageHeader title="연결" subtitle="키·비밀번호는 서버 환경변수에만 있고 여기에는 보이지 않아요" back={<BackButton to="/settings" />} />
      {q.isLoading && <LoadingList rows={3} />}
      {q.isError && <ErrorState error={q.error} onRetry={() => q.refetch()} />}
      {q.data && (
        <div className={styles.stack}>
          {q.data.mode === 'mock' && (
            <Card className={styles.mock}>
              <strong>목업 모드로 실행 중이에요.</strong> X 게시·AI 생성·R2 저장은 테스트 대역이 처리하고 실제로 일어나지 않아요. 운영에서는 <code>INTEGRATION_MODE=live</code>로 실행합니다.
            </Card>
          )}
          <Integration
            icon={<AtSign size={22} />}
            title="X 계정"
            state={xState(q.data)}
            detail={
              q.data.x.connection.status === 'connected'
                ? `@${q.data.x.connection.username} 계정에 게시해요${q.data.x.connection.status_detail ? ` · ${q.data.x.connection.status_detail}` : ''}`
                : q.data.x.configured
                  ? '계정을 연결하면 본문과 자기 답글을 게시할 수 있어요'
                  : 'X 개발자 앱의 X_CLIENT_ID·X_CLIENT_SECRET·X_REDIRECT_URI를 서버에 설정해야 연결할 수 있어요'
            }
          >
            <XConnectActions status={q.data} />
          </Integration>
          <Integration
            icon={<Sparkles size={22} />}
            title="AI 생성"
            state={q.data.ai.configured ? { tone: 'success', label: q.data.mode === 'mock' ? '목업' : '설정됨' } : { tone: 'neutral', label: '서버 설정 필요' }}
            detail={
              q.data.ai.configured
                ? `텍스트 ${q.data.ai.text_model || '모델 미지정'} · 이미지 ${q.data.ai.image_model || '모델 미지정'}`
                : 'AI_API_KEY와 모델(AI_TEXT_MODEL·AI_IMAGE_MODEL)을 설정하면 글·이미지를 생성할 수 있어요. 없어도 직접 쓴 글은 게시할 수 있어요'
            }
          />
          <Integration
            icon={<Cloud size={22} />}
            title="이미지 저장소 (R2)"
            state={q.data.r2.configured ? { tone: q.data.r2.dev_url ? 'warning' : 'success', label: q.data.mode === 'mock' ? '목업' : q.data.r2.dev_url ? '개발용 주소' : '설정됨' } : { tone: 'neutral', label: '서버 설정 필요' }}
            detail={
              q.data.r2.configured
                ? `${q.data.r2.bucket || '로컬 저장소'} / ${q.data.r2.prefix}`
                : 'R2_ACCOUNT_ID(또는 R2_ENDPOINT)·R2_ACCESS_KEY_ID·R2_SECRET_ACCESS_KEY·R2_BUCKET을 설정하면 이미지를 쓸 수 있어요'
            }
          >
            <p className={styles.help}>
              <ImageIcon size={16} aria-hidden /> 공개 조회 주소로 저장하므로 아직 게시하지 않은 이미지도 주소를 아는 사람은 볼 수 있어요.
              {q.data.r2.dev_url && ' r2.dev 주소는 속도 제한이 있는 개발용이라 운영에서는 커스텀 도메인을 권장해요.'}
            </p>
          </Integration>
          <Card>
            <p className={styles.gate}>
              실제 게시는 <strong>서버의 실게시 허용({q.data.publishing_enabled ? '켜짐' : '꺼짐'})</strong>, X 연결, 앱의 <strong>자동 운영 시작</strong>이 모두 갖춰져야 일어나요.
            </p>
          </Card>
        </div>
      )}
    </>
  );
}

function Integration({ icon, title, state, detail, children }: { icon: ReactNode; title: string; state: { tone: Tone; label: string }; detail: string; children?: ReactNode }) {
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
        </div>
      </div>
      {children && <div className={styles.extra}>{children}</div>}
    </Card>
  );
}
