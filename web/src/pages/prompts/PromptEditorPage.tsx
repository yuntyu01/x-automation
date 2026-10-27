import { useQueryClient } from '@tanstack/react-query';
import { ArrowDown, ArrowUp, CopyPlus, History, Plus, Power, Trash2 } from 'lucide-react';
import { useMemo, useState } from 'react';
import { useNavigate, useParams } from 'react-router';
import { ApiError, errorMessage, post, put } from '../../api/client';
import { usePrompt, usePromptVersions } from '../../api/contents';
import type { Prompt, PromptDefaults, PromptVariable } from '../../api/types';
import { PageHeader } from '../../components/layout/AppShell';
import { Badge } from '../../components/ui/Badge';
import { Button } from '../../components/ui/Button';
import { Card, CardTitle } from '../../components/ui/Card';
import { Select, TextArea } from '../../components/ui/Field';
import { IconButton } from '../../components/ui/IconButton';
import { Sheet } from '../../components/ui/Sheet';
import { ErrorState, LoadingList } from '../../components/ui/States';
import { Switch } from '../../components/ui/Switch';
import { TextField } from '../../components/ui/TextField';
import { useToast } from '../../components/ui/Toast';
import { formatDateTime } from '../../lib/format';
import { BackButton } from '../settings/ConnectionsPage';
import styles from './PromptEditorPage.module.css';

interface Form {
  name: string;
  category: string;
  description: string;
  body: string;
  variables: PromptVariable[];
  defaults: PromptDefaults;
  note: string;
}

const EMPTY: Form = {
  name: '',
  category: '',
  description: '',
  body: '',
  variables: [],
  defaults: { model: '', length: 'medium', tone: '', image_mode: 'none', image_prompt: '', split_mode: 'two_parts' },
  note: '',
};

const placeholderRe = /\{\{\s*([A-Za-z_][A-Za-z0-9_]*)\s*\}\}/g;

export function placeholders(body: string): string[] {
  const out: string[] = [];
  for (const m of body.matchAll(placeholderRe)) if (m[1] && !out.includes(m[1])) out.push(m[1]);
  return out;
}

export function PromptEditorPage() {
  const { id } = useParams();
  const isNew = id === 'new';
  const q = usePrompt(id);
  if (!isNew && q.isLoading)
    return (
      <>
        <PageHeader title="프롬프트" back={<BackButton to="/prompts" />} />
        <LoadingList rows={2} />
      </>
    );
  if (!isNew && (q.isError || !q.data))
    return (
      <>
        <PageHeader title="프롬프트" back={<BackButton to="/prompts" />} />
        <ErrorState error={q.error} onRetry={() => q.refetch()} />
      </>
    );
  return <PromptForm key={q.data?.id ?? 'new'} prompt={isNew ? undefined : q.data} />;
}

