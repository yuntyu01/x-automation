import { useQueryClient } from '@tanstack/react-query';
import { CircleAlert, CircleCheck, Download, FileUp } from 'lucide-react';
import { useState, type ChangeEvent } from 'react';
import { apiUrl, errorMessage, post } from '../../api/client';
import { SPLIT_LABEL, type SplitMode } from '../../api/types';
import { Button } from '../../components/ui/Button';
import { Select, TextArea } from '../../components/ui/Field';
import { Sheet } from '../../components/ui/Sheet';
import { useToast } from '../../components/ui/Toast';
import styles from './ImportSheet.module.css';

interface Mapping {
  title: string;
  category: string;
  tags: string;
  text: string;
  split_mode: string;
  parts: string[];
}

interface ParseResult {
  headers: string[];
  sample: string[][];
  row_count: number;
  mapping: Mapping;
}

interface RowResult {
  row: number;
  ok: boolean;
  errors: Record<string, string>;
  title: string;
  category: string;
  part_count: number;
  note: string;
  publishable: boolean;
  problems: string[];
}

const FIELD_LABEL: Record<keyof Omit<Mapping, 'parts'>, string> = {
  title: '제목',
  category: '카테고리',
  tags: '태그',
  text: '원문',
  split_mode: '나누는 방식',
};

