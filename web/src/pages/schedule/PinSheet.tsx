import { useState } from 'react';
import { errorMessage } from '../../api/client';
import { confirmWarnings, useScheduleAction } from '../../api/schedule';
import { Button } from '../../components/ui/Button';
import { Sheet } from '../../components/ui/Sheet';
import { TextField } from '../../components/ui/TextField';
import { TimeField } from '../../components/ui/TimeField';
import { useToast } from '../../components/ui/Toast';
import { ContentPicker } from './ContentPicker';
import styles from './SchedulePage.module.css';

// 글 하나를 정한 날짜·시각에 넣고 시간 고정한다 (규칙 밖이면 경고 후 확인)
export function PinSheet({ open, onClose, defaultDate }: { open: boolean; onClose: () => void; defaultDate: string }) {
  const act = useScheduleAction();
  const toast = useToast();
  const [date, setDate] = useState(defaultDate);
  // 시트는 계속 마운트돼 있다. 열 때마다 지금 보고 있는 날짜로 맞춘다
  const [wasOpen, setWasOpen] = useState(open);
  if (open !== wasOpen) {
    setWasOpen(open);
    if (open) setDate(defaultDate);
  }
  const [time, setTime] = useState('12:00');
  const [content, setContent] = useState<{ id: string } | null>(null);
  const [picker, setPicker] = useState(false);
  const [warnings, setWarnings] = useState<string[] | null>(null);

  async function save(confirm: boolean) {
    if (!content) return;
    try {
      await act.mutateAsync({ path: '/api/schedule/pinned', body: { date, time, content_id: content.id, confirm } });
      toast.show('시간을 지정해 예약했어요', 'success');
      setWarnings(null);
      setContent(null);
      onClose();
    } catch (e) {
      const w = confirmWarnings(e);
      if (w) setWarnings(w);
      else toast.show(errorMessage(e), 'error');
    }
  }

  return (
    <>
      <Sheet
        open={open && !picker}
        onClose={onClose}
        title="시간 지정해 넣기"
        description="고른 글을 정한 시각에 예약하고 시간을 고정해요. 시간표를 다시 짜도 그대로예요."
        footer={
          warnings ? (
            <>
              <Button variant="secondary" onClick={() => setWarnings(null)}>
                다시 고르기
              </Button>
              <Button busy={act.isPending} onClick={() => save(true)}>
                그래도 예약
              </Button>
            </>
          ) : (
            <Button busy={act.isPending} disabled={!content || !date || !time} onClick={() => save(false)}>
              예약
            </Button>
          )
        }
      >
        <div className={styles.form}>
          <TextField label="날짜" type="date" value={date} onChange={(e) => setDate(e.target.value)} />
          <TimeField label="시각" value={time} onChange={setTime} />
          <Button variant="secondary" onClick={() => setPicker(true)}>
            {content ? '글을 골랐어요 · 바꾸기' : '글 고르기'}
          </Button>
          {warnings && (
            <div className={styles.warnBox} role="alert">
              <strong>규칙과 다른 점이 있어요</strong>
              <ul>
                {warnings.map((w) => (
                  <li key={w}>{w}</li>
                ))}
              </ul>
            </div>
          )}
        </div>
      </Sheet>
      <ContentPicker
        open={picker}
        onClose={() => setPicker(false)}
        title="예약할 글"
        onPick={(id) => {
          setContent({ id });
          setPicker(false);
        }}
      />
    </>
  );
}
