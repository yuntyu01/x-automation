import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Archive, ArrowDown, ArrowUp, BarChart2, Check, Copy, CopyPlus, Heart, History, MessageCircle, MoreHorizontal, Plus, Repeat2, RotateCcw, Scissors, Share, Trash2 } from 'lucide-react';
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useBlocker, useNavigate, useParams } from 'react-router';
import { ApiError, apiUrl, errorMessage, get, post, put } from '../../api/client';
import { useBulk, useCategories, useContent, useRevisions } from '../../api/contents';
import { useIntegrations, useSettings } from '../../api/queries';
import { SPLIT_LABEL, type ContentDetail, type ImageMode, type SplitMode, type SplitOptions, type SplitResult, type ThreadPart } from '../../api/types';
import { PageHeader } from '../../components/layout/AppShell';
import { StageBadge } from '../../components/StageBadge';
import { Badge } from '../../components/ui/Badge';
import { Button } from '../../components/ui/Button';
import { Card, CardTitle } from '../../components/ui/Card';
import { TextArea } from '../../components/ui/Field';
import { IconButton } from '../../components/ui/IconButton';
import { Row, RowGroup } from '../../components/ui/Row';
import { Segmented } from '../../components/ui/Segmented';
import { Sheet } from '../../components/ui/Sheet';
import { ErrorState, LoadingList } from '../../components/ui/States';
import { Switch } from '../../components/ui/Switch';
import { TextField } from '../../components/ui/TextField';
import { useToast } from '../../components/ui/Toast';
import { formatDateTime, formatMoney, postCostMicros } from '../../lib/format';
import { copyText, useDebounced } from '../../lib/hooks';
import { hasUrl, joinManual, measure, splitManual } from '../../lib/thread';
import { BackButton } from '../settings/ConnectionsPage';
import { EditorExtras } from './EditorExtras';
import styles from './ContentEditorPage.module.css';

export interface DraftState {
  title: string;
  category: string;
  tags: string;
  raw_text: string;
  split_mode: SplitMode;
  split_options: SplitOptions;
  image_mode: ImageMode;
}

type SaveStatus = 'saved' | 'dirty' | 'saving' | 'error' | 'conflict';

const RETRY_MIN_MS = 5_000;
const RETRY_MAX_MS = 60_000;

function toDraft(d: ContentDetail): DraftState {
  return {
    title: d.title,
    category: d.category,
    tags: d.tags.join(', '),
    raw_text: d.revision.raw_text,
    split_mode: d.revision.split_mode,
    split_options: d.revision.split_options ?? { numbering: false, max_replies: 0 },
    image_mode: d.revision.image_mode,
  };
}

function sameDraft(a: DraftState, b: DraftState) {
  return JSON.stringify(a) === JSON.stringify(b);
}

export function ContentEditorPage() {
  const { id } = useParams();
  const q = useContent(id);
  // 캐시에 남은 예전 내용으로 편집을 시작하지 않는다 (다른 곳에서 바뀐 뒤면 첫 저장부터 충돌난다).
  // 한 번 받은 뒤에는 다시 받기가 실패해도 편집 화면과 저장 안 된 내용을 그대로 둔다
  const ready = !!q.data && q.isFetchedAfterMount;
  if (!ready && q.isFetching)
    return (
      <>
        <PageHeader title="편집" back={<BackButton to="/contents" />} />
        <LoadingList rows={3} />
      </>
    );
  if (!q.data)
    return (
      <>
        <PageHeader title="편집" back={<BackButton to="/contents" />} />
        <ErrorState error={q.error} onRetry={() => q.refetch()} />
      </>
    );
  return <Editor key={q.data.id} initial={q.data} />;
}

