import { ArrowLeft, ArrowRight, CalendarPlus, ImagePlus, Send, X } from 'lucide-react';
import { useQueryClient } from '@tanstack/react-query';
import { Link } from 'react-router';
import { useState } from 'react';
import { ApiError, apiUrl, errorMessage, get, put } from '../../api/client';
import { placeContents } from '../../api/schedule';
import type { MediaAsset } from '../../api/media';
import type { ContentDetail, ImageMode, PartMedia } from '../../api/types';
import { MediaDetailSheet } from '../../components/media/MediaParts';
import { MediaPicker } from '../../components/media/MediaPicker';
import { PostNowSheet } from '../../components/publish/PostNowSheet';
import { Button } from '../../components/ui/Button';
import { Card, CardTitle } from '../../components/ui/Card';
import { Select } from '../../components/ui/Field';
import { IconButton } from '../../components/ui/IconButton';
import { Segmented } from '../../components/ui/Segmented';
import { useToast } from '../../components/ui/Toast';
import { formatDateTime, localDate } from '../../lib/format';
import type { DraftState } from './ContentEditorPage';
import { RegenerateCard } from './RegenerateCard';
import styles from './EditorExtras.module.css';

export interface ExtrasProps {
  content: ContentDetail;
  draft: DraftState;
  update: (p: Partial<DraftState>) => void;
  onChanged: (d: ContentDetail) => void;
  onReplaced: (d: ContentDetail) => void; // 원문까지 바뀐 경우 (재생성 결과 적용)
  flush: () => Promise<void>; // 진행 중인 자동 저장을 마친다 (버전 충돌 방지). 저장하지 못했으면 던진다
  editVersion: () => number;
}

// 편집 화면의 추가 영역: 예약, 이미지 배치, 다시 만들기
export function EditorExtras(props: ExtrasProps) {
  return (
    <>
      <ScheduleCard content={props.content} onChanged={props.onChanged} flush={props.flush} />
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
                          {m.state === 'deleted' ? <span className={styles.cleaned}>정리됨</span> : <img src={apiUrl(m.thumb_url)} alt={m.alt_text || ''} />}
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

// 예약 카드: 이 글의 예약 시각, 또는 다음 빈 예약에 넣기 ("지금 게시"와 다르다)
function ScheduleCard({ content, onChanged, flush }: { content: ContentDetail; onChanged: (d: ContentDetail) => void; flush: () => Promise<void> }) {
  const toast = useToast();
  const qc = useQueryClient();
  const [busy, setBusy] = useState(false);
  const [postNow, setPostNow] = useState(false);
  // 서버는 저장된 글을 게시한다. 방금 입력한 내용이 아직 저장 전이면 먼저 저장해야 화면과 같은 글이 올라간다
  async function openPostNow() {
    setBusy(true);
    try {
      await flush();
      setPostNow(true);
    } catch (e) {
      toast.show(errorMessage(e), 'error');
    } finally {
      setBusy(false);
    }
  }
  if (content.archived_at) return null;
  const inFlight = content.last_run_state === 'pending' || content.last_run_state === 'running';
  const needsCheck = content.last_run_state === 'partial' || content.last_run_state === 'unknown' || content.last_run_state === 'failed';
  async function place() {
    setBusy(true);
    try {
      const r = await placeContents([content.id]);
      if (r.placed[0]) toast.show(`${formatDateTime(r.placed[0].slot_at)} 예약에 넣었어요`, 'success');
      else toast.show(Object.values(r.skipped)[0] ?? '빈 예약이 없어요. 시간표에서 시간을 지정해 넣을 수 있어요', 'info');
      void qc.invalidateQueries({ queryKey: ['schedule'] });
      void qc.invalidateQueries({ queryKey: ['home'] });
      onChanged(await get<ContentDetail>(`/api/contents/${content.id}`));
    } catch (e) {
      toast.show(errorMessage(e), 'error');
    } finally {
      setBusy(false);
    }
  }
  return (
    <Card>
      <CardTitle action={content.reservation_at ? <Link className={styles.link} to={`/schedule?date=${localDate(new Date(content.reservation_at))}`}>예약 보기</Link> : undefined}>
        예약
      </CardTitle>
      <div className={styles.stack}>
        {inFlight || needsCheck ? (
          <p className={styles.muted}>
            {inFlight ? '지금 게시 중이에요.' : '지난 게시에 확인이 필요한 부분이 있어요.'} <Link className={styles.link} to="/publish">게시 화면</Link>에서 진행과 결과를 볼 수 있어요.
          </p>
        ) : content.reservation_at ? (
          <p className={styles.muted}>
            <strong>{formatDateTime(content.reservation_at)}</strong>에 게시 예정이에요. 시간·순서는 예약 화면에서 바꿔요.
          </p>
        ) : (
          <>
            <p className={styles.muted}>아직 예약되지 않았어요. 준비 완료된 글은 빈 예약에 자동으로 들어가요.</p>
            <Button variant="secondary" icon={<CalendarPlus size={16} />} busy={busy} onClick={place}>
              다음 빈 예약에 넣기
            </Button>
          </>
        )}
        {!inFlight && !needsCheck && (
          <Button variant="soft" icon={<Send size={16} />} disabled={!content.revision.publishable} busy={busy} onClick={openPostNow}>
            지금 게시
          </Button>
        )}
      </div>
      <PostNowSheet
        open={postNow}
        onClose={() => {
          setPostNow(false);
          void get<ContentDetail>(`/api/contents/${content.id}`).then(onChanged, () => undefined);
        }}
        contentId={content.id}
        parts={content.revision.parts.length}
      />
    </Card>
  );
}
