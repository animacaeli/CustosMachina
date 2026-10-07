import { expect, test } from '@playwright/test';

import { ensureAdmin, login } from './auth';

test.beforeAll(async () => {
  await ensureAdmin();
});

test.beforeEach(async ({ page }) => {
  await login(page);
});

test('管理后台四域导航与域内 tab（v0.12.9 回归）', async ({ page }) => {
  await page.goto('/admin');
  for (const label of ['身份与安全', '通知与告警', '交付与基础设施', 'AI']) {
    await expect(page.getByText(label).first()).toBeVisible();
  }
  // 切域 → 域内首个 tab 渲染 + URL 双参
  await page.getByText('通知与告警').first().click();
  await expect(page).toHaveURL(/section=notify/);
  await expect(page.getByText('通知群聊').first()).toBeVisible();
});

test('项目总览页加载（v0.12.1/v0.12.11 回归）', async ({ page }) => {
  await page.goto('/projects');
  await expect(page.getByText('项目总览').first()).toBeVisible();
});
