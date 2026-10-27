import { useRef, type KeyboardEvent } from 'react';
import styles from './Segmented.module.css';

interface Option<T extends string> {
  value: T;
  label: string;
  count?: number;
}

interface Props<T extends string> {
  options: Option<T>[];
  value: T;
  onChange: (v: T) => void;
  label: string;
  scroll?: boolean;
}

// 탭형 선택. 화살표 키로 이동한다
export function Segmented<T extends string>({ options, value, onChange, label, scroll }: Props<T>) {
  const refs = useRef<(HTMLButtonElement | null)[]>([]);
  function onKey(e: KeyboardEvent, i: number) {
    if (e.key !== 'ArrowRight' && e.key !== 'ArrowLeft') return;
    const n = e.key === 'ArrowRight' ? (i + 1) % options.length : (i - 1 + options.length) % options.length;
    e.preventDefault();
    onChange(options[n]!.value);
    refs.current[n]?.focus();
  }
  return (
    <div className={[styles.wrap, scroll ? styles.scroll : ''].join(' ')} role="tablist" aria-label={label}>
      {options.map((o, i) => (
        <button
          key={o.value}
          ref={(el) => {
            refs.current[i] = el;
          }}
          type="button"
          role="tab"
          aria-selected={o.value === value}
          tabIndex={o.value === value ? 0 : -1}
          className={[styles.item, o.value === value ? styles.active : ''].join(' ')}
          onClick={() => onChange(o.value)}
          onKeyDown={(e) => onKey(e, i)}
        >
          {o.label}
          {o.count !== undefined && <span className={styles.count}>{o.count}</span>}
        </button>
      ))}
    </div>
  );
}