function Editor({ initial }: { initial: ContentDetail }) {
  const qc = useQueryClient();
  const toast = useToast();
  const nav = useNavigate();
  const settings = useSettings();
  const integrations = useIntegrations();
  const cats = useCategories();
  const bulk = useBulk();

  const [server, setServer] = useState<ContentDetail>(initial);
  const [draft, setDraft] = useState<DraftState>(() => toDraft(initial));
  const [status, setStatus] = useState<SaveStatus>('saved');
  const [conflict, setConflict] = useState<ContentDetail | null>(null);
  const [sheet, setSheet] = useState<'more' | 'history' | 'delete' | null>(null);
  // 마지막으로 서버에 저장된 내용 (화면 표시용 상태 + 비동기 저장에서 읽는 ref)
  const [saved, setSaved] = useState<DraftState>(() => toDraft(initial));
  const savedDraft = useRef<DraftState>(saved);
  const markSaved = useCallback((d: DraftState) => {
    savedDraft.current = d;
    setSaved(d);
  }, []);
  const editVersion = useRef(initial.edit_version);
  // 승인할 revision: 저장으로 새 revision이 생길 수 있어 렌더 때 값이 아니라 마지막 서버 응답을 따른다
  const currentRevision = useRef(initial.current_revision_id);
  const saving = useRef(false);
  const savePromise = useRef<Promise<boolean> | null>(null);
  // 저장에 실패한 내용과 다음 자동 재시도까지 기다릴 시간. 같은 내용을 쉬지 않고 다시 보내지 않는다
  const failedDraft = useRef<DraftState | null>(null);
  const retryDelay = useRef(RETRY_MIN_MS);
  const [retryTick, setRetryTick] = useState(0);
  const draftRef = useRef(draft);
  useEffect(() => {
    draftRef.current = draft;
  }, [draft]);

  const update = (patch: Partial<DraftState>) => setDraft((d) => ({ ...d, ...patch }));
  const dirty = !sameDraft(draft, saved);

  const apply = useCallback(
    (d: ContentDetail) => {
      setServer(d);
      editVersion.current = d.edit_version;
      currentRevision.current = d.current_revision_id;
      qc.setQueryData(['contents', 'detail', d.id], d);
      void qc.invalidateQueries({ queryKey: ['contents', 'list'] });
    },
    [qc],
  );

  // 서버에서 원문이 바뀐 경우(이전 버전 복원·재생성 결과 적용) 편집 중 상태를 서버 값으로 맞춘다
  const replaceAll = useCallback(
    (d: ContentDetail) => {
      const nd = toDraft(d);
      markSaved(nd);
      setDraft(nd);
      apply(d);
    },
    [apply, markSaved],
  );

  // 저장했으면 true. 충돌·실패면 false (화면에 상태를 보여 주고, 이어지는 동작은 멈춰야 한다)
  const save = useCallback(
    async (d: DraftState, force?: number): Promise<boolean> => {
      if (saving.current) return savePromise.current ?? false;
      saving.current = true;
      let done: (ok: boolean) => void = () => {};
      savePromise.current = new Promise<boolean>((r) => (done = r));
      let ok = false;
      setStatus('saving');
      try {
        const res = await put<ContentDetail>(`/api/contents/${server.id}`, {
          edit_version: force ?? editVersion.current,
          title: d.title,
          category: d.category,
          tags: d.tags.split(',').map((t) => t.trim()).filter(Boolean),
          raw_text: d.raw_text,
          split_mode: d.split_mode,
          split_options: d.split_options,
          image_mode: d.image_mode,
        });
        markSaved(d);
        apply(res);
        failedDraft.current = null;
        retryDelay.current = RETRY_MIN_MS;
        setStatus('saved');
        setConflict(null);
        ok = true;
      } catch (e) {
        if (e instanceof ApiError && e.status === 409) {
          setConflict((e.body as { current?: ContentDetail })?.current ?? null);
          setStatus('conflict');
        } else {
          failedDraft.current = d;
          setStatus('error');
          if (!(e instanceof ApiError && e.code === 'network')) toast.show(errorMessage(e), 'error');
        }
      } finally {
        saving.current = false;
        done(ok);
      }
      return ok;
    },
    [server.id, apply, toast, markSaved],
  );

  // 다른 저장(이미지 연결·승인·재생성) 전에 진행 중·대기 중인 자동 저장을 마친다.
  // 저장하지 못했으면 던진다: 화면과 다른 서버 내용으로 이어서 진행하지 않게
  const flush = useCallback(async () => {
    if (saving.current && savePromise.current) await savePromise.current;
    if (!sameDraft(draftRef.current, savedDraft.current) && !(await save(draftRef.current))) {
      throw new Error('바뀐 내용을 저장하지 못했어요. 저장 상태를 확인한 뒤 다시 해 주세요');
    }
  }, [save]);

  // 자동 저장: 입력이 멈추고 0.9초 뒤. 실패한 내용은 새로 고치거나 재시도 시각이 될 때까지 다시 보내지 않는다.
  // 기다리는 동안 편집 내용이 바뀌었으면(충돌 해결로 서버 내용을 불러옴·승인 전 저장 등) 지난 값을 다시 보내지 않는다
  const debounced = useDebounced(draft, 900);
  useEffect(() => {
    if (status === 'conflict') return;
    if (!sameDraft(debounced, draftRef.current)) return;
    if (status === 'error' && failedDraft.current && sameDraft(debounced, failedDraft.current)) return;
    if (!sameDraft(debounced, savedDraft.current) && !saving.current) void save(debounced);
  }, [debounced, save, status, retryTick]);

  // 저장 실패 뒤: 5초부터 두 배씩(최대 1분) 기다렸다가, 또는 인터넷이 다시 연결되면 바로 다시 시도한다
  useEffect(() => {
    if (status !== 'error') return;
    const retry = () => {
      failedDraft.current = null;
      setRetryTick((n) => n + 1);
    };
    const timer = window.setTimeout(retry, retryDelay.current);
    retryDelay.current = Math.min(retryDelay.current * 2, RETRY_MAX_MS);
    window.addEventListener('online', retry);
    return () => {
      window.clearTimeout(timer);
      window.removeEventListener('online', retry);
    };
  }, [status]);
  const shownStatus: SaveStatus = status === 'saved' && dirty ? 'dirty' : status;

  // 저장 전 이탈 경고
  useEffect(() => {
    const fn = (e: BeforeUnloadEvent) => {
      if (dirty || status === 'saving' || status === 'error') e.preventDefault();
    };
    window.addEventListener('beforeunload', fn);
    return () => window.removeEventListener('beforeunload', fn);
  }, [dirty, status]);
  const blocker = useBlocker(({ currentLocation, nextLocation }) => (dirty || status === 'error') && currentLocation.pathname !== nextLocation.pathname);

  // 입력 중 즉시 미리보기 (서버 분할 규칙)
  // 객체를 렌더마다 새로 만들면 지연 타이머가 계속 다시 걸려 250ms마다 다시 그린다
  const previewSource = useMemo(
    () => ({ raw: draft.raw_text, mode: draft.split_mode, opts: draft.split_options }),
    [draft.raw_text, draft.split_mode, draft.split_options],
  );
  const previewInput = useDebounced(previewSource, 250);
  const preview = useQuery({
    queryKey: ['split-preview', previewInput],
    queryFn: () => post<{ result: SplitResult }>('/api/contents/split-preview', { raw_text: previewInput.raw, split_mode: previewInput.mode, split_options: previewInput.opts }),
    placeholderData: (p) => p,
  });
  const result: SplitResult | undefined = preview.data?.result;
  const parts: ThreadPart[] = result?.parts ?? server.revision.parts;
  const limit = result?.limit ?? server.revision.validation.limit ?? 280;
  const cost = settings.data ? postCostMicros(parts.map((p) => p.text), settings.data.policy.pricing, hasUrl) : null;

  async function toManual() {
    try {
      const r = await post<{ manual_text: string }>('/api/contents/split-preview', {
        raw_text: draft.raw_text,
        split_mode: draft.split_mode,
        split_options: draft.split_options,
        to_manual: true,
      });
      update({ raw_text: r.manual_text, split_mode: 'manual' });
      toast.show('지금 나뉜 위치로 직접 나누기를 시작해요. 구분선(---)을 옮겨 고칠 수 있어요', 'info');
    } catch (e) {
      toast.show(errorMessage(e), 'error');
    }
  }

  async function review(action: 'approve' | 'reject') {
    try {
      await flush();
      const d = await post<ContentDetail>(`/api/contents/${server.id}/${action}`, { revision_id: currentRevision.current });
      apply(d);
      toast.show(action === 'approve' ? `버전 ${d.revision.number}을 승인했어요` : '반려했어요', 'success');
    } catch (e) {
      toast.show(errorMessage(e), 'error');
    }
  }

  async function duplicate() {
    try {
      const d = await post<ContentDetail>(`/api/contents/${server.id}/duplicate`);
      await qc.invalidateQueries({ queryKey: ['contents', 'list'] });
      toast.show('복제했어요', 'success');
      setSheet(null);
      nav(`/contents/${d.id}`);
    } catch (e) {
      toast.show(errorMessage(e), 'error');
    }
  }

  async function bulkAction(action: 'archive' | 'unarchive' | 'delete') {
    try {
      const r = await bulk.mutateAsync({ action, ids: [server.id] });
      const skipped = Object.values(r.skipped)[0];
      if (skipped) {
        toast.show(skipped, 'error');
        return;
      }
      setSheet(null);
      if (action === 'delete') {
        markSaved(draft);
        toast.show('삭제했어요', 'success');
        nav('/contents', { replace: true });
      } else {
        toast.show(action === 'archive' ? '보관했어요' : '보관을 해제했어요', 'success');
        apply(await get<ContentDetail>(`/api/contents/${server.id}`));
      }
    } catch (e) {
      toast.show(errorMessage(e), 'error');
    }
  }

  const mode = settings.data?.operation_mode;
  const approvedOther = server.approved_number != null && server.approved_revision_id !== server.current_revision_id;
  const isApproved = server.approved_revision_id === server.current_revision_id;
  const xName = integrations.data?.x.connection.username;

  return (
    <div className={styles.page}>
      <PageHeader
        title={draft.title || '제목 없음'}
        subtitle={
          <span className={styles.subline}>
            <StageBadge stage={server.stage} />
            <SaveIndicator status={shownStatus} onRetry={() => save(draft)} />
          </span>
        }
        back={<BackButton to="/contents" />}
        actions={
          <IconButton label="더 보기" onClick={() => setSheet('more')}>
            <MoreHorizontal size={22} />
          </IconButton>
        }
      />

      {status === 'conflict' && (
        <Card className={styles.conflict} role="alert">
          <CardTitle>다른 곳에서 먼저 바뀌었어요</CardTitle>
          <p className={styles.muted}>다른 탭이나 기기에서 이 글을 저장했어요. 어떤 내용을 남길지 골라 주세요.</p>
          <div className={styles.row}>
            <Button
              variant="secondary"
              onClick={() => {
                if (!conflict) return;
                const d = toDraft(conflict);
                markSaved(d);
                setDraft(d);
                apply(conflict);
                setStatus('saved');
                setConflict(null);
              }}
              disabled={!conflict}
            >
              저장된 내용 불러오기
            </Button>
            <Button onClick={() => conflict && save(draft, conflict.edit_version)} disabled={!conflict}>
              지금 내용으로 덮어쓰기
            </Button>
          </div>
        </Card>
      )}

      <div className={styles.grid}>
        <div className={styles.col}>
          <Card>
            <div className={styles.form}>
              <TextField label="관리용 제목" value={draft.title} onChange={(e) => update({ title: e.target.value })} maxLength={120} />
              <div className={styles.twoCol}>
                <TextField label="카테고리" value={draft.category} onChange={(e) => update({ category: e.target.value })} list="editor-cats" maxLength={40} />
                <TextField label="태그 (쉼표로 구분)" value={draft.tags} onChange={(e) => update({ tags: e.target.value })} />
              </div>
              <datalist id="editor-cats">{cats.data?.items.map((c) => <option key={c} value={c} />)}</datalist>
            </div>
          </Card>

          <Card>
            <CardTitle action={<span className={styles.count}>전체 {measure(draft.raw_text).weighted}자</span>}>원문</CardTitle>
            {draft.split_mode === 'manual' ? (
              <ManualParts raw={draft.raw_text} limit={limit} onChange={(raw) => update({ raw_text: raw })} />
            ) : (
              <TextArea label="원문" hideLabel className={styles.raw} rows={12} value={draft.raw_text} onChange={(e) => update({ raw_text: e.target.value })} hint="원문은 그대로 두고, 게시할 때만 공백·줄바꿈을 정리해 나눠요." />
            )}
          </Card>

          <Card>
            <CardTitle>나누기</CardTitle>
            <div className={styles.form}>
              <Segmented
                label="나누는 방식"
                value={draft.split_mode}
                onChange={(m) => update({ split_mode: m })}
                options={(['two_parts', 'overflow', 'manual'] as SplitMode[]).map((m) => ({ value: m, label: SPLIT_LABEL[m] }))}
              />
              {draft.split_mode !== 'manual' && (
                <Button variant="ghost" size="sm" icon={<Scissors size={16} />} onClick={toManual} className={styles.left}>
                  나뉜 위치 직접 고치기
                </Button>
              )}
              <RowGroup>
                <Row
                  label="번호 표기 (1/N)"
                  description="끝에 (1/3) 같은 번호를 붙여요. 번호도 글자 수에 포함돼요"
                  trailing={<Switch label="번호 표기" checked={draft.split_options.numbering} onChange={(v) => update({ split_options: { ...draft.split_options, numbering: v } })} />}
                />
                <Row
                  label="최대 답글 수"
                  description="넘으면 게시하지 않고 수정 대기로 둬요 (0은 제한 없음)"
                  trailing={
                    <input
                      className={styles.num}
                      type="number"
                      inputMode="numeric"
                      min={0}
                      max={25}
                      aria-label="최대 답글 수"
                      value={draft.split_options.max_replies}
                      onChange={(e) => update({ split_options: { ...draft.split_options, max_replies: Math.max(0, Math.min(25, Number(e.target.value) || 0)) } })}
                    />
                  }
                />
              </RowGroup>
            </div>
          </Card>

          <EditorExtras content={server} draft={draft} update={update} onChanged={apply} onReplaced={replaceAll} flush={flush} editVersion={() => editVersion.current} />
        </div>

        <div className={styles.col}>
          <Card>
            <CardTitle
              action={
                <Button
                  variant="ghost"
                  size="sm"
                  icon={<Copy size={16} />}
                  onClick={async () => {
                    const ok = await copyText(parts.map((p) => p.text).join('\n\n'));
                    toast.show(ok ? '전체를 복사했어요' : '복사하지 못했어요', ok ? 'success' : 'error');
                  }}
                >
                  전체 복사
                </Button>
              }
            >
              타래 미리보기
            </CardTitle>
            {result && (
              <p className={[styles.note, result.publishable ? '' : styles.noteWarn].join(' ')} aria-live="polite">
                <strong>{result.note}</strong> · X 게시물 {result.post_count}개
                {cost !== null && <span> · 예상 게시 비용 {formatMoney(cost, settings.data?.policy.pricing.currency || 'USD')} (추정)</span>}
                {result.has_url && <span className={styles.urlWarn}> · 링크 포함 게시는 X 단가가 높아요</span>}
              </p>
            )}
            {(result?.problems ?? server.revision.validation.problems).map((p) => (
              <p key={p} className={styles.problem} role="alert">
                {p}
              </p>
            ))}
            {draft.title && <p className={styles.previewTitle}><span>제목</span>{draft.title}</p>}
            <ThreadPreview parts={parts} limit={limit} account={xName} media={server.revision.media} />
          </Card>

          <Card>
            <CardTitle>검토와 승인</CardTitle>
            <div className={styles.review}>
              <p>
                {isApproved ? (
                  <Badge tone="success">버전 {server.revision.number} 승인됨</Badge>
                ) : server.revision.review_state === 'rejected' ? (
                  <Badge tone="danger">반려됨</Badge>
                ) : (
                  <Badge tone="warning">버전 {server.revision.number} 미승인</Badge>
                )}
              </p>
              {approvedOther && (
                <p className={styles.muted}>
                  이전 버전 {server.approved_number}이(가) 승인되어 있어요. 지금 버전 {server.revision.number}은 승인 전이라
                  {server.reservation_id ? ' 예약은 승인된 버전으로 게시돼요.' : ' 자동 게시에 쓰이지 않아요.'}
                </p>
              )}
              {mode === 'review' ? (
                <p className={styles.muted}>검토 후 게시 모드에서는 승인한 버전만 자동으로 올라가요.</p>
              ) : (
                <p className={styles.muted}>지금 운영 모드에서는 승인 없이도 준비 완료된 글을 게시해요. 승인하면 그 버전으로 고정돼요.</p>
              )}
              <div className={styles.row}>
                {!isApproved && (
                  <Button icon={<Check size={18} />} onClick={() => review('approve')} disabled={!(result?.publishable ?? server.revision.publishable) || status === 'saving'}>
                    버전 {server.revision.number} 승인
                  </Button>
                )}
                {!isApproved && server.revision.review_state !== 'rejected' && mode === 'review' && (
                  <Button variant="secondary" onClick={() => review('reject')}>
                    반려
                  </Button>
                )}
              </div>
            </div>
          </Card>
        </div>
      </div>

      <Sheet open={sheet === 'more'} onClose={() => setSheet(null)} title="이 글">
        <RowGroup>
          <Row icon={<History size={20} />} label="이전 버전" value={`${server.revision_count}개`} onClick={() => setSheet('history')} />
          <Row icon={<CopyPlus size={20} />} label="복제" onClick={duplicate} />
          {server.archived_at ? (
            <Row icon={<Archive size={20} />} label="보관 해제" onClick={() => bulkAction('unarchive')} />
          ) : (
            <Row icon={<Archive size={20} />} label="보관" description="목록에서 숨겨요. 데이터는 남아요" onClick={() => bulkAction('archive')} />
          )}
          <Row icon={<Trash2 size={20} />} label="삭제" tone="danger" onClick={() => setSheet('delete')} />
        </RowGroup>
      </Sheet>
      <HistorySheet
        open={sheet === 'history'}
        onClose={() => setSheet(null)}
        content={server}
        onRestored={(d) => {
          replaceAll(d);
          setSheet(null);
        }}
      />
      <Sheet
        open={sheet === 'delete'}
        onClose={() => setSheet(null)}
        title="이 글을 삭제할까요?"
        description="여기 기록만 지워요. X 게시물은 그대로예요. 예약·게시 기록이 있으면 삭제할 수 없어요."
        footer={
          <>
            <Button variant="secondary" onClick={() => setSheet(null)}>
              취소
            </Button>
            <Button variant="danger" onClick={() => bulkAction('delete')} busy={bulk.isPending}>
              삭제
            </Button>
          </>
        }
      >
        <p className={styles.muted}>되돌릴 수 없어요.</p>
      </Sheet>
      <Sheet
        open={blocker.state === 'blocked'}
        onClose={() => blocker.reset?.()}
        title="저장하지 않은 변경이 있어요"
        description={status === 'error' ? '마지막 저장에 실패했어요. 나가면 바뀐 내용이 사라져요.' : '잠시 후 자동 저장돼요. 지금 나가면 마지막 변경이 사라질 수 있어요.'}
        footer={
          <>
            <Button variant="secondary" onClick={() => blocker.proceed?.()}>
              나가기
            </Button>
            <Button
              onClick={async () => {
                await save(draft);
                blocker.reset?.();
              }}
            >
              저장하고 머물기
            </Button>
          </>
        }
      >
        <p className={styles.muted}>저장 상태: {status === 'error' ? '저장 실패' : '저장 대기'}</p>
      </Sheet>
    </div>
  );
}

