import { useId, type ReactNode, type SelectHTMLAttributes, type TextareaHTMLAttributes } from 'react';
import styles from './TextField.module.css';
import own from './Field.module.css';

export function FieldShell({ label, hint, error, children, id, hideLabel }: { label: string; hint?: ReactNode; error?: string; children: ReactNode; id: string; hideLabel?: boolean }) {
  return (
    <div className={styles.field}>
      <label htmlFor={id} className={hideLabel ? 'sr-only' : styles.label}>
        {label}
      </label>
      {children}
      {hint && !error && <p className={styles.hint}>{hint}</p>}
      {error && (
        <p className={styles.error} role="alert">
          {error}
        </p>
      )}
    </div>
  );
}

interface SelectProps extends SelectHTMLAttributes<HTMLSelectElement> {
  label: string;
  hint?: ReactNode;
  error?: string;
  options: { value: string; label: string }[];
}

export function Select({ label, hint, error, options, id, ...rest }: SelectProps) {
  const auto = useId();
  const sid = id ?? auto;
  return (
    <FieldShell label={label} hint={hint} error={error} id={sid}>
      <select id={sid} className={[own.select, error ? own.invalid : ''].join(' ')} aria-invalid={error ? true : undefined} {...rest}>
        {options.map((o) => (
          <option key={o.value} value={o.value}>
            {o.label}
          </option>
        ))}
      </select>
    </FieldShell>
  );
}

interface AreaProps extends TextareaHTMLAttributes<HTMLTextAreaElement> {
  label: string;
  hideLabel?: boolean; // 카드 제목이 같은 이름을 이미 보여 줄 때

  hint?: ReactNode;
  error?: string;
}

export function TextArea({ label, hint, error, id, className, hideLabel, ...rest }: AreaProps) {
  const auto = useId();
  const tid = id ?? auto;
  return (
    <FieldShell label={label} hint={hint} error={error} id={tid} hideLabel={hideLabel}>
      <textarea id={tid} className={[own.area, error ? own.invalid : '', className ?? ''].join(' ')} aria-invalid={error ? true : undefined} {...rest} />
    </FieldShell>
  );
}
