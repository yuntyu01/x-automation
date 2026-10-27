import { ArrowLeft, ArrowRight, ImagePlus, X } from 'lucide-react';
import { useState } from 'react';
import { ApiError, errorMessage, get, put } from '../../api/client';
import type { MediaAsset } from '../../api/media';
import type { ContentDetail, ImageMode, PartMedia } from '../../api/types';
import { MediaDetailSheet } from '../../components/media/MediaParts';
import { MediaPicker } from '../../components/media/MediaPicker';
import { Button } from '../../components/ui/Button';
import { Card, CardTitle } from '../../components/ui/Card';
import { Select } from '../../components/ui/Field';
import { IconButton } from '../../components/ui/IconButton';
import { Segmented } from '../../components/ui/Segmented';
import { useToast } from '../../components/ui/Toast';
import type { DraftState } from './ContentEditorPage';
import { RegenerateCard } from './RegenerateCard';
import styles from './EditorExtras.module.css';

export interface ExtrasProps {
  content: ContentDetail;
  draft: DraftState;
  update: (p: Partial<DraftState>) => void;
  onChanged: (d: ContentDetail) => void;
  onReplaced: (d: ContentDetail) => void; // 원문까지 바뀐 경우 (재생성 결과 적용)
  flush: () => Promise<void>; // 진행 중인 자동 저장을 마친다 (버전 충돌 방지)
  editVersion: () => number;
}

// 편집 화면의 추가 영역: 이미지 배치, 다시 만들기 (예약 카드는 시간표 단계에서 붙는다)
export function EditorExtras(props: ExtrasProps) {
  return (
    <>
      <ImagesCard {...props} />
      <RegenerateCard content={props.content} onReplaced={props.onReplaced} flush={props.flush} editVersion={props.editVersion} />
    </>
  );
}

const MODE_LABEL: Record<ImageMode, string> = { none: '이미지 없음', upload: '직접 업로드', ai: 'AI 생성' };