export function ImportSheet({ open, onClose }: { open: boolean; onClose: () => void }) {
  const [step, setStep] = useState<'file' | 'map' | 'preview'>('file');
  const [format, setFormat] = useState<'csv' | 'json'>('csv');
  const [data, setData] = useState('');
  const [parsed, setParsed] = useState<ParseResult | null>(null);
  const [mapping, setMapping] = useState<Mapping | null>(null);
  const [splitMode, setSplitMode] = useState<SplitMode>('two_parts');
  const [preview, setPreview] = useState<{ rows: RowResult[]; valid: number; invalid: number } | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const toast = useToast();
  const qc = useQueryClient();

  function reset() {
    setStep('file');
    setData('');
    setParsed(null);
    setMapping(null);
    setPreview(null);
    setError('');
  }

  async function onFile(e: ChangeEvent<HTMLInputElement>) {
    const f = e.target.files?.[0];
    if (!f) return;
    if (f.size > 2 * 1024 * 1024) {
      setError('파일이 2MB를 넘어요. 나눠서 가져와 주세요');
      return;
    }
    setFormat(f.name.toLowerCase().endsWith('.json') ? 'json' : 'csv');
    setData(await f.text());
    setError('');
  }

  async function parse() {
    setBusy(true);
    setError('');
    try {
      const r = await post<ParseResult>('/api/contents/import/parse', { format, data });
      setParsed(r);
      setMapping({ ...r.mapping, parts: r.mapping.parts ?? [] });
      setStep('map');
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  }

  async function runPreview() {
    setBusy(true);
    setError('');
    try {
      const r = await post<{ rows: RowResult[]; valid: number; invalid: number }>('/api/contents/import/preview', {
        format,
        data,
        mapping,
        defaults: { split_mode: splitMode, category: '' },
      });
      setPreview(r);
      setStep('preview');
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  }

  async function commit() {
    setBusy(true);
    try {
      const r = await post<{ imported: number; skipped: RowResult[] }>('/api/contents/import/commit', {
        format,
        data,
        mapping,
        defaults: { split_mode: splitMode, category: '' },
      });
      await qc.invalidateQueries({ queryKey: ['contents'] });
      toast.show(r.skipped.length ? `${r.imported}개 등록, 오류 ${r.skipped.length}행은 건너뛰었어요` : `${r.imported}개를 등록했어요`, 'success');
      reset();
      onClose();
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  }

  const headerOptions = [{ value: '', label: '사용 안 함' }, ...(parsed?.headers ?? []).map((h) => ({ value: h, label: h }))];

  return (
    <Sheet
      open={open}
      onClose={() => {
        reset();
        onClose();
      }}
      title="CSV·JSON 가져오기"
      size="lg"
      dismissible={!busy}
      footer={
        step === 'file' ? (
          <Button busy={busy} disabled={!data.trim()} onClick={parse}>
            다음: 필드 연결
          </Button>
        ) : step === 'map' ? (
          <>
            <Button variant="secondary" onClick={() => setStep('file')}>
              이전
            </Button>
            <Button busy={busy} disabled={!mapping?.text && !mapping?.parts.length} onClick={runPreview}>
              미리보기
            </Button>
          </>
        ) : (
          <>
            <Button variant="secondary" onClick={() => setStep('map')}>
              이전
            </Button>
            <Button busy={busy} disabled={!preview?.valid} onClick={commit}>
              {preview?.valid ?? 0}개 등록
            </Button>
          </>
        )
      }
    >
      {error && (
        <p className={styles.error} role="alert">
          {error}
        </p>
      )}
      {step === 'file' && (
        <div className={styles.stack}>
          <div className={styles.templates}>
            <span>양식 받기</span>
            <a href={apiUrl('/api/contents/import/template?format=csv')} download>
              <Download size={16} aria-hidden /> CSV 양식
            </a>
            <a href={apiUrl('/api/contents/import/template?format=json')} download>
              <Download size={16} aria-hidden /> JSON 양식
            </a>
          </div>
          <label className={styles.drop}>
            <FileUp size={24} aria-hidden />
            <span>CSV(UTF-8) 또는 JSON 파일 선택</span>
            <input type="file" accept=".csv,.json,text/csv,application/json" onChange={onFile} className="sr-only" />
          </label>
          <Select
            label="형식"
            value={format}
            onChange={(e) => setFormat(e.target.value as 'csv' | 'json')}
            options={[
              { value: 'csv', label: 'CSV (첫 줄은 열 이름)' },
              { value: 'json', label: 'JSON 배열' },
            ]}
          />
          <TextArea label="또는 내용 붙여넣기" rows={6} value={data} onChange={(e) => setData(e.target.value)} placeholder={'title,category,text\n비 오는 밤,소설,"첫 문단..."'} />
        </div>
      )}
      {step === 'map' && parsed && mapping && (
        <div className={styles.stack}>
          <p className={styles.muted}>
            {parsed.row_count}행을 찾았어요. 항목마다 쓸 열을 골라 주세요.
          </p>
          {(Object.keys(FIELD_LABEL) as (keyof typeof FIELD_LABEL)[]).map((k) => (
            <Select key={k} label={FIELD_LABEL[k]} value={mapping[k]} onChange={(e) => setMapping({ ...mapping, [k]: e.target.value })} options={headerOptions} />
          ))}
          <fieldset className={styles.parts}>
            <legend>직접 나눈 부분 열 (선택)</legend>
            <p className={styles.muted}>본문·답글이 열로 나뉘어 있으면 순서대로 골라 주세요. 고르면 원문 대신 이 열들로 직접 나누기 등록해요.</p>
            {parsed.headers.map((h) => (
              <label key={h} className={styles.partOpt}>
                <input
                  type="checkbox"
                  checked={mapping.parts.includes(h)}
                  onChange={(e) => setMapping({ ...mapping, parts: e.target.checked ? [...mapping.parts, h] : mapping.parts.filter((p) => p !== h) })}
                />
                <span>{h}</span>
                {mapping.parts.includes(h) && <span className={styles.order}>{mapping.parts.indexOf(h) + 1}번째</span>}
              </label>
            ))}
          </fieldset>
          <Select
            label="나누는 방식 열이 없을 때 기본값"
            value={splitMode}
            onChange={(e) => setSplitMode(e.target.value as SplitMode)}
            options={(['two_parts', 'overflow', 'manual'] as SplitMode[]).map((m) => ({ value: m, label: SPLIT_LABEL[m] }))}
          />
        </div>
      )}
      {step === 'preview' && preview && (
        <div className={styles.stack}>
          <p className={styles.muted}>
            등록 가능 {preview.valid}개 · 오류 {preview.invalid}개 (오류 행은 건너뛰어요)
          </p>
          <ul className={styles.rows}>
            {preview.rows.map((r) => (
              <li key={r.row} className={[styles.row, r.ok ? '' : styles.bad].join(' ')}>
                {r.ok ? <CircleCheck size={18} aria-hidden className={styles.ok} /> : <CircleAlert size={18} aria-hidden className={styles.badIcon} />}
                <div className={styles.rowBody}>
                  <span className={styles.rowTitle}>
                    {r.row}행 · {r.title || '(제목 없음)'}
                  </span>
                  {r.ok ? (
                    <span className={styles.muted}>
                      {r.note} {!r.publishable && r.problems.length > 0 ? `· ${r.problems[0]}` : ''}
                    </span>
                  ) : (
                    Object.entries(r.errors).map(([k, v]) => (
                      <span key={k} className={styles.errText}>
                        {v}
                      </span>
                    ))
                  )}
                </div>
                <span className="sr-only">{r.ok ? '등록 가능' : '오류'}</span>
              </li>
            ))}
          </ul>
        </div>
      )}
    </Sheet>
  );
}
