import { useState } from 'react';
import { Link } from 'react-router';
import { ApiError, errorMessage } from '../../api/client';
import { usePrompt, usePrompts } from '../../api/contents';
import { newRequestKey, useEstimate, useSubmitGeneration } from '../../api/generation';
import { useSettings } from '../../api/queries';
import type { GenerationOptions } from '../../api/types';
import { formatMoney } from '../../lib/format';
import { VariablesForm } from '../prompt/VariablesForm';
import { Button } from '../ui/Button';
import { Select } from '../ui/Field';
import { Row, RowGroup } from '../ui/Row';
import { Segmented } from '../ui/Segmented';
import { Switch } from '../ui/Switch';
import { TextField } from '../ui/TextField';
import { useToast } from '../ui/Toast';
import styles from './generation.module.css';

type Qty = '1' | '5' | '20' | 'custom';
type Length = '' | 'short' | 'medium' | 'long';

const LENGTH_OPTIONS: { value: Length; label: string }[] = [
  { value: '', label: '프롬프트 기본' },
  { value: 'short', label: '짧게' },
  { value: 'medium', label: '보통' },
  { value: 'long', label: '길게' },
];

// 버튼으로 생성: 1·5·20·직접 수량. 기본은 저장까지이고, 빈 예약 배치는 따로 고른다 ("지금 게시"와 분리)
export function GenerateForm({ onSubmitted }: { onSubmitted: () => void }) {
  const settings = useSettings();
  const prompts = usePrompts(false);
  const submit = useSubmitGeneration();
  const toast = useToast();
  const g = settings.data?.policy.generation;
  const publishOnly = settings.data?.operation_mode === 'publish_only';
  const textAllowed = !!g?.text_enabled && !publishOnly;
  const imageAllowed = !!g?.image_enabled;

  const active = prompts.data?.items.filter((p) => p.active) ?? [];
  const [promptId, setPromptId] = useState('');
  const pid = promptId || g?.default_prompt_id || active[0]?.id || '';
  const prompt = usePrompt(pid || undefined);
  const [values, setValues] = useState<Record<string, string>>({});
  const [qty, setQty] = useState<Qty>('1');
  const [custom, setCustom] = useState('10');
  const [wantText, setWantText] = useState(true);
  const [wantImage, setWantImage] = useState(false);
  const [length, setLength] = useState<Length>('');
  const [tone, setTone] = useState('');
  const [imagePrompt, setImagePrompt] = useState('');
  const [imageCount, setImageCount] = useState('1');
  const [fallback, setFallback] = useState(false);
  const [place, setPlace] = useState(false);
  const [fields, setFields] = useState<Record<string, string>>({});
  // 같은 내용으로 두 번 눌러도 요청은 하나: 입력이 바뀔 때만 새 키를 쓴다
  const [key, setKey] = useState(() => newRequestKey('manual'));

  const text = textAllowed && wantText;
  const image = imageAllowed && wantImage;
  const quantity = qty === 'custom' ? Number(custom) || 0 : Number(qty);
  const options: GenerationOptions = {
    text,
    image,
    length,
    tone: tone.trim(),
    image_count: Number(imageCount),
    image_prompt: imagePrompt.trim(),
    text_only_fallback: image && text && fallback,
  };
  const estimate = useEstimate(quantity, options, text || image);

  const touch = <T,>(fn: (v: T) => void) => (v: T) => {
    fn(v);
    setKey(newRequestKey('manual'));
    setFields({});
  };

  async function go() {
    setFields({});
    try {
      const r = await submit.mutateAsync({ idempotency_key: key, prompt_id: text ? pid : '', variables: values, options, quantity, place_into_slots: place });
      toast.show(r.state === 'queued' ? `${r.quantity}개 생성을 시작했어요. 진행 상황은 콘텐츠 화면 위에 보여요` : '이미 같은 요청이 있어요', 'success');
      setKey(newRequestKey('manual'));
      onSubmitted();
    } catch (e) {
      if (e instanceof ApiError && Object.keys(e.fields).length) {
        setFields(e.fields);
        toast.show('입력값을 확인해 주세요', 'error');
      } else toast.show(errorMessage(e), 'error');
    }
  }

  const varErrors = Object.fromEntries(
    Object.entries(fields)
      .filter(([k]) => k.startsWith('variables.'))
      .map(([k, v]) => [k.slice('variables.'.length), v]),
  );

  if (settings.data && !textAllowed && !imageAllowed) {
    return (
      <div className={styles.notice}>
        <p>{publishOnly ? '게시만 자동 모드에서는 글을 생성하지 않아요.' : '글 생성과 이미지 생성이 모두 꺼져 있어요.'}</p>
        <Link to="/settings/generation">생성 설정 열기</Link>
      </div>
    );
  }

  return (
    <div className={styles.form}>
      {publishOnly && <p className={styles.notice}>게시만 자동 모드라 글은 만들지 않고 이미지만 만들 수 있어요.</p>}
      <RowGroup>
        <Row
          label="글 생성"
          description={textAllowed ? '프롬프트로 본문과 답글이 될 글을 만들어요' : publishOnly ? '게시만 자동 모드에서는 꺼져 있어요' : '생성 설정에서 꺼져 있어요'}
          trailing={<Switch label="글 생성" checked={text} disabled={!textAllowed} onChange={touch(setWantText)} />}
        />
        <Row
          label="이미지 생성"
          description={imageAllowed ? (text ? '글과 함께 이미지를 만들어 붙여요' : '이미지만 만들어 보관함에 저장해요') : '생성 설정에서 꺼져 있어요'}
          trailing={<Switch label="이미지 생성" checked={image} disabled={!imageAllowed} onChange={touch(setWantImage)} />}
        />
      </RowGroup>

      {text && (
        <>
          {active.length === 0 ? (
            <p className={styles.notice}>
              활성화된 프롬프트가 없어요. <Link to="/prompts">프롬프트 만들기</Link>
            </p>
          ) : (
            <Select
              label="프롬프트"
              value={pid}
              onChange={(e) => {
                setPromptId(e.target.value);
                setValues({});
                setKey(newRequestKey('manual'));
              }}
              options={active.map((p) => ({ value: p.id, label: p.name + (p.id === g?.default_prompt_id ? ' (자동 생성 기본)' : '') }))}
              error={fields.prompt_id}
            />
          )}
          {prompt.data && prompt.data.version.variables.length > 0 && (
            <VariablesForm variables={prompt.data.version.variables} values={values} onChange={touch(setValues)} errors={varErrors} />
          )}
          <div className={styles.field}>
            <span className={styles.label}>길이</span>
            <Segmented label="길이" value={length} onChange={touch(setLength)} options={LENGTH_OPTIONS} />
          </div>
          <TextField label="말투 (선택)" value={tone} onChange={(e) => touch(setTone)(e.target.value)} placeholder="예: 담담한 1인칭, 반말" />
        </>
      )}

      {image && (
        <>
          <TextField
            label={text ? '이미지 설명 (선택)' : '이미지 설명 *'}
            value={imagePrompt}
            onChange={(e) => touch(setImagePrompt)(e.target.value)}
            placeholder={text ? '비우면 생성된 글에 맞춰 만들어요' : '예: 비 오는 밤 골목의 가로등'}
            error={fields['options.image_prompt']}
          />
          <Select
            label="묶음당 이미지 수"
            value={imageCount}
            onChange={(e) => touch(setImageCount)(e.target.value)}
            options={['1', '2', '3', '4'].map((n) => ({ value: n, label: `${n}장` }))}
          />
          {text && (
            <RowGroup>
              <Row
                label="이미지가 실패하면 텍스트만으로 준비"
                description="끄면 이미지가 없는 글은 게시되지 않고 '작성 중'으로 남아요"
                trailing={<Switch label="이미지 실패 시 텍스트만" checked={fallback} onChange={touch(setFallback)} />}
              />
            </RowGroup>
          )}
        </>
      )}

      <div className={styles.field}>
        <span className={styles.label}>수량</span>
        <Segmented
          label="수량"
          value={qty}
          onChange={touch(setQty)}
          options={[
            { value: '1', label: '1개' },
            { value: '5', label: '5개' },
            { value: '20', label: '20개' },
            { value: 'custom', label: '직접' },
          ]}
        />
        {qty === 'custom' && (
          <TextField label="직접 입력 (1~100)" type="number" inputMode="numeric" min={1} max={100} value={custom} onChange={(e) => touch(setCustom)(e.target.value)} error={fields.quantity} />
        )}
      </div>

      {text && (
        <RowGroup>
          <Row
            label="생성 후 빈 예약에 배치"
            description="끄면 보관함에 저장만 해요. 바로 게시하지는 않아요"
            trailing={<Switch label="생성 후 빈 예약에 배치" checked={place} onChange={touch(setPlace)} />}
          />
        </RowGroup>
      )}

      <div className={styles.summary} aria-live="polite">
        <strong>
          {quantity > 0 ? `${quantity}개` : '수량을 입력하세요'}
          {text ? ' 글' : ''}
          {image ? (text ? ' + 이미지' : ' 이미지 묶음') : ''}
        </strong>
        <span>
          추정 비용:{' '}
          {estimate.data ? (estimate.data.pricing_known ? formatMoney(estimate.data.micros, estimate.data.currency) : '단가 미확인 (설정에서 요율표를 확인하면 보여요)') : '…'}
        </span>
        {settings.data?.generation_paused && <span className={styles.warnText}>생성이 일시정지되어 있어 재개하면 시작돼요</span>}
        {fields['options.text'] && <span className={styles.warnText}>{fields['options.text']}</span>}
        {fields['options.image'] && <span className={styles.warnText}>{fields['options.image']}</span>}
        {fields.options && <span className={styles.warnText}>{fields.options}</span>}
      </div>
      <Button size="lg" block busy={submit.isPending} disabled={quantity < 1 || quantity > 100 || (!text && !image) || (text && !pid)} onClick={go}>
        {quantity > 0 ? `${quantity}개 생성하기` : '생성하기'}
      </Button>
    </div>
  );
}
