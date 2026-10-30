import { expect, test } from '@playwright/test';
import { expectNoHorizontalOverflow, login, shot } from './helpers';

const ROUTES = ['/', '/contents', '/schedule', '/schedule/rules', '/publish', '/images', '/settings', '/settings/connections', '/settings/generation', '/settings/thread', '/settings/budget', '/notifications', '/prompts', '/setup', '/ai-costs'];
const WIDTHS = [360, 390, 768, 1440];

test('주요 화면: 4개 폭 × 라이트·다크에서 가로 넘침 없음', async ({ page }, info) => {
  test.setTimeout(240_000);
  await login(page, '/');
  for (const scheme of ['light', 'dark'] as const) {
    await page.emulateMedia({ colorScheme: scheme });
    for (const w of WIDTHS) {
      await page.setViewportSize({ width: w, height: 844 });
      for (const r of ROUTES) {
        await page.goto(r);
        await page.waitForLoadState('networkidle');
        await expectNoHorizontalOverflow(page, `${r} @${w} ${scheme}`);
      }
    }
    await page.setViewportSize({ width: 390, height: 844 });
    for (const r of ['/', '/contents', '/schedule', '/publish', '/settings']) {
      await page.goto(r);
      await page.waitForLoadState('networkidle');
      await shot(page, info, `${scheme}-390${r === '/' ? '-home' : r.replaceAll('/', '-')}`);
    }
  }
});

test('하단 탭에 마지막 내용이 가려지지 않고, 탭은 손가락으로 누르기 충분하다', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await login(page, '/settings');
  const tabbar = page.getByRole('navigation', { name: '하단 메뉴' });
  await expect(tabbar).toBeVisible();
  for (const link of await tabbar.getByRole('link').all()) {
    const b = await link.boundingBox();
    expect(b!.height, '탭 높이').toBeGreaterThanOrEqual(44);
    expect(b!.width, '탭 너비').toBeGreaterThanOrEqual(44);
  }
  await page.evaluate(() => window.scrollTo(0, document.documentElement.scrollHeight));
  const last = page.getByRole('button', { name: '로그아웃' });
  const lb = await last.boundingBox();
  const tb = await tabbar.boundingBox();
  expect(lb!.y + lb!.height, '마지막 버튼이 하단 탭 위에 보여야 한다').toBeLessThanOrEqual(tb!.y + 1);
});

test('모든 버튼에 이름이 있고, 키보드로 시트를 열고 닫을 수 있다', async ({ page }) => {
  await login(page, '/contents');
  for (const r of ['/', '/contents', '/schedule', '/settings', '/images', '/publish']) {
    await page.goto(r);
    await page.waitForLoadState('networkidle');
    const unnamed = await page.evaluate(() =>
      [...document.querySelectorAll('button, a[href], [role="button"]')]
        .filter((el) => (el as HTMLElement).offsetParent !== null)
        .filter((el) => !(el.getAttribute('aria-label') || el.textContent?.trim() || el.getAttribute('title')))
        .map((el) => el.outerHTML.slice(0, 80)),
    );
    expect(unnamed, `${r}: 이름 없는 버튼`).toEqual([]);
  }
  await page.goto('/contents');
  const trigger = page.getByRole('button', { name: '새 글' });
  await trigger.focus();
  await page.keyboard.press('Enter');
  const dialog = page.getByRole('dialog', { name: '새 글' });
  await expect(dialog).toBeVisible();
  // 포커스가 시트 안에 있다
  expect(await dialog.evaluate((d) => d.contains(document.activeElement))).toBe(true);
  await page.keyboard.press('Escape');
  await expect(dialog).toBeHidden();
  await expect(trigger).toBeFocused();
});

test('로그인 화면은 키보드만으로 쓸 수 있고 포커스가 보인다', async ({ page, context }) => {
  await context.clearCookies();
  await page.goto('/login');
  await page.keyboard.press('Tab');
  const focused = page.locator(':focus');
  await expect(focused).toHaveAttribute('name', 'username');
  const outline = await focused.evaluate((el) => {
    const s = getComputedStyle(el);
    return s.outlineStyle !== 'none' || s.boxShadow !== 'none';
  });
  expect(outline, '포커스 표시').toBe(true);
});