function ImagesCard({ content, draft, update, onChanged, flush, editVersion }: ExtrasProps) {
  const toast = useToast();
  const [pickFor, setPickFor] = useState<number | null>(null);
  const [detail, setDetail] = useState<MediaAsset | null>(null);
  const [busy, setBusy] = useState(false);
  const parts = content.revision.parts;
  const media = content.revision.media;
  const byPart = (i: number) => media.filter((m) => m.part_index === i).sort((a, b) => a.position - b.position);

  async function setPart(index: number, ids: string[]) {
    setBusy(true);
    try {
      await flush();
      const d = await put<ContentDetail>(`/api/contents/${content.id}/parts/${index}/media`, { edit_version: editVersion(), media_ids: ids, image_mode: '' });
      onChanged(d);
      return true;
    } catch (e) {
      toast.show(e instanceof ApiError && e.status === 409 ? '다른 곳에서 먼저 바뀌었어요. 새로고침 후 다시 해 주세요' : errorMessage(e), 'error');
      return false;
    } finally {
      setBusy(false);
    }
  }

  async function moveToPart(m: PartMedia, target: number) {
    const from = byPart(m.part_index).map((x) => x.media_id).filter((id) => id !== m.media_id);
    const to = [...byPart(target).map((x) => x.media_id), m.media_id];
    if (to.length > 4) {
      toast.show('옮길 부분에 이미지가 이미 4장이에요', 'error');
      return;
    }
    if (await setPart(m.part_index, from)) await setPart(target, to);
  }

  async function openDetail(id: string) {
    try {
      setDetail(await get<MediaAsset>(`/api/media/${id}`));
    } catch (e) {
      toast.show(errorMessage(e), 'error');
    }
  }

  return (
    <Card>
      <CardTitle>이미지</CardTitle>
      <div className={styles.stack}>
        <Segmented label="이미지 방식" value={draft.image_mode} onChange={(m) => update({ image_mode: m })} options={(['none', 'upload', 'ai'] as ImageMode[]).map((m) => ({ value: m, label: MODE_LABEL[m] }))} />
        {draft.image_mode === 'none' && <p className={styles.muted}>텍스트만 게시해요. 붙어 있던 이미지는 게시하지 않아요.</p>}
        {draft.image_mode !== 'none' &&
          parts.map((_, i) => {
            const list = byPart(i);
            return (
              <div key={i} className={styles.part}>
                <div className={styles.partHead}>
                  <strong>{i === 0 ? '본문' : `답글 ${i}`}</strong>
                  <span className={styles.muted}>{list.length}/4장</span>
                  <Button size="sm" variant="ghost" icon={<ImagePlus size={16} />} disabled={busy || list.length >= 4} onClick={() => setPickFor(i)}>
                    추가
                  </Button>
                </div>
                {list.length > 0 && (
                  <ul className={styles.thumbs}>
                    {list.map((m, j) => (
                      <li key={m.media_id} className={styles.thumb}>
                        <button type="button" className={styles.thumbBtn} onClick={() => openDetail(m.media_id)} aria-label={`이미지 ${j + 1} 자세히 (대체 텍스트·자르기)`}>
                          {m.state === 'deleted' ? <span className={styles.cleaned}>정리됨</span> : <img src={m.thumb_url} alt={m.alt_text || ''} />}
                        </button>
                        <div className={styles.thumbActions}>
                          <IconButton label={`이미지 ${j + 1} 순서 앞으로`} disabled={busy || j === 0} onClick={() => setPart(i, swap(list.map((x) => x.media_id), j, j - 1))}>
                            <ArrowLeft size={16} />
                          </IconButton>
                          <IconButton label={`이미지 ${j + 1} 순서 뒤로`} disabled={busy || j === list.length - 1} onClick={() => setPart(i, swap(list.map((x) => x.media_id), j, j + 1))}>
                            <ArrowRight size={16} />
                          </IconButton>
                          <IconButton label={`이미지 ${j + 1} 빼기`} disabled={busy} onClick={() => setPart(i, list.filter((x) => x.media_id !== m.media_id).map((x) => x.media_id))}>
                            <X size={16} />
                          </IconButton>
                        </div>
                        {parts.length > 1 && (
                          <Select
                            label={`이미지 ${j + 1} 옮길 부분`}
                            value={String(i)}
                            disabled={busy}
                            onChange={(e) => moveToPart(m, Number(e.target.value))}
                            options={parts.map((_, k) => ({ value: String(k), label: k === 0 ? '본문' : `답글 ${k}` }))}
                          />
                        )}
                      </li>
                    ))}
                  </ul>
                )}
              </div>
            );
          })}
        {draft.image_mode === 'ai' && <p className={styles.muted}>AI 이미지는 아래 &lsquo;다시 만들기 → 이미지&rsquo;로 만들거나, 보관함의 생성 이미지를 붙여요.</p>}
        {media.some((m) => m.part_index >= parts.length) && (
          <p className={styles.problem}>
            분할이 바뀌어 사라진 부분에 이미지가 남아 있어요.{' '}
            <Button
              size="sm"
              variant="secondary"
              onClick={async () => {
                for (const idx of [...new Set(media.filter((m) => m.part_index >= parts.length).map((m) => m.part_index))]) await setPart(idx, []);
              }}
            >
              정리
            </Button>
          </p>
        )}
      </div>
      <MediaPicker
        open={pickFor !== null}
        onClose={() => setPickFor(null)}
        max={pickFor === null ? 4 : 4 - byPart(pickFor).length}
        title={pickFor === 0 ? '본문에 이미지 붙이기' : `답글 ${pickFor}에 이미지 붙이기`}
        onPick={async (ids) => {
          const i = pickFor!;
          setPickFor(null);
          if (await setPart(i, [...byPart(i).map((x) => x.media_id), ...ids])) toast.show(`${ids.length}장을 붙였어요`, 'success');
        }}
      />
      <MediaDetailSheet asset={detail} onClose={() => setDetail(null)} />
    </Card>
  );
}

function swap<T>(arr: T[], a: number, b: number): T[] {
  const n = [...arr];
  [n[a], n[b]] = [n[b]!, n[a]!];
  return n;
}