function PromptForm({ prompt }: { prompt?: Prompt }) {
  const nav = useNavigate();
  const toast = useToast();
  const qc = useQueryClient();
  const [form, setForm] = useState<Form>(() =>
    prompt
      ? {
          name: prompt.name,
          category: prompt.category,
          description: prompt.description,
          body: prompt.version.body,
          variables: prompt.version.variables,
          defaults: { ...EMPTY.defaults, ...prompt.version.defaults },
          note: '',
        }
      : EMPTY,
  );
  const [fields, setFields] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  const [sheet, setSheet] = useState<'versions' | 'test' | null>(null);
  const found = useMemo(() => placeholders(form.body), [form.body]);
  const declared = form.variables.map((v) => v.name);
  const missing = found.filter((n) => !declared.includes(n));
  const unused = declared.filter((n) => !found.includes(n));

  const set = (p: Partial<Form>) => setForm((f) => ({ ...f, ...p }));
  const setVar = (i: number, p: Partial<PromptVariable>) => set({ variables: form.variables.map((v, j) => (j === i ? { ...v, ...p } : v)) });

  async function save() {
    setBusy(true);
    setFields({});
    try {
      const body = { ...form };
      const p = prompt ? await put<Prompt>(`/api/prompts/${prompt.id}`, body) : await post<Prompt>('/api/prompts', body);
      await qc.invalidateQueries({ queryKey: ['prompts'] });
      toast.show(prompt && p.current_version !== prompt.current_version ? `새 버전 v${p.current_version}으로 저장했어요. 이후 생성부터 적용돼요` : '저장했어요', 'success');
      if (!prompt) nav(`/prompts/${p.id}`, { replace: true });
      else set({ note: '' });
    } catch (e) {
      if (e instanceof ApiError && Object.keys(e.fields).length) {
        setFields(e.fields);
        toast.show('입력값을 확인해 주세요', 'error');
      } else toast.show(errorMessage(e), 'error');
    } finally {
      setBusy(false);
    }
  }

  async function action(kind: 'duplicate' | 'toggle') {
    if (!prompt) return;
    try {
      if (kind === 'duplicate') {
        const p = await post<Prompt>(`/api/prompts/${prompt.id}/duplicate`);
        await qc.invalidateQueries({ queryKey: ['prompts'] });
        toast.show('복제했어요', 'success');
        nav(`/prompts/${p.id}`);
      } else {
        await post<Prompt>(`/api/prompts/${prompt.id}/active`, { active: !prompt.active });
        await qc.invalidateQueries({ queryKey: ['prompts'] });
        toast.show(prompt.active ? '비활성화했어요. 자동 생성에 쓰이지 않아요' : '다시 사용해요', 'success');
      }
    } catch (e) {
      toast.show(errorMessage(e), 'error');
    }
  }

  return (
    <>
      <PageHeader
        title={prompt ? prompt.name : '새 프롬프트'}
        subtitle={prompt ? `현재 v${prompt.current_version} · 사용 ${prompt.usage_count}번` : '쓰던 프롬프트를 그대로 붙여넣으세요'}
        back={<BackButton to="/prompts" />}
      />
      {prompt?.is_example && (
        <Card className={styles.example}>
          <Badge tone="warning">예시</Badge> 앱이 넣은 예시 프롬프트예요. 실제로 쓰는 프롬프트로 바꿔 쓰세요.
        </Card>
      )}
      <div className={styles.grid}>
        <div className={styles.col}>
          <Card>
            <div className={styles.form}>
              <TextField label="이름" value={form.name} onChange={(e) => set({ name: e.target.value })} error={fields.name} maxLength={60} />
              <div className={styles.two}>
                <TextField label="카테고리" value={form.category} onChange={(e) => set({ category: e.target.value })} placeholder="소설·썰·명언" error={fields.category} />
                <TextField label="설명 (선택)" value={form.description} onChange={(e) => set({ description: e.target.value })} error={fields.description} />
              </div>
              <TextArea
                label="프롬프트"
                rows={14}
                value={form.body}
                onChange={(e) => set({ body: e.target.value })}
                error={fields.body}
                hint="바꿔 넣을 부분은 {{topic}}처럼 쓰면 생성할 때 입력칸이 생겨요."
              />
              {missing.length > 0 && (
                <div className={styles.notice}>
                  본문에 변수 {missing.map((m) => `{{${m}}}`).join(', ')}가 있어요.
                  <Button
                    size="sm"
                    variant="soft"
                    onClick={() => set({ variables: [...form.variables, ...missing.map((n) => ({ name: n, label: n, type: 'text' as const, required: true, default: '' }))] })}
                  >
                    입력칸 추가
                  </Button>
                </div>
              )}
              {unused.length > 0 && <p className={styles.muted}>본문에서 쓰지 않는 변수: {unused.join(', ')}</p>}
            </div>
          </Card>
          <Card>
            <CardTitle
              action={
                <Button size="sm" variant="ghost" icon={<Plus size={16} />} onClick={() => set({ variables: [...form.variables, { name: '', label: '', type: 'text', required: false, default: '' }] })}>
                  변수 추가
                </Button>
              }
            >
              입력칸 (변수)
            </CardTitle>
            {form.variables.length === 0 && <p className={styles.muted}>변수가 없으면 프롬프트를 그대로 사용해요.</p>}
            <div className={styles.vars}>
              {form.variables.map((v, i) => (
                <fieldset key={i} className={styles.var}>
                  <legend className="sr-only">변수 {i + 1}</legend>
                  <div className={styles.varHead}>
                    <strong>{v.label || v.name || `변수 ${i + 1}`}</strong>
                    <IconButton label="위로" disabled={i === 0} onClick={() => set({ variables: move(form.variables, i, i - 1) })}>
                      <ArrowUp size={18} />
                    </IconButton>
                    <IconButton label="아래로" disabled={i === form.variables.length - 1} onClick={() => set({ variables: move(form.variables, i, i + 1) })}>
                      <ArrowDown size={18} />
                    </IconButton>
                    <IconButton label="변수 삭제" onClick={() => set({ variables: form.variables.filter((_, j) => j !== i) })}>
                      <Trash2 size={18} />
                    </IconButton>
                  </div>
                  <div className={styles.two}>
                    <TextField label="이름 (본문의 {{이름}})" value={v.name} onChange={(e) => setVar(i, { name: e.target.value.trim() })} error={fields[`variables.${i}.name`]} />
                    <TextField label="화면 레이블" value={v.label} onChange={(e) => setVar(i, { label: e.target.value })} />
                  </div>
                  <div className={styles.two}>
                    <Select
                      label="종류"
                      value={v.type}
                      onChange={(e) => setVar(i, { type: e.target.value as PromptVariable['type'] })}
                      options={[
                        { value: 'text', label: '짧은 글' },
                        { value: 'textarea', label: '긴 글' },
                        { value: 'select', label: '선택' },
                        { value: 'number', label: '숫자' },
                      ]}
                      error={fields[`variables.${i}.type`]}
                    />
                    <TextField label="기본값" value={v.default} onChange={(e) => setVar(i, { default: e.target.value })} error={fields[`variables.${i}.default`]} />
                  </div>
                  {v.type === 'select' && (
                    <TextField
                      label="선택지 (쉼표로 구분)"
                      value={(v.options ?? []).join(', ')}
                      onChange={(e) => setVar(i, { options: e.target.value.split(',').map((s) => s.trim()).filter(Boolean) })}
                      error={fields[`variables.${i}.options`]}
                    />
                  )}
                  {(v.type === 'number' || v.type === 'text' || v.type === 'textarea') && (
                    <div className={styles.two}>
                      {v.type === 'number' && <TextField label="최솟값" type="number" inputMode="numeric" value={v.min ?? ''} onChange={(e) => setVar(i, { min: Number(e.target.value) || 0 })} />}
                      <TextField label={v.type === 'number' ? '최댓값' : '최대 글자 수'} type="number" inputMode="numeric" value={v.max ?? ''} onChange={(e) => setVar(i, { max: Number(e.target.value) || 0 })} />
                    </div>
                  )}
                  <label className={styles.req}>
                    <Switch label="필수" checked={v.required} onChange={(c) => setVar(i, { required: c })} />
                    <span>필수 입력</span>
                  </label>
                </fieldset>
              ))}
            </div>
          </Card>
        </div>
        <div className={styles.col}>
          <Card>
            <CardTitle>생성 기본값</CardTitle>
            <div className={styles.form}>
              <Select
                label="길이"
                value={form.defaults.length}
                onChange={(e) => set({ defaults: { ...form.defaults, length: e.target.value } })}
                options={[
                  { value: 'short', label: '짧게' },
                  { value: 'medium', label: '보통' },
                  { value: 'long', label: '길게' },
                ]}
              />
              <TextField label="말투" value={form.defaults.tone} onChange={(e) => set({ defaults: { ...form.defaults, tone: e.target.value } })} placeholder="예: 담담한 반말" />
              <TextField label="텍스트 모델 (비우면 서버 기본값)" value={form.defaults.model} onChange={(e) => set({ defaults: { ...form.defaults, model: e.target.value } })} />
              <Select
                label="이미지"
                value={form.defaults.image_mode}
                onChange={(e) => set({ defaults: { ...form.defaults, image_mode: e.target.value } })}
                options={[
                  { value: 'none', label: '이미지 없음' },
                  { value: 'ai', label: 'AI 이미지 생성' },
                  { value: 'upload', label: '직접 업로드' },
                ]}
              />
              {form.defaults.image_mode === 'ai' && (
                <TextArea label="이미지 프롬프트 기본값 (선택)" rows={3} value={form.defaults.image_prompt} onChange={(e) => set({ defaults: { ...form.defaults, image_prompt: e.target.value } })} />
              )}
              <Select
                label="나누는 방식"
                value={form.defaults.split_mode}
                onChange={(e) => set({ defaults: { ...form.defaults, split_mode: e.target.value } })}
                options={[
                  { value: 'two_parts', label: '본문 + 답글 1개' },
                  { value: 'overflow', label: '넘칠 때만 답글' },
                ]}
              />
              {prompt && <TextField label="변경 메모 (선택)" value={form.note} onChange={(e) => set({ note: e.target.value })} hint="새 버전이 만들어질 때 이력에 남아요" />}
            </div>
          </Card>
          <Card>
            <div className={styles.actions}>
              <Button size="lg" block busy={busy} onClick={save}>
                {prompt ? '저장' : '등록'}
              </Button>
              {prompt && (
                <>
                  <Button variant="secondary" onClick={() => setSheet('test')}>
                    입력해 보기
                  </Button>
                  <Button variant="secondary" icon={<History size={18} />} onClick={() => setSheet('versions')}>
                    변경 이력
                  </Button>
                  <Button variant="secondary" icon={<CopyPlus size={18} />} onClick={() => action('duplicate')}>
                    복제
                  </Button>
                  <Button variant={prompt.active ? 'danger' : 'secondary'} icon={<Power size={18} />} onClick={() => action('toggle')}>
                    {prompt.active ? '비활성화' : '다시 사용'}
                  </Button>
                </>
              )}
            </div>
            <p className={styles.muted}>저장한 변경은 이후 생성에만 적용돼요. 이미 만든 글은 다시 생성하지 않아요.</p>
          </Card>
        </div>
      </div>
      {prompt && <VersionsSheet open={sheet === 'versions'} onClose={() => setSheet(null)} id={prompt.id} />}
      {prompt && sheet === 'test' && <TestSheet open onClose={() => setSheet(null)} prompt={prompt} />}
    </>
  );
}

