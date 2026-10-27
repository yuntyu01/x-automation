import { useQuery } from '@tanstack/react-query';
import { Search } from 'lucide-react';
import { useState } from 'react';
import { get } from '../../api/client';
import { contentQueryString } from '../../api/contents';
import { STAGE_LABEL, type ContentPage, type Stage } from '../../api/types';
import { StageBadge } from '../../components/StageBadge';
import { Segmented } from '../../components/ui/Segmented';
import { Sheet } from '../../components/ui/Sheet';
import { LoadingList } from '../../components/ui/States';
import { TextField } from '../../components/ui/TextField';
import { useDebounced } from '../../lib/hooks';
import styles from './SchedulePage.module.css';

type Pick = 'ready' | 'review' | 'draft';

// 예약에 넣을 글 고르기. 준비 완료가 아닌 글도 넣을 수 있고, 준비될 때까지 대기로 보인다
export function ContentPicker({ open, onClose, onPick, title }: { open: boolean; onClose: () => void; onPick: (id: string) => void; title: string }) {
  const [stage, setStage] = useState<Pick>('ready');
  const [q, setQ] = useState('');
  const dq = useDebounced(q, 300);
  const list = useQuery({
    queryKey: ['contents', 'list', 'picker', stage, dq],
    queryFn: () => get<ContentPage>(`/api/contents?${contentQueryString({ stage, q: dq, limit: 50 })}`),
    enabled: open,
  });
  return (
    <Sheet open={open} onClose={onClose} title={title} description="이미 다른 예약에 있는 글을 고르면 그 예약에서 빠지고 여기로 와요" size="lg">
      <div className={styles.pickerTools}>
        <Segmented
          label="상태"
          value={stage}
          onChange={setStage}
          options={(['ready', 'review', 'draft'] as Stage[]).map((s) => ({ value: s as Pick, label: STAGE_LABEL[s] }))}
        />
        <TextField label="검색" hideLabel type="search" placeholder="제목이나 원문으로 검색" value={q} onChange={(e) => setQ(e.target.value)} suffix={<Search size={18} aria-hidden />} />
      </div>
      {list.isLoading && <LoadingList rows={3} />}
      {list.data && list.data.items.length === 0 && <p className={styles.muted}>{STAGE_LABEL[stage]} 글이 없어요.</p>}
      <ul className={styles.pickList}>
        {list.data?.items.map((c) => (
          <li key={c.id}>
            <button type="button" className={styles.pickItem} onClick={() => onPick(c.id)}>
              <span className={styles.title}>{c.title || '제목 없음'}</span>
              <span className={styles.pickPreview}>{c.preview}</span>
              <span className={styles.meta}>
                <StageBadge stage={c.stage} />
                <span>X 게시물 {c.part_count}개</span>
              </span>
            </button>
          </li>
        ))}
      </ul>
    </Sheet>
  );
}
