import { useId } from 'react';
import styles from './TimeField.module.css';

interface Props {
  label: string;
  value: string; // "HH:MM" (24시간)
  onChange: (v: string) => void;
  hint?: string;
  error?: string;
  minuteStep?: number;
}

const pad = (n: number) => String(n).padStart(2, '0');

// 24시간 시각 입력. 브라우저 기본 시간 입력은 기기 언어에 따라 오전·오후(AM/PM)로 보여 시간표와 맞지 않아 시·분 선택으로 쓴다
export function TimeField({ label, value, onChange, hint, error, minuteStep = 1 }: Props) {
  const id = useId();
  const [h = '00', m = '00'] = value.split(':');
  const minutes = Array.from({ length: Math.ceil(60 / minuteStep) }, (_, i) => i * minuteStep);
  if (!minutes.includes(Number(m))) minutes.push(Number(m));
  minutes.sort((a, b) => a - b);
  const desc = error ? `${id}-err` : hint ? `${id}-hint` : undefined;
  return (
    <fieldset className={styles.field} aria-describedby={desc}>
      <legend className={styles.label}>{label}</legend>
      <div className={[styles.box, error ? styles.invalid : ''].join(' ')}>
        <select aria-label={`${label} 시`} value={pad(Number(h))} onChange={(e) => onChange(`${e.target.value}:${pad(Number(m))}`)} className={styles.select}>
          {Array.from({ length: 24 }, (_, i) => (
            <option key={i} value={pad(i)}>
              {pad(i)}시
            </option>
          ))}
        </select>
        <span className={styles.sep} aria-hidden>
          :
        </span>
        <select aria-label={`${label} 분`} value={pad(Number(m))} onChange={(e) => onChange(`${pad(Number(h))}:${e.target.value}`)} className={styles.select}>
          {minutes.map((i) => (
            <option key={i} value={pad(i)}>
              {pad(i)}분
            </option>
          ))}
        </select>
      </div>
      {error ? (
        <span id={`${id}-err`} className={styles.error} role="alert">
          {error}
        </span>
      ) : (
        hint && (
          <span id={`${id}-hint`} className={styles.hint}>
            {hint}
          </span>
        )
      )}
    </fieldset>
  );
}
