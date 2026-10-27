import { useQueryClient } from '@tanstack/react-query';
import { ChevronRight } from 'lucide-react';
import { useState } from 'react';
import { Link } from 'react-router';
import { ApiError, errorMessage, post } from '../../api/client';
import { usePrompts } from '../../api/contents';
import { useSaveSettings } from '../../api/queries';
import { useScheduleSettings } from '../../api/schedule';
import type { Prompt, Settings } from '../../api/types';
import { Button } from '../../components/ui/Button';
import { Select, TextArea } from '../../components/ui/Field';
import { TextField } from '../../components/ui/TextField';
import { useToast } from '../../components/ui/Toast';
import { minutesToHM } from '../../lib/format';
import { StepCard } from './StepCard';
import styles from './SetupPage.module.css';

const WEEKDAYS = ['일', '월', '화', '수', '목', '금', '토'];

// 프롬프트 단계: 쓰던 프롬프트를 바로 붙여넣어 등록하고, 자동 생성의 기본 프롬프트로 정한다
export function SetupPromptStep({ settings }: { settings: Settings }) {
  const prompts = usePrompts(false);
  const save = useSaveSettings();
  const qc = useQueryClient();
  const toast = useToast();
  const [name, setName] = useState('');
  const [body, setBody] = useState('');
  const [fields, setFields] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  const active = (prompts.data?.items ?? []).filter((p) => p.active);
  const current = settings.policy.generation.default_prompt_id;

  async function setDefault(id: string) {
    try {
      await save.mutateAsync({ ...settings, policy: { ...settings.policy, generation: { ...settings.policy.generation, default_prompt_id: id } } });
    } catch (e) {
      toast.show(errorMessage(e), 'error');
    }
  }

  async function add() {
    setBusy(true);
    setFields({});
    try {
      const p = await post<Prompt>('/api/prompts', {
        name: name.trim(),
        category: '',
        description: '',
        body,
        variables: [],
        defaults: { model: '', length: 'medium', tone: '', image_mode: 'none', image_prompt: '', split_mode: 'two_parts' },
        note: '첫 설정에서 등록',
      });
      await qc.invalidateQueries({ queryKey: ['prompts'] });
      if (!current) await setDefault(p.id);
      toast.show(`'${p.name}' 프롬프트를 등록했어요`, 'success');
      setName('');
      setBody('');
    } catch (e) {
      if (e instanceof ApiError && Object.keys(e.fields).length) setFields(e.fields);
      else toast.show(errorMessage(e), 'error');
    } finally {
      setBusy(false);
    }
  }

  return (
    <StepCard title="쓰던 프롬프트를 등록해요" desc="기존 프롬프트를 그대로 붙여넣으면 돼요. 직접 쓴 글만 올린다면 건너뛰어도 괜찮아요.">
      {active.length > 0 && (
        <Select
          label="자동 생성에 쓸 기본 프롬프트"
          value={current}
          onChange={(e) => void setDefault(e.target.value)}
          options={[{ value: '', label: '정하지 않음 (자동 생성 안 함)' }, ...active.map((p) => ({ value: p.id, label: p.name }))]}
        />
      )}
      <TextField label="프롬프트 이름" value={name} onChange={(e) => setName(e.target.value)} placeholder="예: 새벽 감성 썰" error={fields.name} />
      <TextArea
        label="프롬프트 내용"
        rows={6}
        value={body}
        onChange={(e) => setBody(e.target.value)}
        error={fields.body}
        hint="{{주제}} 같은 변수의 기본값은 프롬프트 화면에서 정해요"
      />
      <div className={styles.inlineActions}>
        <Button variant="soft" busy={busy} disabled={!name.trim() || !body.trim()} onClick={add}>
          프롬프트 등록
        </Button>
        <Link to="/prompts" className={styles.link}>
          프롬프트 관리 <ChevronRight size={18} aria-hidden />
        </Link>
      </div>
    </StepCard>
  );
}

// 시간표 단계: 지금 허용 구간과 하루 묶음 수를 보여 주고 바로 고칠 수 있게 연결한다
export function SetupScheduleStep() {
  const s = useScheduleSettings();
  const r = s.data?.rules;
  return (
    <StepCard title="언제 올릴지 정해요" desc="새벽·낮처럼 여러 구간을 둘 수 있어요. 직접 올리는 시간은 비워 두면 돼요.">
      {s.data && r && (
        <dl className={styles.summary}>
          <dt>하루 묶음 수</dt>
          <dd>{r.daily_target}묶음</dd>
          <dt>간격</dt>
          <dd>
            {r.base_interval_min}분 + 추가 지연 {r.jitter_min_min}~{r.jitter_max_min}분
          </dd>
          {s.data.windows
            .filter((w) => w.enabled)
            .map((w) => (
              <div key={w.id ?? w.label} className={styles.summaryRow}>
                <dt>{w.label || '구간'}</dt>
                <dd>
                  {minutesToHM(w.start_minute)}~{minutesToHM(w.end_minute)} · {w.weekdays.length === 7 ? '매일' : w.weekdays.map((d) => WEEKDAYS[d]).join('·')}
                </dd>
              </div>
            ))}
        </dl>
      )}
      <Link to="/schedule/rules" className={styles.link}>
        시간표 바꾸기 <ChevronRight size={18} aria-hidden />
      </Link>
    </StepCard>
  );
}
