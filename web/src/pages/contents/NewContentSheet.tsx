import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useState, type ReactNode } from 'react';
import { ApiError, errorMessage, post } from '../../api/client';
import { useCategories } from '../../api/contents';
import { useSettings } from '../../api/queries';
import { SPLIT_LABEL, type ContentDetail, type SplitMode, type SplitResult } from '../../api/types';
import { Button } from '../../components/ui/Button';
import { TextArea } from '../../components/ui/Field';
import { Segmented } from '../../components/ui/Segmented';
import { Sheet } from '../../components/ui/Sheet';
import { TextField } from '../../components/ui/TextField';
import { useToast } from '../../components/ui/Toast';
import { useDebounced } from '../../lib/hooks';
import { measure } from '../../lib/thread';
import styles from './NewContentSheet.module.css';

type Tab = 'write' | 'paste' | 'generate';

interface Props {
  initialTab?: Tab;
  open: boolean;
  onClose: () => void;
  onCreated: (id: string) => void;
  generateTab?: ReactNode; // AI 생성 탭 (생성 기능이 연결되면 채워진다)
}

export function NewContentSheet({ open, onClose, onCreated, generateTab, initialTab = 'write' }: Props) {
  const [tab, setTab] = useState<Tab>(initialTab === 'generate' && !generateTab ? 'write' : initialTab);
  const tabs: { value: Tab; label: string }[] = [
    { value: 'write', label: '직접 쓰기' },
    { value: 'paste', label: '여러 개 붙여넣기' },
  ];
  if (generateTab) tabs.push({ value: 'generate', label: 'AI로 생성' });
  return (
    <Sheet open={open} onClose={onClose} title="새 글" size="lg">
      <div className={styles.tabs}>
        <Segmented label="등록 방식" value={tab} onChange={setTab} options={tabs} />
      </div>
      {tab === 'write' && <WriteForm onCreated={onCreated} />}
      {tab === 'paste' && <PasteForm onDone={onClose} />}
      {tab === 'generate' && generateTab}
    </Sheet>
  );
}

function useSplitPreview(raw: string, mode: SplitMode) {
  const d = useDebounced(raw, 350);
  return useQuery({
    queryKey: ['split-preview', d, mode],
    queryFn: () => post<{ result: SplitResult }>('/api/contents/split-preview', { raw_text: d, split_mode: mode, split_options: { numbering: false, max_replies: 0 } }),
    enabled: d.trim().length > 0,
    placeholderData: (p) => p,
  });
}

