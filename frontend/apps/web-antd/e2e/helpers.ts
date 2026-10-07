import type { Page } from '@playwright/test';

import { expect } from '@playwright/test';

/** 项目链路共用：创建→表格行出现（详情/发布交互依赖嵌套 tab 布局，交互留人工验收） */
export async function createProject(page: Page, name: string) {
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
