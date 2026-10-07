import { expect, test } from '@playwright/test';

import { ensureAdmin, login } from './auth';

test.beforeAll(async () => {
  await ensureAdmin();
});

test.beforeEach(async ({ page }) => {
  await login(page);
});

// v0.12.15：Monaco 从根入口（全量语言注册，ts/css/html worker 进产物）改为
// editor.api 精确入口 + 显式注册 json/shell/python/ini——回归点是「语言未
// 注册时打开编辑器抛错/白屏」，此前的 E2E 没有任何用例真正打开过编辑器。
test('脚本编辑器加载且无页面错误（Monaco editor.api 重构回归）', async ({
  page,
}) => {
  const pageErrors: string[] = [];
  page.on('pageerror', (err) => pageErrors.push(String(err)));
  await page.goto('/cron/scripts');
  await page.getByRole('button', { name: '新增脚本' }).click();
  // Monaco 容器与输入区渲染（shell 语言显式注册）
  await expect(page.locator('.monaco-editor').first()).toBeVisible({
    timeout: 10_000,
  });
  await expect(
    page.locator('.monaco-editor textarea').first(),
  ).toBeVisible();
  expect(pageErrors, '编辑器加载不应产生页面错误').toEqual([]);
});
