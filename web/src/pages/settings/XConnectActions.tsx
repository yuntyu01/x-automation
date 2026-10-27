import { Link2, Link2Off, RefreshCw } from 'lucide-react';
import { useEffect, useRef, useState } from 'react';
import { useSearchParams } from 'react-router';
import { errorMessage } from '../../api/client';
import { startXConnect, useXConnectionAction } from '../../api/publish';
import type { IntegrationStatus } from '../../api/types';
import { Button } from '../../components/ui/Button';
import { Sheet } from '../../components/ui/Sheet';
import { useToast } from '../../components/ui/Toast';
import styles from './ConnectionsPage.module.css';

const REQUIRED = ['tweet.read', 'tweet.write', 'users.read', 'offline.access'];

// X 계정 연결·해제·확인. 실제 OAuth(PKCE) 흐름은 서버의 /api/x/oauth/* 가 담당하고, 토큰은 화면에 오지 않는다
export function XConnectActions({ status }: { status: IntegrationStatus }) {
  const toast = useToast();
  const act = useXConnectionAction();
  const [params, setParams] = useSearchParams();
  const [starting, setStarting] = useState(false);
  const [confirmOff, setConfirmOff] = useState(false);
  const shown = useRef(false);
  const c = status.x.connection;

  // 콜백에서 돌아온 결과를 한 번 알려 주고 주소에서 지운다
  useEffect(() => {
    if (shown.current) return;
    const ok = params.get('x');
    const err = params.get('x_error');
    if (!ok && !err) return;
    shown.current = true;
    if (ok === 'connected') toast.show('X 계정을 연결했어요', 'success');
    if (err) toast.show(err, 'error');
    const next = new URLSearchParams(params);
    next.delete('x');
    next.delete('x_error');
    setParams(next, { replace: true });
  }, [params, setParams, toast]);

  async function connect() {
    setStarting(true);
    try {
      await startXConnect();
    } catch (e) {
      toast.show(errorMessage(e), 'error');
      setStarting(false);
    }
  }

  async function run(kind: 'disconnect' | 'check') {
    try {
      const v = await act.mutateAsync(kind);
      if (kind === 'disconnect') toast.show('X 연결을 해제했어요. 예약 게시는 다시 연결할 때까지 멈춰요', 'success');
      else toast.show(v.status === 'connected' ? `@${v.username} 연결이 정상이에요` : (v.status_detail ?? '연결을 확인해 주세요'), v.status === 'connected' ? 'success' : 'error');
      setConfirmOff(false);
    } catch (e) {
      toast.show(errorMessage(e), 'error');
    }
  }

  if (!status.x.configured) return null;
  const connected = c.status !== 'disconnected';
  const missing = REQUIRED.filter((s) => connected && !c.scopes.includes(s));
  const sub = typeof c.capabilities.subscription_type === 'string' ? c.capabilities.subscription_type : '';
  return (
    <>
      {connected && (
        <dl className={styles.facts}>
          <div>
            <dt>계정</dt>
            <dd>
              @{c.username}
              {c.display_name ? ` (${c.display_name})` : ''}
            </dd>
          </div>
          <div>
            <dt>구독 등급</dt>
            <dd>{sub || '알 수 없음'}</dd>
          </div>
          <div>
            <dt>장문 게시</dt>
            <dd>{c.capabilities.long_post_possible ? '가능할 수 있음 (등급 확인 필요)' : '불가 (일반 길이로 분할)'}</dd>
          </div>
          <div>
            <dt>이미지 업로드</dt>
            <dd>{c.capabilities.media_write ? '가능' : '권한 없음 (다시 연결 필요)'}</dd>
          </div>
          {missing.length > 0 && (
            <div>
              <dt>빠진 권한</dt>
              <dd className={styles.bad}>{missing.join(', ')}</dd>
            </div>
          )}
        </dl>
      )}
      {c.status !== 'connected' && (
        <Button icon={<Link2 size={16} />} busy={starting} onClick={connect}>
          {connected ? '다시 연결' : 'X 계정 연결'}
        </Button>
      )}
      {connected && (
        <>
          <Button variant="secondary" icon={<RefreshCw size={16} />} busy={act.isPending && act.variables === 'check'} onClick={() => run('check')}>
            연결 확인
          </Button>
          <Button variant="ghost" icon={<Link2Off size={16} />} onClick={() => setConfirmOff(true)}>
            연결 해제
          </Button>
        </>
      )}
      {status.mode === 'mock' && <p className={styles.help}>목업 모드에서는 X로 이동하지 않고 테스트 계정(@mock_account)으로 바로 연결돼요. 실제 게시는 일어나지 않아요.</p>}
      <Sheet
        open={confirmOff}
        onClose={() => setConfirmOff(false)}
        title="X 연결을 해제할까요?"
        description="저장한 토큰을 지우고 X에도 해제를 요청해요. 다시 연결하기 전까지 예약 게시가 멈춰요."
        footer={
          <>
            <Button variant="secondary" onClick={() => setConfirmOff(false)}>
              취소
            </Button>
            <Button variant="danger" busy={act.isPending && act.variables === 'disconnect'} onClick={() => run('disconnect')}>
              연결 해제
            </Button>
          </>
        }
      >
        <p className={styles.detail}>이미 올라간 게시물은 지워지지 않아요.</p>
      </Sheet>
    </>
  );
}
