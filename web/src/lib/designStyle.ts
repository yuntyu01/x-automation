import { useEffect, useSyncExternalStore } from 'react';

// 디자인 스타일 시험: 기본은 토스 스타일. 노션 스타일은 홈·콘텐츠·편집기 세 화면에만 적용해 비교한다
export type DesignStyle = 'toss' | 'notion';
const KEY = 'xa-design-style';
const EVENT = 'xa-design-style';

function read(): DesignStyle {
  try {
    return localStorage.getItem(KEY) === 'notion' ? 'notion' : 'toss';
  } catch {
    return 'toss';
  }
}

function subscribe(fn: () => void) {
  window.addEventListener(EVENT, fn);
  window.addEventListener('storage', fn);
  return () => {
    window.removeEventListener(EVENT, fn);
    window.removeEventListener('storage', fn);
  };
}

export function setDesignStyle(s: DesignStyle) {
  try {
    localStorage.setItem(KEY, s);
  } catch {
    // 저장이 막혀도 이번 화면에서는 바꾼다
  }
  window.dispatchEvent(new Event(EVENT));
}

export function useDesignStyle(): DesignStyle {
  return useSyncExternalStore(subscribe, read, () => 'toss');
}

// 노션 스타일 화면이 떠 있는 동안 <html data-style="notion">으로 토큰을 바꾼다. 다른 화면으로 가면 원래대로
export function useNotionScope(active: boolean) {
  useEffect(() => {
    if (!active) return;
    const root = document.documentElement;
    root.dataset.style = 'notion';
    return () => {
      delete root.dataset.style;
    };
  }, [active]);
}