function move<T>(arr: T[], a: number, b: number): T[] {
  const n = [...arr];
  [n[a], n[b]] = [n[b]!, n[a]!];
  return n;
}

function VersionsSheet({ open, onClose, id }: { open: boolean; onClose: () => void; id: string }) {
  const q = usePromptVersions(open ? id : undefined);
  return (
    <Sheet open={open} onClose={onClose} title="변경 이력" description="어떤 버전으로 생성했는지 글마다 기록돼요" size="lg">
      {q.isLoading && <LoadingList rows={2} />}
      <ol className={styles.versions}>
        {q.data?.items.map((v) => (
          <li key={v.version} className={styles.version}>
            <div className={styles.varHead}>
              <strong>v{v.version}</strong>
              <span className={styles.muted}>{formatDateTime(v.created_at)}</span>
            </div>
            {v.note && <p className={styles.muted}>{v.note}</p>}
            <pre className={styles.body}>{v.body}</pre>
          </li>
        ))}
      </ol>
    </Sheet>
  );
}

function TestSheet({ open, onClose, prompt }: { open: boolean; onClose: () => void; prompt: Prompt }) {
  const [values, setValues] = useState<Record<string, string>>({});
  const [text, setText] = useState('');
  const [errs, setErrs] = useState<Record<string, string>>({});
  async function render() {
    try {
      const r = await post<{ text: string }>(`/api/prompts/${prompt.id}/render`, { values });
      setText(r.text);
      setErrs({});
    } catch (e) {
      if (e instanceof ApiError) setErrs(e.fields);
    }
  }
  return (
    <Sheet open={open} onClose={onClose} title="입력해 보기" description="AI를 호출하지 않고, 입력값이 들어간 프롬프트만 보여 줘요" footer={<Button onClick={render}>프롬프트 만들기</Button>}>
      <div className={styles.form}>
        {prompt.version.variables.map((v) =>
          v.type === 'select' ? (
            <Select
              key={v.name}
              label={v.label}
              value={values[v.name] ?? v.default}
              onChange={(e) => setValues({ ...values, [v.name]: e.target.value })}
              options={(v.options ?? []).map((o) => ({ value: o, label: o }))}
              error={errs[v.name]}
            />
          ) : v.type === 'textarea' ? (
            <TextArea key={v.name} label={v.label} value={values[v.name] ?? ''} placeholder={v.default} onChange={(e) => setValues({ ...values, [v.name]: e.target.value })} error={errs[v.name]} />
          ) : (
            <TextField
              key={v.name}
              label={v.label + (v.required ? ' *' : '')}
              type={v.type === 'number' ? 'number' : 'text'}
              inputMode={v.type === 'number' ? 'numeric' : undefined}
              value={values[v.name] ?? ''}
              placeholder={v.default}
              onChange={(e) => setValues({ ...values, [v.name]: e.target.value })}
              error={errs[v.name]}
              hint={v.help}
            />
          ),
        )}
        {text && <pre className={styles.body}>{text}</pre>}
      </div>
    </Sheet>
  );
}
