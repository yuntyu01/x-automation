import { useInfiniteQuery, useQueryClient } from '@tanstack/react-query';
import { Archive, CalendarPlus, CheckSquare, Download, FileText, Filter, ImageIcon, MoreHorizontal, Plus, Search, Sparkles, Trash2, Upload, X } from 'lucide-react';
import { useState } from 'react';
import { Link, useNavigate } from 'react-router';
import { errorMessage, get } from '../../api/client';
import { contentQueryString, useBulk, useCategories, type ContentQuery } from '../../api/contents';
import { placeContents } from '../../api/schedule';
import { STAGE_LABEL, type ContentItem, type ContentPage, type Stage } from '../../api/types';
import { PageHeader } from '../../components/layout/AppShell';
import { StageBadge } from '../../components/StageBadge';
import { Badge } from '../../components/ui/Badge';
import { Button } from '../../components/ui/Button';
import { Card } from '../../components/ui/Card';
import { Select } from '../../components/ui/Field';
import { IconButton } from '../../components/ui/IconButton';
import { Row, RowGroup } from '../../components/ui/Row';
import { Segmented } from '../../components/ui/Segmented';
import { Sheet } from '../../components/ui/Sheet';
import { EmptyState, ErrorState, LoadingList } from '../../components/ui/States';
import { TextField } from '../../components/ui/TextField';
import { useToast } from '../../components/ui/Toast';
import { formatDateTime } from '../../lib/format';
import { useDebounced, useSessionState } from '../../lib/hooks';
import { ImportSheet } from './ImportSheet';
import { GenerateForm } from '../../components/generation/GenerateForm';
import { GenerationQueue } from '../../components/generation/GenerationQueue';
import { NewContentSheet } from './NewContentSheet';
import styles from './ContentsPage.module.css';

const STAGES: (Stage | 'all')[] = ['all', 'draft', 'review', 'ready', 'scheduled', 'published', 'problem', 'archived'];
const INITIAL: ContentQuery = { stage: 'all', q: '', category: '', from: '', to: '', sort: 'updated', limit: 20 };

