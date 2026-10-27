import { useId, type InputHTMLAttributes, type ReactNode } from 'react';
import styles from './TextField.module.css';

interface Props extends Omit<InputHTMLAttributes<HTMLInputElement>, 'size'> {
  label: string;
  hint?: ReactNode;
  error?: string;
  hideLabel?: boolean;
  suffix?: ReactNode;
}

// 레이블은 항상 유지한다. placeholder만으로 의미를 전달하지 않는다
export function TextField({ label, hint, error, hideLabel, suffix, id, className, ...rest }: Props) {
  const autoId = useId();
  const inputId = id ?? autoId;
  const hintId = hint ? inputId + '-hint' : undefined;
  const errId = error ? inputId + '-err' : undefined;
  return (
    <div className={[styles.field, className ?? ''].join(' ')}>
      <label htmlFor={inputId} className={hideLabel ? 'sr-only' : styles.label}>
        {label}
      </label>
      <div className={[styles.box, error ? styles.invalid : ''].join(' ')}>
        <input
          id={inputId}
          className={styles.input}
          aria-invalid={error ? true : undefined}
          aria-describedby={[hintId, errId].filter(Boolean).join(' ') || undefined}
          {...rest}
        />
        {suffix && <span className={styles.suffix}>{suffix}</span>}
      </div>
      {hint && !error && (
        <p id={hintId} className={styles.hint}>
          {hint}
        </p>
      )}
      {error && (
        <p id={errId} className={styles.error} role="alert">
          {error}
        </p>
      )}
    </div>
  );
}
