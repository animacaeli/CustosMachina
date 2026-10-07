import { expect, test } from '@playwright/test';

import { ensureAdmin, login } from './auth';
import { createProject } from './helpers';

test.beforeAll(async () => {
  await ensureAdmin();
});

test.beforeEach(async ({ page }) => {
  await login(page);
});

test('项目创建成功且表格行含名称/仓库路径（v0.12.2 回归）', async ({
  page,
}) => {
  const name = `e2e-proj-${Date.now()}`;
  await createProject(page, name);
  await expect(
    page.getByText(name).first(),
    '项目名应出现在表格中',
  ).toBeVisible({ timeout: 5000 });
  await expect(page.getByText('org/demo').first()).toBeVisible();
});

test('发布走确认抽屉：点击打开发布面板、取消不发布（v0.12.14 加深——原用例名不符实）', async ({
  page,
}) => {
  const name = `e2e-rel-${Date.now()}`;
  await createProject(page, name);
  // 发布按钮（primary）点击 → 发布抽屉（确认层级：发布必须经抽屉确认，v0.12.1）。
  // force：右下角 AI 助手悬浮球盖住最后一行按钮（已知布局，不影响真实用户滚动）
  await page
    .getByRole('button', { name: /发\s*布/ })
    .first()
    .click({
      force: true,
    });
  const drawer = page.locator('.ant-drawer-content');
  await expect(drawer).toBeVisible({ timeout: 5000 });
  // 关闭抽屉 = 取消，不产生发布动作
  await page.locator('.ant-drawer-close').first().click();
  await expect(drawer).toBeHidden({ timeout: 5000 });
});
