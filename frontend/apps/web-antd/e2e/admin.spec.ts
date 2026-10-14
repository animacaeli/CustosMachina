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

test('管理后台内部切换不裂顶栏页签（路由 meta fullPathKey:false 回归）', async ({
  page,
}) => {
  await page.goto('/admin');
  // 顶栏页签两种样式共用文本「管理后台」，切域/切 tab 前后都必须只有 1 个
  const adminTabs = page
    .locator('.tab-item, .tabs-chrome__item')
    .filter({ hasText: '管理后台' });
  await expect(adminTabs).toHaveCount(1, { timeout: 15_000 });
  // 切侧边栏域 + 域内 tab：URL query 已变（深链仍生效），但页签数不变
  await page.getByText('通知与告警').first().click();
  await expect(page).toHaveURL(/section=notify/);
  await page.getByRole('tab', { name: '通知路由' }).click();
  await expect(page).toHaveURL(/tab=notify-routes/);
  await expect(adminTabs).toHaveCount(1);
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
