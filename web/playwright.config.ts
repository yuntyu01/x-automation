import { defineConfig, devices } from '@playwright/test';

// 브라우저 E2E: 전용 DB(xa_e2e)를 새로 만들고 목업 연동으로 실제 서버 바이너리를 띄워 검사한다.
// 실제 X 게시·AI 호출·R2 저장은 일어나지 않는다. 먼저 `make build`가 필요하다 (`make e2e`가 함께 한다).
const port = process.env.E2E_PORT ?? '18090';
// localhost는 러너에 따라 IPv6(::1)로 먼저 풀리는데 서버는 0.0.0.0(IPv4)에서 받으므로 주소를 고정한다
export const baseURL = `http://127.0.0.1:${port}`;

export default defineConfig({
  testDir: './e2e',
  fullyParallel: false,
  workers: 1, // 같은 DB를 쓰는 흐름이라 순서대로
  retries: 0,
  timeout: 60_000,
  expect: { timeout: 10_000 },
  reporter: [['list'], ['html', { open: 'never', outputFolder: 'playwright-report' }]],
  use: {
    baseURL,
    locale: 'ko-KR',
    timezoneId: 'Asia/Seoul',
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
  },
  projects: [{ name: 'mobile', use: { ...devices['Pixel 7'], viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true } }],
  webServer: {
    command: 'go run ./internal/testutil/e2eserver',
    cwd: '..',
    url: `${baseURL}/readyz`,
    reuseExistingServer: false,
    timeout: 120_000,
    stdout: 'pipe',
    stderr: 'pipe',
    env: { E2E_PORT: port },
  },
});
