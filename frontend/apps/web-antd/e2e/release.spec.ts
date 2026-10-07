import type { Page } from '@playwright/test';

import { expect, test } from '@playwright/test';

import { ensureAdmin, login } from './auth';

/** 项目链路：创建→表格行出现（详情/发布交互依赖嵌套 tab 布局，交互留人工验收） */
async function createProject(page: Page, name: string) {
  await page.goto('/envs/prod');
  await page.getByRole('button', { name: '新增项目' }).click();
  await page.getByPlaceholder('如 custos-machina').fill(name);
  await page
    .getByPlaceholder('https://gitea.internal/org/repo')
    .fill('https://gitea.example.com/org/demo.git');
  await page.getByPlaceholder('org/repo').nth(1).fill('org/demo');
  await page.locator('.ant-modal-footer .ant-btn-primary').click();
  await expect(page.getByText('创建成功').first()).toBeVisible({
    timeout: 10_000,
  });
  await page.waitForSelector('.ant-modal', { state: 'hidden', timeout: 5000 });
}

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

test('正式环境发布按钮为 danger（v0.12.1 确认层级回归——按钮存在性）', async ({
  page,
}) => {
  const name = `e2e-rel-${Date.now()}`;
  await createProject(page, name);
  // 环境操作列应有"发布"按钮（primary/danger）——仅验证可达
  await expect(
    page.getByRole('button', { name: /发\s*布/ }).first(),
  ).toBeVisible({ timeout: 5000 });
});
