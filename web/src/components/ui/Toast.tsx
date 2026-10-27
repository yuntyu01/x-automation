import { CircleAlert, CircleCheck, Info } from 'lucide-react';
import { createContext, useCallback, useContext, useMemo, useRef, useState, type ReactNode } from 'react';
import styles from './Toast.module.css';

type Kind = 'success' | 'error' | 'info';
interface ToastItem {
  id: number;
  kind: Kind;
  message: string;
  action?: { label: string; onClick: () => void };
}

interface ToastApi {
  show: (message: string, kind?: Kind, action?: ToastItem['action']) => void;
}

const ToastContext = createContext<ToastApi | null>(null);

// 잠깐 보이는 토스트. 계속 남아야 하는 문제는 알림함이 맡는다
export function ToastProvider({ children }: { children: ReactNode }) {
  const [items, setItems] = useState<ToastItem[]>([]);
  const seq = useRef(0);
  const show = useCallback<ToastApi['show']>((message, kind = 'info', action) => {
    const id = ++seq.current;
    setItems((xs) => [...xs.slice(-2), { id, kind, message, action }]);
    window.setTimeout(() => setItems((xs) => xs.filter((x) => x.id !== id)), action ? 7000 : 3500);
  }, []);
  const api = useMemo(() => ({ show }), [show]);
  return (
    <ToastContext.Provider value={api}>
      {children}
      <div className={styles.stack} aria-live="polite" aria-atomic="false">
        {items.map((t) => (
          <div key={t.id} className={[styles.toast, styles[t.kind]].join(' ')} role={t.kind === 'error' ? 'alert' : 'status'}>
            {t.kind === 'success' ? <CircleCheck size={18} aria-hidden /> : t.kind === 'error' ? <CircleAlert size={18} aria-hidden /> : <Info size={18} aria-hidden />}
            <span className={styles.msg}>{t.message}</span>
            {t.action && (
              <button
                type="button"
                className={styles.action}
                onClick={() => {
                  t.action?.onClick();
                  setItems((xs) => xs.filter((x) => x.id !== t.id));
                }}
              >
                {t.action.label}
              </button>
            )}
          </div>
        ))}
      </div>
    </ToastContext.Provider>
  );
}

export function useToast(): ToastApi {
  const ctx = useContext(ToastContext);
  if (!ctx) throw new Error('ToastProvider가 필요합니다');
  return ctx;
}
