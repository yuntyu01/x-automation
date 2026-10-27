import { useState } from 'react';
import { useMediaList, type MediaAsset } from '../../api/media';
import { Button } from '../ui/Button';
import { Sheet } from '../ui/Sheet';
import { EmptyState, LoadingList } from '../ui/States';
import { MediaTile, UploadDrop } from './MediaParts';
import { UploadList, useUploader } from './useUploader';
import styles from './media.module.css';

// 보관함에서 고르거나 새로 올려 게시 부분에 붙인다. 고른 순서가 게시 순서다
export function MediaPicker({ open, onClose, max, onPick, title }: { open: boolean; onClose: () => void; max: number; onPick: (ids: string[]) => void; title: string }) {
  const [picked, setPicked] = useState<string[]>([]);
  const list = useMediaList('all', '', 90);
  const up = useUploader((a: MediaAsset) => setPicked((p) => (p.length < max && !p.includes(a.id) ? [...p, a.id] : p)));
  const toggle = (id: string) => setPicked((p) => (p.includes(id) ? p.filter((x) => x !== id) : p.length < max ? [...p, id] : p));
  const close = () => {
    setPicked([]);
    onClose();
  };
  return (
    <Sheet
      open={open}
      onClose={close}
      title={title}
      description={`최대 ${max}장까지 고를 수 있어요. 고른 순서대로 붙어요.`}
      size="lg"
      footer={
        <>
          <Button variant="secondary" onClick={close}>
            취소
          </Button>
          <Button
            disabled={picked.length === 0}
            onClick={() => {
              onPick(picked);
              setPicked([]);
            }}
          >
            {picked.length}장 붙이기
          </Button>
        </>
      }
    >
      <UploadDrop onFiles={up.add} label="새 이미지 올리기" />
      <UploadList items={up.items} onRetry={up.retry} />
      {list.isLoading && <LoadingList rows={1} />}
      {list.data && list.data.items.length === 0 && <EmptyState title="보관함이 비어 있어요" body="위에서 이미지를 올려 주세요." />}
      <ul className={styles.grid}>
        {list.data?.items
          .filter((a) => a.state === 'active')
          .map((a) => (
            <li key={a.id}>
              <MediaTile asset={a} selectedIndex={picked.indexOf(a.id)} onClick={() => toggle(a.id)} />
            </li>
          ))}
      </ul>
    </Sheet>
  );
}
