import { expect, type Page, type TestInfo } from '@playwright/test';

export const PASSWORD = process.env.E2E_PASSWORD ?? 'e2e-test-password';

// 콘솔 오류·화면 예외를 모아 테스트 끝에 확인한다 (의도한 409 확인 요청은 제외)
export function watchErrors(page: Page): string[] {
  const errors: string[] = [];
  page.on('pageerror', (e) => errors.push('pageerror: ' + e.message));
  page.on('console', (m) => {
    if (m.type() === 'error' && !/409|401/.test(m.text())) errors.push(m.text());
  });
  return errors;
}

export async function login(page: Page, next = '/') {
  await page.goto('/login?next=' + encodeURIComponent(next));
  await page.getByLabel('아이디').fill('admin');
  await page.getByLabel('비밀번호').fill(PASSWORD);
  await page.getByRole('button', { name: '로그인' }).click();
  await page.waitForURL((u) => u.pathname === next);
}

export async function expectNoHorizontalOverflow(page: Page, label: string) {
  const { sw, cw } = await page.evaluate(() => ({ sw: document.documentElement.scrollWidth, cw: document.documentElement.clientWidth }));
  expect(sw, `${label}: 가로 넘침 (${sw} > ${cw})`).toBeLessThanOrEqual(cw);
}

export async function shot(page: Page, info: TestInfo, name: string) {
  await info.attach(name, { body: await page.screenshot({ fullPage: true }), contentType: 'image/png' });
  if (process.env.E2E_SCREENSHOT_DIR) await page.screenshot({ path: `${process.env.E2E_SCREENSHOT_DIR}/${name}.png`, fullPage: false, scale: 'css' });
}

// 확인이 필요한 경고 시트가 뜨면 확인하고 진행한다 (허용 구간 밖 실행 시간 등 실행 시각에 따라 달라진다)
export async function confirmIfAsked(page: Page, confirmName: RegExp | string, done: RegExp | string) {
  const confirm = page.getByRole('dialog').getByRole('button', { name: confirmName });
  const ok = page.getByText(done).first();
  await expect(confirm.or(ok).first()).toBeVisible();
  if (await confirm.isVisible()) await confirm.click();
  await expect(ok).toBeVisible();
}