export function ContentsPage() {
  const [filter, setFilter] = useSessionState<ContentQuery>('xa-contents-filter', INITIAL);
  const [search, setSearch] = useState(filter.q);
  const q = useDebounced(search, 300);
  const f = { ...filter, q };
  const list = useInfiniteQuery({
    queryKey: ['contents', 'list', f],
    queryFn: ({ pageParam, signal }) => get<ContentPage>(`/api/contents?${contentQueryString(f, pageParam)}`, signal),
    initialPageParam: 0,
    getNextPageParam: (last) => (last.offset + last.items.length < last.total ? last.offset + last.items.length : undefined),
  });
  const items = list.data?.pages.flatMap((p) => p.items) ?? [];
  const counts = list.data?.pages[0]?.counts ?? {};
  const total = list.data?.pages[0]?.total ?? 0;

  const [selecting, setSelecting] = useState(false);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [sheet, setSheet] = useState<'new' | 'import' | 'export' | 'filters' | 'more' | 'category' | 'delete' | null>(null);
  const bulk = useBulk();
  const qc = useQueryClient();
  const toast = useToast();
  const nav = useNavigate();

  const toggle = (id: string) =>
    setSelected((s) => {
      const n = new Set(s);
      if (n.has(id)) n.delete(id);
      else n.add(id);
      return n;
    });
  const exitSelect = () => {
    setSelecting(false);
    setSelected(new Set());
  };

  const [placing, setPlacing] = useState(false);
  // 고른 순서대로 다음 빈 예약에 넣는다 (지금 게시가 아니라 예약)
  async function placeSelected() {
    setPlacing(true);
    try {
      const r = await placeContents([...selected]);
      const skipped = Object.keys(r.skipped).length;
      const parts = [`${r.placed.length}개를 다음 빈 시간에 배치했어요`];
      if (r.placed[0]) parts[0] += ` (첫 예약 ${formatDateTime(r.placed[0].slot_at)})`;
      if (skipped) parts.push(`${skipped}개는 건너뜀: ${Object.values(r.skipped)[0]}`);
      if (r.left.length) parts.push(`빈 예약이 모자라 ${r.left.length}개는 남겼어요`);
      toast.show(parts.join(' · '), r.placed.length && !r.left.length ? 'success' : 'info');
      await qc.invalidateQueries({ queryKey: ['contents'] });
      exitSelect();
    } catch (e) {
      toast.show(errorMessage(e), 'error');
    } finally {
      setPlacing(false);
    }
  }

  async function runBulk(action: 'archive' | 'unarchive' | 'category' | 'delete', category?: string) {
    const ids = [...selected];
    try {
      const r = await bulk.mutateAsync({ action, ids, category });
      const skipped = Object.keys(r.skipped).length;
      toast.show(skipped ? `${r.updated}개 처리, ${skipped}개는 건너뛰었어요: ${Object.values(r.skipped)[0]}` : `${r.updated}개를 처리했어요`, skipped ? 'info' : 'success');
      exitSelect();
      setSheet(null);
    } catch (e) {
      toast.show(errorMessage(e), 'error');
    }
  }

  const activeFilters = [filter.category, filter.from, filter.to].filter(Boolean).length + (filter.sort !== 'updated' ? 1 : 0);

  return (
    <>
      <PageHeader
        title="콘텐츠"
        actions={
          selecting ? (
            <Button variant="ghost" size="sm" onClick={exitSelect} icon={<X size={18} />}>
              선택 취소
            </Button>
          ) : (
            <>
              <IconButton label="더 보기" onClick={() => setSheet('more')}>
                <MoreHorizontal size={22} />
              </IconButton>
              <Button size="sm" icon={<Plus size={18} />} onClick={() => setSheet('new')}>
                새 글
              </Button>
            </>
          )
        }
      />
      <div className={styles.toolbar}>
        <div className={styles.searchRow}>
          <TextField
            label="제목·원문 검색"
            hideLabel
            type="search"
            placeholder="제목이나 원문으로 검색"
            value={search}
            onChange={(e) => {
              setSearch(e.target.value);
              setFilter((p) => ({ ...p, q: e.target.value }));
            }}
            suffix={<Search size={18} aria-hidden />}
          />
          <Button variant="secondary" onClick={() => setSheet('filters')} icon={<Filter size={18} />} aria-label={`필터${activeFilters ? ` ${activeFilters}개 적용` : ''}`}>
            {activeFilters ? `필터 ${activeFilters}` : '필터'}
          </Button>
        </div>
        <Segmented
          label="상태"
          scroll
          value={filter.stage}
          onChange={(v) => setFilter((p) => ({ ...p, stage: v }))}
          options={STAGES.map((s) => ({ value: s, label: s === 'all' ? '전체' : STAGE_LABEL[s], count: counts[s] ?? 0 }))}
        />
      </div>

      <GenerationQueue activeOnly />
      {list.isLoading && <LoadingList />}
      {list.isError && <ErrorState error={list.error} onRetry={() => list.refetch()} />}
      {list.data && items.length === 0 && (
        <Card>
          {f.q || search || activeFilters || filter.stage !== 'all' ? (
            <EmptyState icon={<Search size={28} aria-hidden />} title="조건에 맞는 글이 없어요" body="검색어나 필터를 바꿔 보세요.">
              <Button
                variant="secondary"
                onClick={() => {
                  setSearch('');
                  setFilter(INITIAL);
                }}
              >
                조건 초기화
              </Button>
            </EmptyState>
          ) : (
            <EmptyState icon={<FileText size={28} aria-hidden />} title="아직 글이 없어요" body="직접 쓴 글을 등록하거나 파일로 한꺼번에 가져올 수 있어요.">
              <Button onClick={() => setSheet('new')} icon={<Plus size={18} />}>
                직접 등록
              </Button>
              <Button variant="secondary" onClick={() => setSheet('import')} icon={<Upload size={18} />}>
                가져오기
              </Button>
            </EmptyState>
          )}
        </Card>
      )}
      {items.length > 0 && (
        <>
          <div className={styles.listHead}>
            <span className={styles.total}>{total}개</span>
            {!selecting && (
              <Button variant="ghost" size="sm" icon={<CheckSquare size={18} />} onClick={() => setSelecting(true)}>
                선택
              </Button>
            )}
            {selecting && (
              <Button variant="ghost" size="sm" onClick={() => setSelected(new Set(items.map((i) => i.id)))}>
                보이는 {items.length}개 모두 선택
              </Button>
            )}
          </div>
          <ul className={styles.list}>
            {items.map((it) => (
              <li key={it.id}>
                <ContentCard item={it} selecting={selecting} selected={selected.has(it.id)} onToggle={() => toggle(it.id)} />
              </li>
            ))}
          </ul>
          {list.hasNextPage && (
            <div className={styles.more}>
              <Button variant="secondary" block busy={list.isFetchingNextPage} onClick={() => list.fetchNextPage()}>
                더 보기
              </Button>
            </div>
          )}
        </>
      )}

      {selecting && selected.size > 0 && (
        <div className={styles.bulkBar} role="toolbar" aria-label="선택한 글 작업">
          <span className={styles.bulkCount}>{selected.size}개 선택</span>
          {filter.stage === 'archived' ? (
            <Button size="sm" variant="secondary" onClick={() => runBulk('unarchive')}>
              보관 해제
            </Button>
          ) : (
            <Button size="sm" variant="secondary" icon={<Archive size={16} />} onClick={() => runBulk('archive')}>
              보관
            </Button>
          )}
          {filter.stage !== 'archived' && (
            <Button size="sm" variant="secondary" icon={<CalendarPlus size={16} />} busy={placing} onClick={placeSelected}>
              빈 시간에 배치
            </Button>
          )}
          <Button size="sm" variant="secondary" onClick={() => setSheet('category')}>
            분류 변경
          </Button>
          <Button size="sm" variant="danger" icon={<Trash2 size={16} />} onClick={() => setSheet('delete')}>
            삭제
          </Button>
        </div>
      )}

      <NewContentSheet
        open={sheet === 'new'}
        onClose={() => setSheet(null)}
        onCreated={(id) => nav(`/contents/${id}`)}
        generateTab={<GenerateForm onSubmitted={() => setSheet(null)} />}
      />
      <ImportSheet open={sheet === 'import'} onClose={() => setSheet(null)} />
      <FilterSheet open={sheet === 'filters'} onClose={() => setSheet(null)} filter={filter} setFilter={setFilter} />
      <Sheet open={sheet === 'more'} onClose={() => setSheet(null)} title="콘텐츠 관리">
        <RowGroup>
          <Row icon={<Upload size={20} />} label="CSV·JSON 가져오기" description="양식 다운로드·필드 연결·미리보기 후 등록" onClick={() => setSheet('import')} />
          <Row icon={<Download size={20} />} label="내보내기" description="현재 필터의 글을 CSV·JSON으로" onClick={() => setSheet('export')} />
          <Row icon={<Sparkles size={20} />} label="프롬프트 관리" to="/prompts" />
          <Row icon={<ImageIcon size={20} />} label="이미지 보관함" to="/images" />
        </RowGroup>
      </Sheet>
      <Sheet open={sheet === 'export'} onClose={() => setSheet(null)} title="내보내기" description="줄바꿈·한글을 보존하고, 스프레드시트에서 수식으로 실행되지 않게 처리해요. 연결 정보는 포함하지 않아요.">
        <div className={styles.exportBtns}>
          <a className={styles.exportLink} href={`/api/contents/export?format=csv&${contentQueryString(f)}`} download>
            <Download size={18} aria-hidden /> CSV로 내보내기
          </a>
          <a className={styles.exportLink} href={`/api/contents/export?format=json&${contentQueryString(f)}`} download>
            <Download size={18} aria-hidden /> JSON으로 내보내기
          </a>
        </div>
      </Sheet>
      <CategorySheet open={sheet === 'category'} onClose={() => setSheet(null)} count={selected.size} onApply={(c) => runBulk('category', c)} busy={bulk.isPending} />
      <Sheet
        open={sheet === 'delete'}
        onClose={() => setSheet(null)}
        title={`${selected.size}개를 삭제할까요?`}
        description="앱의 기록만 지워요. X에 올라간 게시물은 삭제되지 않아요. 예약했거나 게시한 기록이 있는 글은 삭제되지 않고 건너뛰어요 (보관을 쓰세요)."
        footer={
          <>
            <Button variant="secondary" onClick={() => setSheet(null)}>
              취소
            </Button>
            <Button variant="danger" busy={bulk.isPending} onClick={() => runBulk('delete')}>
              삭제
            </Button>
          </>
        }
      >
        <p className={styles.muted}>삭제한 글은 되돌릴 수 없어요.</p>
      </Sheet>
    </>
  );
}