function SaveIndicator({ status, onRetry }: { status: SaveStatus; onRetry: () => void }) {
  const label = { saved: '저장됨', dirty: '저장 대기', saving: '저장 중…', error: '저장 실패', conflict: '충돌' }[status];
  return (
    <span className={[styles.save, status === 'error' || status === 'conflict' ? styles.saveBad : ''].join(' ')} role="status" aria-live="polite">
      {label}
      {status === 'error' && (
        <button type="button" className={styles.retry} onClick={onRetry}>
          다시 시도
        </button>
      )}
    </span>
  );
}

function ManualParts({ raw, limit, onChange }: { raw: string; limit: number; onChange: (raw: string) => void }) {
  const parts = useMemo(() => splitManual(raw), [raw]);
  const set = (next: string[]) => onChange(joinManual(next));
  return (
    <div className={styles.manual}>
      {parts.map((p, i) => {
        const m = measure(p.trim(), limit); // 게시할 때처럼 앞뒤 공백은 빼고 센다
        return (
          <div key={i} className={styles.manualPart}>
            <div className={styles.manualHead}>
              <span className={styles.partLabel}>{i === 0 ? '본문' : `답글 ${i}`}</span>
              <span className={[styles.len, m.valid ? '' : styles.lenBad].join(' ')}>
                {m.weighted}/{limit}
              </span>
              <IconButton label={`${i + 1}번째 부분 위로`} disabled={i === 0} onClick={() => set(swap(parts, i, i - 1))}>
                <ArrowUp size={18} />
              </IconButton>
              <IconButton label={`${i + 1}번째 부분 아래로`} disabled={i === parts.length - 1} onClick={() => set(swap(parts, i, i + 1))}>
                <ArrowDown size={18} />
              </IconButton>
              <IconButton label={`${i + 1}번째 부분 삭제`} disabled={parts.length === 1} onClick={() => set(parts.filter((_, j) => j !== i))}>
                <Trash2 size={18} />
              </IconButton>
            </div>
            <textarea
              className={styles.partArea}
              aria-label={i === 0 ? '본문' : `답글 ${i}`}
              value={p}
              rows={Math.min(10, Math.max(3, p.split('\n').length + 1))}
              onChange={(e) => set(parts.map((x, j) => (j === i ? e.target.value : x)))}
            />
            {!m.valid && p.trim() && <p className={styles.problem}>{m.weighted}자로 한도 {limit}자를 넘어요</p>}
          </div>
        );
      })}
      <Button variant="secondary" icon={<Plus size={18} />} onClick={() => set([...parts, ''])}>
        답글 추가
      </Button>
    </div>
  );
}

