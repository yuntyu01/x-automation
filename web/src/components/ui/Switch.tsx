import styles from './Switch.module.css';

interface Props {
  checked: boolean;
  onChange: (v: boolean) => void;
  label: string; // 스크린리더용 (화면에 보이는 레이블은 행이 담당)
  disabled?: boolean;
}

export function Switch({ checked, onChange, label, disabled }: Props) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      aria-label={label}
      disabled={disabled}
      className={[styles.switch, checked ? styles.on : ''].join(' ')}
      onClick={() => onChange(!checked)}
    >
      <span className={styles.thumb} />
    </button>
  );
}
