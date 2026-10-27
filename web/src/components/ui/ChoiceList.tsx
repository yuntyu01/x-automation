import { Check } from 'lucide-react';
import styles from './ChoiceList.module.css';

interface Choice<T extends string> {
  value: T;
  label: string;
  description?: string;
  disabled?: boolean;
}

// 라디오 목록 (설정 시트 안에서 하나 고르기)
export function ChoiceList<T extends string>({ name, value, options, onChange }: { name: string; value: T; options: Choice<T>[]; onChange: (v: T) => void }) {
  return (
    <div className={styles.list} role="radiogroup" aria-label={name}>
      {options.map((o) => (
        <label key={o.value} className={[styles.item, o.value === value ? styles.selected : '', o.disabled ? styles.disabled : ''].join(' ')}>
          <input type="radio" name={name} value={o.value} checked={o.value === value} disabled={o.disabled} onChange={() => onChange(o.value)} className="sr-only" />
          <span className={styles.text}>
            <span className={styles.label}>{o.label}</span>
            {o.description && <span className={styles.desc}>{o.description}</span>}
          </span>
          <span className={styles.check} aria-hidden>
            {o.value === value && <Check size={18} />}
          </span>
        </label>
      ))}
    </div>
  );
}