function swap<T>(arr: T[], a: number, b: number): T[] {
  const n = [...arr];
  [n[a], n[b]] = [n[b]!, n[a]!];
  return n;
}

export function ThreadPreview({ parts, limit, account, media }: { parts: ThreadPart[]; limit: number; account?: string; media: ContentDetail['revision']['media'] }) {
  const toast = useToast();
  if (!parts.length) return <p className={styles.muted}>원문을 입력하면 여기에 게시될 모습이 보여요.</p>;
  return (
    <ol className={styles.thread} aria-label="게시될 타래">
      {parts.map((p, i) => {
        const imgs = media.filter((m) => m.part_index === i);
        return (
          <li key={i} className={styles.post}>
            <div className={styles.avatar} aria-hidden>{account ? account.charAt(0).toUpperCase() : '나'}</div>
            <div className={styles.postBody}>
              <div className={styles.postHead}>
                <strong>{account || '내 계정'}</strong>
                {account && <span className={styles.handle}>@{account}</span>}
                <span className={styles.handle}>· {i === 0 ? '본문' : `답글 ${i}`}</span>
                <span className={[styles.len, p.valid ? '' : styles.lenBad].join(' ')}>
                  {p.weighted_length}/{limit}
                </span>
              </div>
              <p className={styles.postText}>{p.text}</p>
              {imgs.length > 0 && (
                <div className={`${styles.postImgs} ${imgs.length === 1 ? styles.singleImage : ''}`}>
                  {imgs.map((m) =>
                    m.state === 'deleted' ? (
                      <span key={m.media_id} className={styles.cleaned}>
                        보관 기간이 지나 정리된 이미지
                      </span>
                    ) : (
                      <img key={m.media_id} src={apiUrl(m.thumb_url || m.url)} alt={m.alt_text || '첨부 이미지'} loading="lazy" />
                    ),
                  )}
                </div>
              )}
              {p.problem && <p className={styles.problem}>{p.problem}</p>}
              <div className={styles.postMetrics} aria-hidden="true">
                <MessageCircle size={17} /><Repeat2 size={17} /><Heart size={17} /><BarChart2 size={17} /><Share size={17} />
              </div>
              <div className={styles.postActions}>
                <Button
                  variant="ghost"
                  size="sm"
                  icon={<Copy size={14} />}
                  onClick={async () => {
                    const ok = await copyText(p.text);
                    toast.show(ok ? `${i === 0 ? '본문' : `답글 ${i}`}을 복사했어요` : '복사하지 못했어요', ok ? 'success' : 'error');
                  }}
                >
                  복사
                </Button>
              </div>
            </div>
          </li>
        );
      })}
    </ol>
  );
}

