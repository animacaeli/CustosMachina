import { expect, test } from '@playwright/test';

import { ensureAdmin, login } from './auth';

test.beforeAll(async () => {
  await ensureAdmin();
});

test.beforeEach(async ({ page }) => {
  await login(page);
});

test('角色页高危摘要列（v0.12.12 U4 回归）', async ({ page }) => {
  await page.goto('/system/role');
  await expect(page.getByText('动作集（高危摘要）').first()).toBeVisible({
    timeout: 10_000,
  });
  // 内置 admin 角色应有红色高危徽标（至少 prod 发布）
  await expect(page.getByText('prod 发布').first()).toBeVisible();
  // 内置角色不可编辑但应显示禁用操作按钮而非仅灰色文字
  await expect(page.getByText(/详\s*情/).first()).toBeVisible();
});

test('配置中心敏感文件 reveal 权限（admin 可看列表）', async ({ page }) => {
  await page.goto('/configs/files');
  await expect(page.getByText('配置文件').first()).toBeVisible({
    timeout: 10_000,
  });
});