function WriteForm({ onCreated }: { onCreated: (id: string) => void }) {
  const settings = useSettings();
  const cats = useCategories();
  const qc = useQueryClient();
  const toast = useToast();
  const [title, setTitle] = useState('');
  const [category, setCategory] = useState('');
  const [raw, setRaw] = useState('');
  const [mode, setMode] = useState<SplitMode | null>(null);
  const [busy, setBusy] = useState(false);
  const [fields, setFields] = useState<Record<string, string>>({});
  const effMode = mode ?? (settings.data?.policy.thread.split_mode as SplitMode) ?? 'two_parts';
  const preview = useSplitPreview(raw, effMode);
  const total = measure(raw);
  const review = settings.data?.operation_mode === 'review';

  async function save(approve: boolean) {
    if (!raw.trim()) {
      setFields({ raw_text: '내용을 입력하세요' });
      return;
    }
    setBusy(true);
    setFields({});
    try {
      const d = await post<ContentDetail>('/api/contents', {
        title,
        category,
        tags: [],
        raw_text: raw,
        split_mode: effMode,
        split_options: { numbering: settings.data?.policy.thread.numbering ?? false, max_replies: settings.data?.policy.thread.max_replies ?? 0 },
        image_mode: settings.data?.policy.image.default_mode ?? 'none',
        approve,
      });
      await qc.invalidateQueries({ queryKey: ['contents'] });
      toast.show(approve && d.approved_revision_id ? '저장하고 승인했어요' : '저장했어요', 'success');
      onCreated(d.id);
    } catch (e) {
      if (e instanceof ApiError && Object.keys(e.fields).length) setFields(e.fields);
      else toast.show(errorMessage(e), 'error');
    } finally {
      setBusy(false);
    }
  }

  const r = preview.data?.result;
  return (
    <div className={styles.form}>
      <TextField label="관리용 제목 (선택)" value={title} onChange={(e) => setTitle(e.target.value)} hint="비우면 원문 첫 줄로 정해요" error={fields.title} />
      <TextField label="카테고리 (선택)" value={category} onChange={(e) => setCategory(e.target.value)} list="new-category-options" error={fields.category} />
      <datalist id="new-category-options">{cats.data?.items.map((c) => <option key={c} value={c} />)}</datalist>
      <TextArea
        label="원문"
        rows={8}
        value={raw}
        onChange={(e) => setRaw(e.target.value)}
        placeholder="소설·썰·명언 등 올릴 글 전체를 붙여넣으세요. 앱이 본문과 답글로 나눠요."
        hint={`전체 ${total.weighted}자 (X 기준, 한글·이모지는 2자)`}
        error={fields.raw_text}
      />
      <div className={styles.field}>
        <span className={styles.label}>나누는 방식</span>
        <Segmented label="나누는 방식" value={effMode} onChange={setMode} options={(['two_parts', 'overflow', 'manual'] as SplitMode[]).map((m) => ({ value: m, label: SPLIT_LABEL[m] }))} />
        {effMode === 'manual' && <p className={styles.hint}>한 줄에 --- 만 쓰면 그 자리에서 나눠요.</p>}
      </div>
      {r && raw.trim() && (
        <div className={[styles.summary, r.publishable ? '' : styles.warn].join(' ')} aria-live="polite">
          <strong>{r.note}</strong>
          <span>
            X 게시물 {r.post_count}개 · 각 부분 {r.parts.map((p) => p.weighted_length).join(' / ')}자
          </span>
          {r.has_url && <span>링크가 있으면 X 게시 단가가 높아질 수 있어요</span>}
          {r.problems.map((p) => (
            <span key={p} className={styles.problem}>
              {p}
            </span>
          ))}
        </div>
      )}
      <div className={styles.actions}>
        <Button size="lg" variant={review ? 'secondary' : 'primary'} busy={busy} onClick={() => save(false)}>
          저장하고 편집
        </Button>
        {review && (
          <Button size="lg" busy={busy} onClick={() => save(true)} disabled={!r?.publishable}>
            저장하고 승인
          </Button>
        )}
      </div>
    </div>
  );
}

const SEP = /^\s*={3,}\s*$/m;

function PasteForm({ onDone }: { onDone: () => void }) {
  const [text, setText] = useState('');
  const [category, setCategory] = useState('');
  const [busy, setBusy] = useState(false);
  const qc = useQueryClient();
  const toast = useToast();
  const chunks = text
    .replace(/\r\n/g, '\n')
    .split(new RegExp(SEP.source, 'm'))
    .map((c) => c.trim())
    .filter(Boolean);

  async function create() {
    setBusy(true);
    try {
      const r = await post<{ created: number; errors: Record<string, string> }>('/api/contents/batch', {
        items: chunks.map((c) => ({ raw_text: c, category, tags: [] })),
      });
      await qc.invalidateQueries({ queryKey: ['contents'] });
      const errs = Object.keys(r.errors).length;
      toast.show(errs ? `${r.created}개 등록, ${errs}개 항목에 오류가 있어 건너뛰었어요` : `${r.created}개를 등록했어요`, errs ? 'info' : 'success');
      onDone();
    } catch (e) {
      toast.show(errorMessage(e), 'error');
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className={styles.form}>
      <TextArea
        label="여러 글"
        rows={10}
        value={text}
        onChange={(e) => setText(e.target.value)}
        placeholder={'첫 번째 글\n===\n두 번째 글\n===\n세 번째 글'}
        hint="글과 글 사이에 === 만 있는 줄을 넣어 구분해요. 각 글은 설정의 기본 방식으로 나눠져요."
      />
      <TextField label="카테고리 (선택)" value={category} onChange={(e) => setCategory(e.target.value)} />
      <div className={styles.summary} aria-live="polite">
        <strong>{chunks.length}개를 등록해요</strong>
        {chunks.length > 100 && <span className={styles.problem}>한 번에 100개까지 등록할 수 있어요</span>}
      </div>
      <Button size="lg" block busy={busy} disabled={chunks.length === 0 || chunks.length > 100} onClick={create}>
        {chunks.length}개 등록
      </Button>
    </div>
  );
}
