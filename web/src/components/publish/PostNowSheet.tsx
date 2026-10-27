import { Send, TriangleAlert } from 'lucide-react';
import { useState } from 'react';
import { Link } from 'react-router';
import { errorMessage } from '../../api/client';
import { usePostNow } from '../../api/publish';
import { useRuntime } from '../../api/queries';
import { confirmWarnings } from '../../api/schedule';
import { Button } from '../ui/Button';
import { Sheet } from '../ui/Sheet';
import { useToast } from '../ui/Toast';
import styles from './publish.module.css';

// "지금 게시": X에 바로 올리는 외부 작업이라 항상 한 번 확인한다. 허용 구간·간격·하루 수량을 벗어나면 경고를 보여 주고 한 번 더 확인한다
export function PostNowSheet({ open, onClose, contentId, parts }: { open: boolean; onClose: () => void; contentId: string; parts: number }) {
  const post = usePostNow();
  const runtime = useRuntime();
  const toast = useToast();
  const [warnings, setWarnings] = useState<string[] | null>(null);
  const mock = runtime.data?.integration_mode === 'mock';

  function close() {
    setWarnings(null);
    onClose();
  }

  async function submit(confirm: boolean) {
    try {
      await post.mutateAsync({ content_id: contentId, confirm });
      toast.show(mock ? '목업 게시를 시작했어요 (실제 X에는 올라가지 않아요)' : '게시를 시작했어요. 게시 화면에서 진행을 볼 수 있어요', 'success');
      close();
    } catch (e) {
      const w = confirmWarnings(e);
      if (w) setWarnings(w);
      else toast.show(errorMessage(e), 'error');
    }
  }

  return (
    <Sheet
      open={open}
      onClose={close}
      dismissible={!post.isPending}
      title={warnings ? '규칙과 다른 게시예요' : '지금 X에 게시할까요?'}
      description={`본문${parts > 1 ? ` + 답글 ${parts - 1}개` : ''}를 순서대로 올려요. 올라간 게시물은 앱에서 지울 수 없어요.`}
      footer={
        <>
          <Button variant="secondary" onClick={close} disabled={post.isPending}>
            취소
          </Button>
          <Button icon={<Send size={16} />} busy={post.isPending} onClick={() => submit(warnings !== null)}>
            {warnings ? '그래도 지금 게시' : '지금 게시'}
          </Button>
        </>
      }
    >
      {mock && <p className={styles.mock}>목업 모드예요. 테스트 대역에만 기록되고 실제 X에는 올라가지 않아요.</p>}
      {warnings && (
        <ul className={styles.warnings}>
          {warnings.map((w) => (
            <li key={w}>
              <TriangleAlert size={16} aria-hidden /> {w}
            </li>
          ))}
        </ul>
      )}
      <p className={styles.muted}>
        이 글이 다른 예약에 들어 있으면 그 예약에서 빼고 지금 게시해요 (같은 글이 두 번 올라가지 않게). 진행 상황은 <Link to="/publish">게시 화면</Link>에서 볼 수 있어요.
      </p>
    </Sheet>
  );
}
