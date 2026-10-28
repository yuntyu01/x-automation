import { existsSync, writeFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import react from '@vitejs/plugin-react';
import type { Plugin } from 'vite';
import { defineConfig } from 'vitest/config';

// Go 서버가 embed.FS로 읽는 위치. 빌드마다 비우므로 .gitkeep을 다시 남긴다
const outDir = fileURLToPath(new URL('../internal/web/dist', import.meta.url));

function keepDir(): Plugin {
  return {
    name: 'keep-embed-dir',
    closeBundle() {
      if (existsSync(outDir)) writeFileSync(`${outDir}/.gitkeep`, '');
    },
  };
}

export default defineConfig({
  plugins: [react(), keepDir()],
  build: {
    outDir,
    emptyOutDir: true,
    sourcemap: false,
    chunkSizeWarningLimit: 900,
  },
  server: {
    port: 5173,
    // 개발 환경에서만 API를 Go 서버로 넘긴다. 운영은 같은 origin에서 Go가 제공한다
    proxy: {
      '/api': { target: 'http://127.0.0.1:8080', changeOrigin: false },
      '/healthz': 'http://127.0.0.1:8080',
      '/readyz': 'http://127.0.0.1:8080',
    },
  },
  test: {
    environment: 'node',
    include: ['src/**/*.test.ts'],
    passWithNoTests: true,
  },
});
