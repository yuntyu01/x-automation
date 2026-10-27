import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Sparkles } from 'lucide-react';
import { useState } from 'react';
import { ApiError, errorMessage, get } from '../../api/client';
import { applyResult, discardResult, genKeys, newRequestKey, useRegenerate, useResults } from '../../api/generation';
import { useSettings } from '../../api/queries';
import type { ContentDetail, GenerationRequest, GenerationResult } from '../../api/types';
import { Badge } from '../../components/ui/Badge';
import { Button } from '../../components/ui/Button';
import { Card, CardTitle } from '../../components/ui/Card';
import { Select } from '../../components/ui/Field';
import { Segmented } from '../../components/ui/Segmented';
import { TextField } from '../../components/ui/TextField';
import { useToast } from '../../components/ui/Toast';
import { relativeTime } from '../../lib/format';
import styles from './RegenerateCard.module.css';

type Target = 'all' | 'part' | 'image';

const CHIPS = ['짧게', '길게', '더 자연스럽게', '반말로', '존댓말로', '첫 문장을 더 끌리게', '결말을 더 강하게'];

interface Props {
  content: ContentDetail;
  onReplaced: (d: ContentDetail) => void;
  flush: () => Promise<void>;
  editVersion: () => number;
}

const partName = (i: number) => (i === 0 ? '본문' : `답글 ${i}`);

// 다시 만들기: 결과는 바로 덮어쓰지 않고 현재 글과 비교한 뒤 적용하거나 버린다
export function RegenerateCard({ content, onReplaced, flush, editVersion }: Props) {
  const settings = useSettings();
  const toast = useToast();
  const qc = useQueryClient();
  const regen = useRegenerate(content.id);
  const g = settings.data?.policy.generation;
  const textAllowed = !!g?.text_enabled && settings.data?.operation_mode !== 'publish_only';
  const imageAllowed = !!g?.image_enabled;
  const [target, setTarget] = useState<Target>('all');
  const [part, setPart] = useState(0);
  const [instruction, setInstruction] = useState('');
  const [imagePrompt, setImagePrompt] = useState('');
  const [pending, setPending] = useState<string | null>(null);
  const [busyId, setBusyId] = useState<string | null>(null);

  const req = useQuery({
    queryKey: ['generation', 'one', pending],
    queryFn: () => get<GenerationRequest>(`/api/generation/${pending}`),
    enabled: !!pending,
    refetchInterval: (q) => (q.state.data && !['queued', 'running'].includes(q.state.data.state) ? false : 2000),
  });
  const running = !!pending && (!req.data || req.data.state === 'queued' || req.data.state === 'running');
  const failed = pending && req.data && (req.data.state === 'failed' || req.data.state === 'canceled') ? req.data.error : null;
  const results = useResults(content.id, running, pending && !running ? pending : '');
  const items = results.data?.items ?? [];
  const fresh = items.filter((r) => r.state === 'new');
  const used = items.length;
  const remaining = g ? Math.max(g.max_regenerations - used, 0) : 0;
  const parts = content.revision.parts;
  const effTarget: Target = !textAllowed && imageAllowed ? 'image' : target;
  const effPart = Math.min(part, Math.max(parts.length - 1, 0));

  if (settings.data && !textAllowed && !imageAllowed) return null;

  async function start() {
    try {
      await flush();
      const r = await regen.mutateAsync({
        idempotency_key: newRequestKey('regen'),
        target: effTarget,
        part_index: effPart,
        instruction: instruction.trim(),
        image_prompt: imagePrompt.trim(),
      });
      setPending(r.id);
    } catch (e) {
      toast.show(e instanceof ApiError && Object.keys(e.fields).length ? Object.values(e.fields)[0]! : errorMessage(e), 'error');
    }
  }

  async function decide(r: GenerationResult, apply: boolean) {
    setBusyId(r.id);
    try {
      if (apply) {
        await flush();
        const d = await applyResult(content.id, r.id, editVersion());
        onReplaced(d);
        toast.show(content.approved_revision_id ? '적용했어요. 바뀐 글은 다시 승인해야 게시돼요' : '적용했어요', 'success');
      } else {
        await discardResult(content.id, r.id);
        toast.show('버렸어요', 'info');
      }
      await qc.invalidateQueries({ queryKey: genKeys.results(content.id) });
    } catch (e) {
      toast.show(errorMessage(e), 'error');
    } finally {
      setBusyId(null);
    }
  }

  const targets: { value: Target; label: string }[] = [];
  if (textAllowed) targets.push({ value: 'all', label: '전체 글' });
  if (textAllowed && parts.length > 1) targets.push({ value: 'part', label: '한 부분만' });
  if (imageAllowed) targets.push({ value: 'image', label: '이미지' });

  return (
    <Card aria-labelledby="regen-title">
      <CardTitle id="regen-title" action={g ? <span className={styles.muted}>남은 횟수 {remaining}/{g.max_regenerations}</span> : undefined}>
        <span className={styles.title}>
          <Sparkles size={18} aria-hidden /> 다시 만들기
        </span>
      </CardTitle>
      <div className={styles.stack}>
        <Segmented label="다시 만들 대상" value={effTarget} onChange={setTarget} options={targets} />
        {effTarget === 'part' && (
          <Select label="고칠 부분" value={String(effPart)} onChange={(e) => setPart(Number(e.target.value))} options={parts.map((_, i) => ({ value: String(i), label: partName(i) }))} />
        )}
        {effTarget !== 'image' ? (
          <>
            <div className={styles.chips} role="group" aria-label="자주 쓰는 요청">
              {CHIPS.map((c) => (
                <button
                  key={c}
                  type="button"
                  className={[styles.chip, instruction === c ? styles.chipOn : ''].join(' ')}
                  aria-pressed={instruction === c}
                  onClick={() => setInstruction(instruction === c ? '' : c)}
                >
                  {c}
                </button>
              ))}
            </div>
            <TextField label="수정 요청" value={instruction} onChange={(e) => setInstruction(e.target.value)} placeholder="예: 마지막 문단을 반전 있게" maxLength={500} />
          </>
        ) : (
          <TextField label="이미지 설명 (선택)" value={imagePrompt} onChange={(e) => setImagePrompt(e.target.value)} placeholder="비우면 글 첫 줄의 분위기로 만들어요" maxLength={2000} />
        )}
        <Button variant="secondary" busy={regen.isPending || running} disabled={remaining <= 0 || targets.length === 0} onClick={start}>
          {running ? '만드는 중…' : '다시 만들기'}
        </Button>
        {remaining <= 0 && <p className={styles.muted}>이 글의 재생성 상한에 도달했어요. 생성 설정에서 바꿀 수 있어요.</p>}
        {failed && <p className={styles.err}>{failed}</p>}

        {fresh.map((r) => (
          <ResultView key={r.id} r={r} content={content} busy={busyId === r.id} onDecide={(apply) => decide(r, apply)} />
        ))}
        {items.length > fresh.length && <p className={styles.muted}>지난 결과 {items.length - fresh.length}개는 적용했거나 버렸어요.</p>}
      </div>
    </Card>
  );
}

