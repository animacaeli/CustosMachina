import type { Page } from '@playwright/test';

import { expect } from '@playwright/test';

/** 项目链路共用：创建 → 表格行出现 → 关闭自动弹出的详情抽屉 */
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
  // modal 关闭强化等待：仅等 .ant-modal hidden 不够——leave 动画期间
  // form 控件 div 仍拦截 pointer events。等 mask 移除 + 可见 modal
  // 计数归零双条件。
  await page.waitForSelector('.ant-modal-mask', {
    state: 'hidden',
    timeout: 5000,
  });
  await expect(page.locator('.ant-modal:visible')).toHaveCount(0, {
    timeout: 5000,
  });
  // 产品行为：创建成功自动打开项目详情抽屉（引导配置部署目标）。必须
  // 关掉再返回——抽屉遮罩会拦截后续对表格按钮的一切点击（v0.12.15
  // 补丁批实测：发布按钮被详情抽屉的 form 控件拦 30s；此前用例的
  // force:true 一直在掩盖这个真实遮挡，独立复核第一批「不以 force
  // 绕过」的建议正是为此）
  const detailClose = page.locator('.ant-drawer-close').first();
  if (await detailClose.isVisible({ timeout: 3000 }).catch(() => false)) {
    await detailClose.click();
    await expect(page.locator('.ant-drawer-content:visible')).toHaveCount(0, {
      timeout: 5000,
    });
  }
}