function ContentCard({ item, selecting, selected, onToggle }: { item: ContentItem; selecting: boolean; selected: boolean; onToggle: () => void }) {
  const body = (
    <>
      <div className={styles.cardHead}>
        <StageBadge stage={item.stage} />
        {item.is_demo && <Badge tone="warning">데모</Badge>}
        {item.source === 'ai' && <Badge tone="info" icon={false}>AI</Badge>}
        {item.category && <span className={styles.category}>{item.category}</span>}
        <span className={styles.time}>{formatDateTime(item.updated_at)}</span>
      </div>
      <p className={styles.title}>{item.title}</p>
      <p className={styles.preview}>{item.preview}</p>
      <div className={styles.meta}>
        <span>{item.part_count > 1 ? `본문 + 답글 ${item.part_count - 1}` : item.part_count === 1 ? '본문만' : '부분 없음'}</span>
        {item.image_mode !== 'none' && (
          <span className={styles.inline}>
            <ImageIcon size={14} aria-hidden /> 이미지
          </span>
        )}
        {item.reservation_at && <span>예약 {formatDateTime(item.reservation_at)}</span>}
        {item.problems.length > 0 && item.stage !== 'published' && <span className={styles.problem}>문제 {item.problems.length}개</span>}
      </div>
    </>
  );
  if (selecting) {
    return (
      <label className={[styles.card, selected ? styles.selected : ''].join(' ')}>
        <input type="checkbox" checked={selected} onChange={onToggle} className={styles.check} aria-label={`${item.title} 선택`} />
        <div className={styles.cardBody}>{body}</div>
      </label>
    );
  }
  return (
    <Link to={`/contents/${item.id}`} className={styles.card}>
      <div className={styles.cardBody}>{body}</div>
    </Link>
  );
}

