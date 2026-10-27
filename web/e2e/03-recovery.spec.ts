import { expect, test } from '@playwright/test';
import { PASSWORD, login, watchErrors } from './helpers';

test('네트워크 오류는 성공으로 보이지 않고 다시 시도로 회복한다', async ({ page }) => {
  await login(page, '/');
  await page.route('**/api/contents?**', (r) => r.abort('internetdisconnected'));
  await page.goto('/contents');
  await expect(page.getByText(/서버에 연결할 수 없습니다/)).toBeVisible();
  await page.unroute('**/api/contents?**');
  await page.getByRole('button', { name: '다시 시도' }).click();
  await expect(page.getByText(/서버에 연결할 수 없습니다/)).toHaveCount(0);
  await expect(page.getByRole('heading', { name: '콘텐츠' })).toBeVisible();
});

test('세션이 끝나면 로그인 화면으로 보내고 원래 화면으로 돌아온다', async ({ page, context }) => {
  await login(page, '/schedule');
  await context.clearCookies(); // 세션 만료와 같다
  await page.reload();
  await expect(page).toHaveURL(/\/login\?next=%2Fschedule/);
  await page.getByLabel('아이디').fill('admin');
  await page.getByLabel('비밀번호').fill(PASSWORD);
  await page.getByRole('button', { name: '로그인' }).click();
  await expect(page).toHaveURL(/\/schedule$/);
  // 열어 둔 화면에서 API가 401을 받으면(만료) 바로 로그인 화면으로 보낸다
  await context.clearCookies();
  await page.getByRole('link', { name: '콘텐츠' }).first().click();
  await expect(page).toHaveURL(/\/login/);
});

test('X 연결을 해제하면 연결 화면과 홈이 미연결 상태를 보여 준다', async ({ page }) => {
  const errors = watchErrors(page);
  await login(page, '/settings/connections');
  const off = page.getByRole('button', { name: '연결 해제' });
  await expect(off.or(page.getByRole('button', { name: 'X 계정 연결' })).first()).toBeVisible();
  if (await off.isVisible()) {
    await off.click();
    await page.getByRole('dialog').getByRole('button', { name: '연결 해제' }).click();
    await expect(page.getByText('X 연결을 해제했어요', { exact: false })).toBeVisible();
  }
  await expect(page.getByText('연결 안 됨').first()).toBeVisible();
  await page.goto('/');
  await expect(page.getByText('X 계정이 연결되지 않았어요')).toBeVisible();
  // 지금 게시는 이유와 함께 막힌다 (성공한 척하지 않음)
  const list = await (await page.request.get('/api/contents?limit=1&stage=ready')).json();
  if (list.items?.length) {
    await page.goto('/contents/' + list.items[0].id);
    await page.getByRole('button', { name: '지금 게시' }).click();
    await page.getByRole('dialog').getByRole('button', { name: '지금 게시' }).click();
    await expect(page.getByText('X 계정을 연결해야 해요')).toBeVisible();
  }
  expect(errors).toEqual([]);
});

test('편집 중 연결이 끊기면 "저장 실패"로 보이고, 다시 연결되면 다시 시도로 저장된다', async ({ page }) => {
  await login(page, '/contents');
  const created = await page.request.post('/api/contents', {
    data: { raw_text: '자동 저장 확인용 글입니다.\n\n두 번째 문단.' },
    headers: { 'X-CSRF-Token': (await (await page.request.get('/api/auth/session')).json()).csrf_token },
  });
  const { id } = await created.json();
  await page.goto('/contents/' + id);
  const status = page.getByRole('status').filter({ hasText: /저장/ }).first();
  await expect(status).toHaveText(/저장됨/);
  await page.route(`**/api/contents/${id}`, (r) => (r.request().method() === 'GET' ? r.continue() : r.abort('internetdisconnected')));
  await page.getByLabel('원문', { exact: true }).fill('연결이 끊긴 동안 고친 원문입니다.\n\n두 번째 문단.');
  await expect(status).toContainText('저장 실패');
  await page.unroute(`**/api/contents/${id}`);
  // 다시 시도 버튼으로, 또는 다음 편집·자동 재시도로 저장된다
  const retry = status.getByRole('button', { name: '다시 시도' });
  if (await retry.isVisible()) await retry.click({ timeout: 2_000 }).catch(() => undefined);
  await expect(status).toHaveText(/저장됨/);
  const saved = await (await page.request.get('/api/contents/' + id)).json();
  expect(saved.revision.raw_text).toContain('연결이 끊긴 동안 고친 원문');
});
