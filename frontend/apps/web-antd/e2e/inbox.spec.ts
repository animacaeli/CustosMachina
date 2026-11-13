import { ensureAdmin, login } from './auth';
import { expect, test } from './fixtures';

test('标记旧通知已读后保留已加载的历史页', async ({ page }) => {
  await ensureAdmin();
  const first = {
    id: 51,
    projectId: 0,
    title: '最新通知',
    message: '',
    createdAt: '2026-11-13T01:00:00Z',
    isRead: true,
  };
  let oldRead = false;
  await page.route('**/api/auth/inbox*', async (route) => {
    const req = route.request();
    if (req.method() === 'PUT') {
      expect(req.postDataJSON()).toEqual({ action: 'read', id: 1 });
      oldRead = true;
      await route.fulfill({ json: { code: 0, data: { unread: 0 } } });
      return;
    }
    const older = new URL(req.url()).searchParams.has('before');
    const items = older
      ? [{ ...first, id: 1, title: '历史通知', isRead: oldRead }]
      : [first];
    await route.fulfill({
      json: {
        code: 0,
        data: { items, next: older ? 0 : 51, unread: oldRead ? 0 : 1 },
      },
    });
  });
  await login(page);
  await page.locator('.bell-button').click();
  await page.getByRole('button', { name: '查看所有消息' }).click();
  const drawer = page
    .locator('.ant-drawer-content')
    .filter({ hasText: '站内通知' });
  await drawer.getByRole('button', { name: '加载更多' }).click();
  const older = drawer.getByRole('button', { name: /历史通知/ });
  await expect(older.locator('[aria-label="未读"]')).toBeVisible();
  await older.click();
  await expect(older.locator('[aria-label="未读"]')).toHaveCount(0);
  await expect(older).toBeVisible();
  await expect(drawer.getByText('最新通知')).toBeVisible();
});
