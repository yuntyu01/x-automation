import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';

function Bootstrap() {
  return (
    <main style={{ fontFamily: 'system-ui, sans-serif', padding: 24 }}>
      <h1>X 자동 게시</h1>
      <p>실행 기반만 준비된 상태입니다.</p>
    </main>
  );
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <Bootstrap />
  </StrictMode>,
);
