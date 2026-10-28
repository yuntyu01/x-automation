import { Bell, CalendarClock, FileText, House, Settings as SettingsIcon } from 'lucide-react';
import { useEffect, useState, type ReactNode } from 'react';
import { NavLink, Outlet, useLocation } from 'react-router';
import { useNotificationCounts, useRuntime } from '../../api/queries';
import { AppMark } from '../AppMark';
import styles from './AppShell.module.css';

const TABS = [
  { to: '/', label: '홈', icon: House, end: true },
  { to: '/contents', label: '콘텐츠', icon: FileText, end: false },
  { to: '/schedule', label: '예약', icon: CalendarClock, end: false },
  { to: '/settings', label: '설정', icon: SettingsIcon, end: false },
];

function useOnline() {
  const [online, setOnline] = useState(() => (typeof navigator === 'undefined' ? true : navigator.onLine));
  useEffect(() => {
    const on = () => setOnline(true);
    const off = () => setOnline(false);
    window.addEventListener('online', on);
    window.addEventListener('offline', off);
    return () => {
      window.removeEventListener('online', on);
      window.removeEventListener('offline', off);
    };
  }, []);
  return online;
}

export function AppShell() {
  const runtime = useRuntime();
  const online = useOnline();
  const loc = useLocation();
  // 상세 편집처럼 전체 화면을 쓰는 경로에서는 하단 탭을 숨긴다
  const immersive = /^\/contents\/[^/]+/.test(loc.pathname) || loc.pathname.startsWith('/setup');

  return (
    <div className={styles.shell}>
      <a href="#main" className={styles.skip}>
        본문으로 건너뛰기
      </a>
      <nav className={styles.side} aria-label="주 메뉴">
        <div className={styles.brand}>
          <AppMark size={32} />
          <span>X 자동 게시</span>
        </div>
        {TABS.map((t) => (
          <NavLink key={t.to} to={t.to} end={t.end} className={({ isActive }) => [styles.sideItem, isActive ? styles.active : ''].join(' ')}>
            <t.icon size={22} aria-hidden />
            <span>{t.label}</span>
          </NavLink>
        ))}
        <NavLink to="/notifications" className={({ isActive }) => [styles.sideItem, isActive ? styles.active : ''].join(' ')}>
          <Bell size={22} aria-hidden />
          <span>알림</span>
          <UnreadDot />
        </NavLink>
      </nav>
      <div className={styles.main}>
        {runtime.data?.integration_mode === 'mock' && (
          <div className={styles.banner} role="note">
            <strong>목업 모드</strong> 실제로 게시·생성·저장하지 않아요
          </div>
        )}
        {!online && (
          <div className={[styles.banner, styles.offline].join(' ')} role="alert">
            인터넷 연결이 끊겼어요. 저장과 게시 요청이 전송되지 않아요
          </div>
        )}
        <main id="main" className={[styles.content, immersive ? styles.immersive : ''].join(' ')} tabIndex={-1}>
          <Outlet />
        </main>
      </div>
      {!immersive && (
        <nav className={styles.tabbar} aria-label="하단 메뉴">
          {TABS.map((t) => (
            <NavLink key={t.to} to={t.to} end={t.end} className={({ isActive }) => [styles.tab, isActive ? styles.tabActive : ''].join(' ')}>
              <t.icon size={24} aria-hidden />
              <span>{t.label}</span>
            </NavLink>
          ))}
        </nav>
      )}
    </div>
  );
}

function UnreadDot() {
  const counts = useNotificationCounts();
  const n = counts.data?.unread ?? 0;
  if (!n) return null;
  return (
    <span className={styles.count} aria-label={`읽지 않은 알림 ${n}개`}>
      {n > 99 ? '99+' : n}
    </span>
  );
}

// 화면 상단: 제목·필요한 작업·알림 아이콘
// icon: 노션 스타일 화면의 페이지 아이콘(이모지)
export function PageHeader({ title, subtitle, actions, back, icon }: { title: string; subtitle?: ReactNode; actions?: ReactNode; back?: ReactNode; icon?: string }) {
  return (
    <header className={[styles.header, icon ? styles.withIcon : ''].join(' ')}>
      {back}
      <div className={styles.headTitles}>
        {icon && (
          <span className={styles.pageIcon} aria-hidden>
            {icon}
          </span>
        )}
        <h1 className={styles.title}>{title}</h1>
        {subtitle && <p className={styles.subtitle}>{subtitle}</p>}
      </div>
      <div className={styles.headActions}>
        {actions}
        <NotificationBell />
      </div>
    </header>
  );
}

function NotificationBell() {
  const counts = useNotificationCounts();
  const n = counts.data?.unread ?? 0;
  return (
    <NavLink to="/notifications" className={styles.bell} aria-label={n ? `알림 (읽지 않음 ${n}개)` : '알림'}>
      <Bell size={22} aria-hidden />
      {n > 0 && <span className={styles.bellBadge}>{n > 99 ? '99+' : n}</span>}
    </NavLink>
  );
}
