import { ImageIcon } from 'lucide-react';
import { useState } from 'react';
import { useMediaList, type MediaAsset, type MediaFilter } from '../../api/media';
import { useIntegrations } from '../../api/queries';
import { PageHeader } from '../../components/layout/AppShell';
import { MediaDetailSheet, MediaTile, UploadDrop } from '../../components/media/MediaParts';
import { UploadList, useUploader } from '../../components/media/useUploader';
import { Button } from '../../components/ui/Button';
import { Card } from '../../components/ui/Card';
import { Segmented } from '../../components/ui/Segmented';
import { EmptyState, ErrorState, LoadingList } from '../../components/ui/States';
import { TextField } from '../../components/ui/TextField';
import { useDebounced } from '../../lib/hooks';
import { BackButton } from '../settings/ConnectionsPage';
import styles from './ImagesPage.module.css';

export function ImagesPage() {
  const [filter, setFilter] = useState<MediaFilter>('all');
  const [search, setSearch] = useState('');
  const q = useDebounced(search, 300);
  const [limit, setLimit] = useState(60);
  const list = useMediaList(filter, q, limit);
  const integrations = useIntegrations();
  const up = useUploader();
  const [open, setOpen] = useState<MediaAsset | null>(null);
  const r2 = integrations.data?.r2;

  return (
    <>
      <PageHeader title="이미지 보관함" subtitle="올리거나 생성한 이미지를 다시 쓸 수 있어요" back={<BackButton />} />
      {r2 && !r2.configured && (
        <Card className={styles.warn}>이미지 저장소(R2) 설정이 없어 올릴 수 없어요. 서버 환경변수를 설정하세요.</Card>
      )}
      {r2?.configured && (
        <p className={styles.notice}>
          공개 조회 주소로 저장해요. 아직 게시하지 않은 이미지도 주소를 아는 사람은 볼 수 있어요. 게시가 끝난 이미지는 보관 기간이 지나면 정리돼요.
        </p>
      )}
      <UploadDrop onFiles={up.add} />
      <UploadList items={up.items} onRetry={up.retry} />
      <div className={styles.toolbar}>
        <Segmented
          label="이미지 필터"
          scroll
          value={filter}
          onChange={setFilter}
          options={[
            { value: 'all', label: '전체' },
            { value: 'used', label: '사용 중' },
            { value: 'unused', label: '미사용' },
            { value: 'cleaned', label: '정리됨' },
          ]}
        />
        <TextField label="대체 텍스트·프롬프트 검색" hideLabel type="search" placeholder="대체 텍스트나 생성 프롬프트로 검색" value={search} onChange={(e) => setSearch(e.target.value)} />
      </div>
      {list.isLoading && <LoadingList rows={2} />}
      {list.isError && <ErrorState error={list.error} onRetry={() => list.refetch()} />}
      {list.data && list.data.items.length === 0 && (
        <Card>
          <EmptyState icon={<ImageIcon size={28} aria-hidden />} title={filter === 'cleaned' ? '정리된 이미지가 없어요' : '이미지가 없어요'} body="위에서 이미지를 올리거나, 글을 만들 때 AI 이미지를 생성할 수 있어요." />
        </Card>
      )}
      {list.data && list.data.items.length > 0 && (
        <>
          <p className={styles.count}>{list.data.total}장</p>
          <ul className={styles.grid}>
            {list.data.items.map((a) => (
              <li key={a.id}>
                <MediaTile asset={a} onClick={() => setOpen(a)} />
              </li>
            ))}
          </ul>
          {list.data.total > list.data.items.length && (
            <Button variant="secondary" block className={styles.more} onClick={() => setLimit((l) => l + 60)}>
              더 보기
            </Button>
          )}
        </>
      )}
      <MediaDetailSheet asset={open} onClose={() => setOpen(null)} />
    </>
  );
}
