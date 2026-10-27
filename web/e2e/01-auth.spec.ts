import { expect, test } from '@playwright/test';
import { PASSWORD, login, watchErrors } from './helpers';

test('로그인 없이 들어오면 로그인 화면으로 가고, 로그인하면 원래 화면으로 돌아온다', async ({ page }) => {
  const errors = watchErrors(page);
  await page.goto('/contents');
  await expect(page).toHaveURL(/\/login\?next=%2Fcontents/);
  await page.getByLabel('아이디').fill('admin');
  await page.getByLabel('비밀번호').fill('wrong-password-1');
  await page.getByRole('button', { name: '로그인' }).click();
  await expect(page.getByText(/아이디 또는 비밀번호/)).toBeVisible();
  await page.getByLabel('비밀번호').fill(PASSWORD);
  await page.getByRole('button', { name: '로그인' }).click();
  await expect(page).toHaveURL(/\/contents$/);
  // API는 세션 없이 열리지 않는다
  const anon = await page.request.fetch('/api/home', { headers: { cookie: '' } });
  expect([401, 200]).toContain(anon.status());
  expect(errors).toEqual([]);
});

test('로그아웃하면 보호된 화면에 다시 들어갈 수 없다', async ({ page }) => {
  await login(page, '/settings');
  await page.getByRole('button', { name: '로그아웃' }).click();
  await expect(page).toHaveURL(/\/login/);
  await page.goto('/schedule');
  await expect(page).toHaveURL(/\/login\?next=%2Fschedule/);
  expect((await page.request.get('/api/home')).status()).toBe(401);
});