function FilterSheet({ open, onClose, filter, setFilter }: { open: boolean; onClose: () => void; filter: ContentQuery; setFilter: (f: ContentQuery) => void }) {
  const cats = useCategories();
  const [draft, setDraft] = useState(filter);
  const [lastOpen, setLastOpen] = useState(open);
  if (open !== lastOpen) {
    setLastOpen(open);
    if (open) setDraft(filter);
  }
  return (
    <Sheet
      open={open}
      onClose={onClose}
      title="필터와 정렬"
      footer={
        <>
          <Button variant="secondary" onClick={() => setDraft({ ...draft, category: '', from: '', to: '', sort: 'updated' })}>
            초기화
          </Button>
          <Button
            onClick={() => {
              setFilter(draft);
              onClose();
            }}
          >
            적용
          </Button>
        </>
      }
    >
      <div className={styles.form}>
        <Select
          label="카테고리"
          value={draft.category}
          onChange={(e) => setDraft({ ...draft, category: e.target.value })}
          options={[{ value: '', label: '전체' }, ...(cats.data?.items ?? []).map((c) => ({ value: c, label: c }))]}
        />
        <div className={styles.twoCol}>
          <TextField label="등록일 시작" type="date" value={draft.from} onChange={(e) => setDraft({ ...draft, from: e.target.value })} />
          <TextField label="등록일 끝" type="date" value={draft.to} onChange={(e) => setDraft({ ...draft, to: e.target.value })} />
        </div>
        <Select
          label="정렬"
          value={draft.sort}
          onChange={(e) => setDraft({ ...draft, sort: e.target.value as ContentQuery['sort'] })}
          options={[
            { value: 'updated', label: '최근 수정순' },
            { value: 'created', label: '최근 등록순' },
            { value: 'title', label: '제목순' },
            { value: 'slot', label: '예약 시각순' },
          ]}
        />
      </div>
    </Sheet>
  );
}

function CategorySheet({ open, onClose, count, onApply, busy }: { open: boolean; onClose: () => void; count: number; onApply: (c: string) => void; busy: boolean }) {
  const cats = useCategories();
  const [value, setValue] = useState('');
  return (
    <Sheet
      open={open}
      onClose={onClose}
      title={`${count}개의 분류 바꾸기`}
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>
            취소
          </Button>
          <Button busy={busy} onClick={() => onApply(value)}>
            바꾸기
          </Button>
        </>
      }
    >
      <TextField label="카테고리" value={value} onChange={(e) => setValue(e.target.value)} list="category-options" hint="비우면 카테고리 없음으로 바뀌어요" />
      <datalist id="category-options">
        {cats.data?.items.map((c) => <option key={c} value={c} />)}
      </datalist>
    </Sheet>
  );
}
