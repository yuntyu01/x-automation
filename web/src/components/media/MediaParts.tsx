import { useQueryClient } from '@tanstack/react-query';
import { Crop, Download, ImagePlus, Trash2 } from 'lucide-react';
import { useState, type DragEvent } from 'react';
import { Link } from 'react-router';
import { del, errorMessage, patch, post } from '../../api/client';
import { formatBytes, useMediaUsage, type MediaAsset } from '../../api/media';
import { formatDate } from '../../lib/format';
import { Badge } from '../ui/Badge';
import { Button } from '../ui/Button';
import { Sheet } from '../ui/Sheet';
import { TextArea } from '../ui/Field';
import { useToast } from '../ui/Toast';
import styles from './media.module.css';
import own from './MediaParts.module.css';

export function UploadDrop({ onFiles, label = '이미지 올리기 (여러 장 가능)' }: { onFiles: (f: FileList) => void; label?: string }) {
  const [drag, setDrag] = useState(false);
  const onDrop = (e: DragEvent) => {
    e.preventDefault();
    setDrag(false);
    if (e.dataTransfer.files.length) onFiles(e.dataTransfer.files);
  };
  return (
    <label
      className={[styles.drop, drag ? styles.dragging : ''].join(' ')}
      onDragOver={(e) => {
        e.preventDefault();
        setDrag(true);
      }}
      onDragLeave={() => setDrag(false)}
      onDrop={onDrop}
    >
      <ImagePlus size={20} aria-hidden />
      <span>{label}</span>
      <input
        type="file"
        accept="image/jpeg,image/png,image/webp,image/gif"
        multiple
        className="sr-only"
        onChange={(e) => {
          if (e.target.files?.length) onFiles(e.target.files);
          e.target.value = '';
        }}
      />
    </label>
  );
}

export function MediaTile({ asset, selectedIndex, onClick }: { asset: MediaAsset; selectedIndex?: number; onClick: () => void }) {
  const selected = selectedIndex !== undefined && selectedIndex >= 0;
  return (
    <button type="button" className={[styles.tile, selected ? styles.tileSelected : ''].join(' ')} onClick={onClick} aria-pressed={selectedIndex !== undefined ? selected : undefined} aria-label={asset.alt_text || `이미지 ${asset.width}×${asset.height}`}>
      {asset.state === 'deleted' ? <span className={styles.cleaned}>정리된 이미지</span> : <img src={asset.thumb_url} alt="" loading="lazy" />}
      <span className={styles.tileBadge}>
        {asset.source === 'ai' && <Badge tone="info" icon={false}>AI</Badge>}
        {asset.usage_count > 0 && <Badge tone="success" icon={false}>사용 중</Badge>}
      </span>
      {selected && <span className={styles.check}>{selectedIndex! + 1}</span>}
    </button>
  );
}

// 이미지 상세: 미리보기·대체 텍스트·자르기·다운로드·삭제·사용처·정리 예정
export function MediaDetailSheet({ asset, onClose }: { asset: MediaAsset | null; onClose: () => void }) {
  const usage = useMediaUsage(asset?.id ?? null);
  const qc = useQueryClient();
  const toast = useToast();
  const [alt, setAlt] = useState('');
  const [lastId, setLastId] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  if (asset && asset.id !== lastId) {
    setLastId(asset.id);
    setAlt(asset.alt_text);
  }
  if (!asset) return null;

  async function act(fn: () => Promise<unknown>, done: string, close = false) {
    setBusy(true);
    try {
      await fn();
      await qc.invalidateQueries({ queryKey: ['media'] });
      toast.show(done, 'success');
      if (close) onClose();
    } catch (e) {
      toast.show(errorMessage(e), 'error');
    } finally {
      setBusy(false);
    }
  }

  const retention = asset.state === 'deleted'
    ? `보관 기간이 지나 ${asset.deleted_at ? formatDate(asset.deleted_at) : ''} 정리됐어요. 기록과 X 링크는 남아 있어요`
    : asset.cleanup_after
      ? `게시 완료 후 보관 중 · ${formatDate(asset.cleanup_after)} 이후 정리 실행 때 지워져요`
      : asset.last_published_at
        ? '게시가 끝나지 않은 묶음에서 쓰고 있어 보호 중이에요'
        : '아직 게시에 쓰이지 않아 자동으로 지우지 않아요';

  return (
    <Sheet open onClose={onClose} title="이미지" size="lg" dismissible={!busy}>
      <div className={own.detail}>
        <div className={own.preview}>
          {asset.state === 'deleted' ? <span className={styles.cleaned}>보관 기간이 지나 정리된 이미지</span> : <img src={asset.url} alt={asset.alt_text || '이미지'} />}
        </div>
        <div className={own.side}>
          <p className={own.meta}>
            {asset.width}×{asset.height} · {asset.content_type.replace('image/', '').toUpperCase()} · {formatBytes(asset.bytes)} ·{' '}
            {asset.source === 'ai' ? 'AI 생성' : asset.source === 'crop' ? '자른 이미지' : '직접 업로드'}
          </p>
          <p className={own.retention}>{retention}</p>
          {asset.state !== 'deleted' && (
            <>
              <TextArea label="대체 텍스트" rows={3} value={alt} onChange={(e) => setAlt(e.target.value)} hint="이미지를 설명하는 글이에요. 화면 낭독기 사용자에게 읽혀요" />
              <Button variant="secondary" size="sm" disabled={busy || alt === asset.alt_text} onClick={() => act(() => patch(`/api/media/${asset.id}`, { alt_text: alt }), '대체 텍스트를 저장했어요')}>
                대체 텍스트 저장
              </Button>
              <div>
                <p className={own.label}>
                  <Crop size={16} aria-hidden /> 가운데 기준으로 자르기 (새 이미지로 저장)
                </p>
                <div className={own.row}>
                  {['1:1', '4:5', '16:9'].map((a) => (
                    <Button key={a} size="sm" variant="secondary" disabled={busy} onClick={() => act(() => post(`/api/media/${asset.id}/crop`, { aspect: a }), `${a}로 자른 이미지를 만들었어요`)}>
                      {a}
                    </Button>
                  ))}
                </div>
              </div>
              <div className={own.row}>
                <a className={own.link} href={`/api/media/${asset.id}/download`} download>
                  <Download size={16} aria-hidden /> 원본 다운로드
                </a>
                <Button
                  size="sm"
                  variant="danger"
                  icon={<Trash2 size={16} />}
                  disabled={busy || asset.usage_count > 0}
                  onClick={() => act(() => del(`/api/media/${asset.id}`), '이미지를 지웠어요', true)}
                >
                  삭제
                </Button>
              </div>
              {asset.usage_count > 0 && <p className={own.muted}>글에서 쓰고 있어 지울 수 없어요.</p>}
            </>
          )}
          <div>
            <p className={own.label}>쓰인 글</p>
            {usage.data?.items.length ? (
              <ul className={own.usage}>
                {usage.data.items.map((u) => (
                  <li key={`${u.content_id}-${u.revision}-${u.part_index}`}>
                    <Link to={`/contents/${u.content_id}`} onClick={onClose}>
                      {u.title}
                    </Link>{' '}
                    <span className={own.muted}>
                      버전 {u.revision}
                      {u.current ? ' (편집 중)' : ''} · {u.part_index === 0 ? '본문' : `답글 ${u.part_index}`}
                    </span>
                  </li>
                ))}
              </ul>
            ) : (
              <p className={own.muted}>아직 쓰인 글이 없어요</p>
            )}
          </div>
        </div>
      </div>
    </Sheet>
  );
}