function HistorySheet({ open, onClose, content, onRestored }: { open: boolean; onClose: () => void; content: ContentDetail; onRestored: (d: ContentDetail) => void }) {
  const revs = useRevisions(content.id, open);
  const toast = useToast();
  async function restore(revId: string, n: number) {
    try {
      const d = await post<ContentDetail>(`/api/contents/${content.id}/revisions/${revId}/restore`, { edit_version: content.edit_version });
      toast.show(`버전 ${n} 내용으로 되돌렸어요 (새 버전으로 저장)`, 'success');
      onRestored(d);
    } catch (e) {
      toast.show(errorMessage(e), 'error');
    }
  }
  return (
    <Sheet open={open} onClose={onClose} title="이전 버전" description="승인되었거나 게시된 버전은 바뀌지 않고 기록으로 남아요. 되돌리면 그 내용으로 새 편집본을 만들어요." size="lg">
      {revs.isLoading && <LoadingList rows={2} />}
      <ul className={styles.history}>
        {revs.data?.items.map((r) => (
          <li key={r.id} className={styles.historyItem}>
            <div className={styles.historyHead}>
              <strong>버전 {r.number}</strong>
              {r.id === content.current_revision_id && <Badge tone="info">편집 중</Badge>}
              {r.approved_at && <Badge tone="success">승인</Badge>}
              {r.published && <Badge tone="scheduled">게시됨</Badge>}
              <span className={styles.muted}>{formatDateTime(r.updated_at)}</span>
            </div>
            <p className={styles.historyPreview}>{r.preview}</p>
            {r.id !== content.current_revision_id && (
              <Button variant="secondary" size="sm" icon={<RotateCcw size={16} />} onClick={() => restore(r.id, r.number)}>
                이 내용으로 되돌리기
              </Button>
            )}
          </li>
        ))}
      </ul>
    </Sheet>
  );
}
