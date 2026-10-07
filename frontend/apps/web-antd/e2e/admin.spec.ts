import { ensureAdmin, login } from './auth';
import { expect, test } from './fixtures';
import { createProject } from './helpers';

test.beforeAll(async () => {
  await ensureAdmin();
});

test.beforeEach(async ({ page }) => {
  await login(page);
});

test('管理后台四域导航与域内 tab（v0.12.9 回归）', async ({ page }) => {
  await page.goto('/admin');
  for (const label of ['身份与安全', '通知与告警', '交付与基础设施', 'AI']) {
    await expect(page.getByText(label).first()).toBeVisible({
      timeout: 15_000,
    });
  }
  // 切域 → 域内首个 tab 渲染 + URL 双参
  await page.getByText('通知与告警').first().click();
  await expect(page).toHaveURL(/section=notify/);
  await expect(page.getByText('通知群聊').first()).toBeVisible();
});

test('项目总览 → 详情页三环境聚合深链（v0.12.11 回归，v0.12.14 加深）', async ({
  page,
}) => {
  const name = `e2e-detail-${Date.now()}`;
  await createProject(page, name);
  await page.goto('/projects');
  // 项目名是行内链接 → /projects/:id 详情页（v0.12.11 卖点，此前无 E2E 覆盖）
  await page.getByRole('link', { name }).first().click();
  await expect(page).toHaveURL(/\/projects\/\d+/, { timeout: 10_000 });
  // 三环境聚合卡片 + 环境深链入口
  await expect(page.getByText(/进入.*环境/).first()).toBeVisible({
    timeout: 10_000,
  });
});