function ResultView({ r, content, busy, onDecide }: { r: GenerationResult; content: ContentDetail; busy: boolean; onDecide: (apply: boolean) => void }) {
  const current = r.target === 'part' && r.part_index !== null ? (content.revision.parts[r.part_index]?.text ?? '') : content.revision.raw_text;
  const label = r.kind === 'image' ? '새 이미지' : r.target === 'part' && r.part_index !== null ? `${partName(r.part_index)} 다시 쓰기` : '전체 다시 쓰기';
  return (
    <section className={styles.result} aria-label={label}>
      <div className={styles.resultHead}>
        <strong>{label}</strong>
        <Badge tone="info">제안</Badge>
        <span className={styles.muted}>{relativeTime(r.created_at)}</span>
      </div>
      {r.kind === 'image' ? (
        r.thumb_url ? <img className={styles.img} src={r.thumb_url} alt="생성된 이미지 미리보기" /> : <p className={styles.muted}>이미지를 불러올 수 없어요</p>
      ) : (
        <div className={styles.compare}>
          <div>
            <span className={styles.colLabel}>지금</span>
            <p className={styles.text}>{current}</p>
          </div>
          <div>
            <span className={styles.colLabel}>제안</span>
            <p className={[styles.text, styles.new].join(' ')}>{r.text}</p>
          </div>
        </div>
      )}
      {r.target === 'part' && <p className={styles.muted}>부분만 바꾸면 다른 부분을 그대로 두기 위해 &lsquo;직접 나누기&rsquo; 방식으로 바뀌어요.</p>}
      <div className={styles.actions}>
        <Button variant="secondary" disabled={busy} onClick={() => onDecide(false)}>
          버리기
        </Button>
        <Button busy={busy} onClick={() => onDecide(true)}>
          {r.kind === 'image' ? '본문에 붙이기' : '적용'}
        </Button>
      </div>
    </section>
  );
}
