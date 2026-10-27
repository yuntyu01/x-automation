import { expect, test } from '@playwright/test';
import { confirmIfAsked, expectNoHorizontalOverflow, login, shot, watchErrors } from './helpers';

// 핵심 사용자 흐름: 로그인 → 첫 설정(X 연결·프롬프트·운영 모드·시간표) → 직접 등록 → 분할 확인 → 예약 → 목업 게시 → 결과 확인
// → AI 생성(목업) → 일괄 보관. 목업 연동이라 실제 X·AI·R2 호출은 없다.
test.describe.configure({ mode: 'serial' });

test('첫 설정부터 목업 게시 결과 확인까지', async ({ page }, info) => {
  const errors = watchErrors(page);
  await login(page, '/');
  await expect(page.getByText('처음이라면 설정부터 해요')).toBeVisible();
  await shot(page, info, 'home-first');
  await page.getByRole('link', { name: /첫 설정 시작/ }).click();
  await expect(page).toHaveURL(/\/setup/);

  // 1단계: 연결 상태 → 목업 X 계정 연결 (목업은 X로 가지 않고 바로 돌아온다)
  await page.getByRole('button', { name: 'X 계정 연결' }).click();
  await expect(page).toHaveURL(/\/settings\/connections/);
  await expect(page.getByText('X 계정을 연결했어요')).toBeVisible();
  await expect(page.getByText('@mock_account').first()).toBeVisible();

  // 2단계: 프롬프트 등록 → 자동 생성 기본 프롬프트로 지정
  await page.goto('/setup?step=1');
  await page.getByLabel('프롬프트 이름').fill('새벽 감성 썰');
  await page.getByLabel('프롬프트 내용').fill('새벽에 어울리는 짧은 이야기를 한국어로 써 줘. 본문과 답글로 나눌 수 있게 두 문단으로.');
  await page.getByRole('button', { name: '프롬프트 등록' }).click();
  await expect(page.getByText("'새벽 감성 썰' 프롬프트를 등록했어요")).toBeVisible();
  await expect(page.getByLabel('자동 생성에 쓸 기본 프롬프트')).toHaveValue(/.+/);

  // 3단계: 운영 모드
  await page.getByRole('button', { name: '다음' }).click();
  await page.getByText('완전 자동', { exact: true }).click();
  await expect(page.getByRole('radio', { name: /완전 자동/ })).toBeChecked();

  // 4단계: 시간표 요약
  await page.getByRole('button', { name: '다음' }).click();
  await expect(page.getByText('20묶음')).toBeVisible();
  await expect(page.getByText(/00:00~06:00/)).toBeVisible();

  // 5단계: 확인 → 완료 (자동 운영은 꺼진 채)
  await page.getByRole('button', { name: '다음' }).click();
  await expect(page.getByText('완전 자동').first()).toBeVisible();
  await page.getByRole('button', { name: '설정 완료' }).click();
  await expect(page.getByText(/첫 설정을 마쳤어요/)).toBeVisible();
  await expect(page).toHaveURL(/\/$/);
  await expect(page.getByText('자동 운영을 시작하지 않았어요')).toBeVisible();

  // 직접 등록 → 분할 미리보기
  await page.goto('/contents');
  await page.getByRole('button', { name: '새 글' }).click();
  await page.getByLabel('관리용 제목 (선택)').fill('E2E 첫 글');
  await page.getByRole('dialog').getByLabel('원문', { exact: true }).fill('비가 그친 새벽, 오래된 우산을 접으며 생각했다.\n\n그날 놓친 버스는 사실 내가 일부러 보낸 것이었다는 걸.');
  await expect(page.getByText(/X 게시물 2개/).first()).toBeVisible();
  await page.getByRole('button', { name: '저장하고 편집' }).click();
  await expect(page).toHaveURL(/\/contents\/[0-9a-f-]{36}$/);
  await expect(page.getByText(/X 게시물 2개/).first()).toBeVisible();
  await expectNoHorizontalOverflow(page, 'editor');
  await shot(page, info, 'editor');

  // 예약에 넣기 (빈 예약이 있으면 넣고, 없으면 안내)
  await page.getByRole('button', { name: '다음 빈 예약에 넣기' }).click();
  await expect(page.getByText(/예약에 넣었어요|빈 예약이 없어요|이미/).first()).toBeVisible();

  // 지금 게시 (목업) → 경고가 있으면 확인
  await page.getByRole('button', { name: '지금 게시' }).click();
  await expect(page.getByRole('dialog').getByText(/지금 X에 게시할까요|규칙과 다른 게시예요/)).toBeVisible();
  await expect(page.getByRole('dialog').getByText('목업 모드예요')).toBeVisible();
  await page.getByRole('dialog').getByRole('button', { name: '지금 게시' }).click();
  await confirmIfAsked(page, '그래도 지금 게시', '목업 게시를 시작했어요');

  // 결과 확인: 본문·답글 모두 올라감 + X 링크
  await page.goto('/publish');
  await expect(page.getByText('게시 완료').first()).toBeVisible({ timeout: 20_000 });
  await expect(page.getByRole('link', { name: /X에서 보기/ })).toHaveCount(2);
  await shot(page, info, 'publish');

  // 홈: 오늘 묶음·실제 게시물 수가 실제 상태로 보인다
  await page.goto('/');
  const today = page.locator('dl').first();
  await expect(today).toContainText('1 / 20묶음');
  await expect(today).toContainText('2개');
  await shot(page, info, 'home');
  expect(errors).toEqual([]);
});

test('AI 생성(목업)과 일괄 보관', async ({ page }, info) => {
  const errors = watchErrors(page);
  await login(page, '/contents');
  const before = (await (await page.request.get('/api/contents?limit=1')).json()).total as number;
  await page.getByRole('button', { name: '새 글' }).click();
  await page.getByRole('tab', { name: 'AI로 생성' }).click();
  await page.getByRole('tab', { name: '5개' }).click();
  await page.getByRole('button', { name: '5개 생성하기' }).click();
  await expect(page.getByText(/5개 생성을 시작했어요/)).toBeVisible();
  await expect
    .poll(async () => (await (await page.request.get('/api/contents?limit=1')).json()).total as number, { timeout: 30_000 })
    .toBe(before + 5);
  await page.reload();
  await shot(page, info, 'contents');

  await page.getByRole('button', { name: '선택', exact: true }).click();
  const boxes = page.getByRole('checkbox');
  await boxes.nth(0).check();
  await boxes.nth(1).check();
  await expect(page.getByText('2개 선택')).toBeVisible();
  await page.getByRole('toolbar', { name: '선택한 글 작업' }).getByRole('button', { name: '보관', exact: true }).click();
  await expect(page.getByText('2개를 처리했어요')).toBeVisible();
  expect(errors).toEqual([]);
});
