import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useState } from 'react';
import { errorMessage, get, post } from '../../api/client';
import { useSettings } from '../../api/queries';
import { Button } from '../../components/ui/Button';
import { Card, CardTitle } from '../../components/ui/Card';
import { Sheet } from '../../components/ui/Sheet';
import { useToast } from '../../components/ui/Toast';
import styles from './ImagesPage.module.css';

interface SweepReport {
  claimed: number;
  retried: number;
  deleted: number;
  failed: number;
  pending_removed: number;
}

// 보관 규칙 안내와 관리 작업: 지금 정리 실행, 저장소에만 남은 고아 파일 정리
export function RetentionCard() {
  const settings = useSettings();
  const qc = useQueryClient();
  const toast = useToast();
  const orphans = useQuery({ queryKey: ['media', 'orphans'], queryFn: () => get<{ items: { key: string; size: number }[] }>('/api/media/orphans') });
  const [busy, setBusy] = useState<'sweep' | 'orphans' | null>(null);
  const [confirm, setConfirm] = useState(false);
  const days = settings.data?.policy.retention.days ?? 14;
  const n = orphans.data?.items?.length ?? 0;

  async function sweep() {
    setBusy('sweep');
    try {
      const r = await post<SweepReport>('/api/media/retention/run');
      toast.show(
        r.deleted || r.failed ? `${r.deleted}개 정리${r.failed ? `, ${r.failed}개 실패 (다시 시도해요)` : ''}` : '지금 정리할 이미지가 없어요',
        r.failed ? 'error' : 'success',
      );
      await qc.invalidateQueries({ queryKey: ['media'] });
    } catch (e) {
      toast.show(errorMessage(e), 'error');
    } finally {
      setBusy(null);
    }
  }

  async function cleanOrphans() {
    setBusy('orphans');
    try {
      const r = await post<{ deleted: number }>('/api/media/orphans/clean');
      toast.show(`저장소에만 남은 파일 ${r.deleted}개를 지웠어요`, 'success');
      setConfirm(false);
      await qc.invalidateQueries({ queryKey: ['media', 'orphans'] });
    } catch (e) {
      toast.show(errorMessage(e), 'error');
    } finally {
      setBusy(null);
    }
  }

  return (
    <Card className={styles.retention}>
      <CardTitle>보관 규칙</CardTitle>
      <p className={styles.notice}>
        게시가 끝난 묶음의 이미지는 마지막 게시 완료 후 {days}일이 지나면 저장소에서 지워요 (매시간 검사). 한 번도 게시하지 않았거나, 작성 중·예약·게시 중·확인 필요인 글에서 쓰는 이미지는 지우지 않아요.
        X에 올라간 첨부 이미지와 게시 기록·링크는 그대로예요. 보관 일수는 설정에서 바꿀 수 있어요.
      </p>
      <div className={styles.retentionActions}>
        <Button size="sm" variant="secondary" busy={busy === 'sweep'} onClick={sweep}>
          지금 정리 실행
        </Button>
        {n > 0 && (
          <Button size="sm" variant="ghost" onClick={() => setConfirm(true)}>
            저장소에만 남은 파일 {n}개
          </Button>
        )}
      </div>
      <Sheet
        open={confirm}
        onClose={() => setConfirm(false)}
        title="저장소에만 남은 파일을 지울까요?"
        description="업로드 중 끊겨 기록되지 않은 파일이에요. 이 서비스 전용 경로에서 하루 넘게 쓰이지 않은 파일만, 지우기 직전에 한 번 더 확인하고 지워요."
        footer={
          <>
            <Button variant="secondary" onClick={() => setConfirm(false)}>
              취소
            </Button>
            <Button variant="danger" busy={busy === 'orphans'} onClick={cleanOrphans}>
              {n}개 지우기
            </Button>
          </>
        }
      >
        <p className={styles.notice}>
          파일 {n}개 · 합계 {Math.ceil((orphans.data?.items?.reduce((sum, o) => sum + o.size, 0) ?? 0) / 1024).toLocaleString()}KB. 글에 붙은 이미지는 건드리지 않아요.
        </p>
      </Sheet>
    </Card>
  );
}
