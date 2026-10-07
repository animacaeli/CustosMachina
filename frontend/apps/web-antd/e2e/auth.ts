import type { Page } from '@playwright/test';

import process from 'node:process';

import { expect, request } from '@playwright/test';

/** E2E 共用：初始化超管 + UI 登录（独立审核第 3 批最小集合之①） */
export const ADMIN = { username: 'e2eadmin', password: 'e2e-pass-123' };

export async function ensureAdmin() {
  const ctx = await request.newContext({
    baseURL: process.env.E2E_API_URL ?? 'http://127.0.0.1:18080',
  });
  const res = await ctx.post('/api/setup/admin', { data: ADMIN });
  // 已初始化过的库返回 4xx，视为就绪
  if (!res.ok() && res.status() >= 500) {
    throw new Error(`setup/admin 失败: ${res.status()}`);
  }
  await ctx.dispose();
}

export async function login(page: Page) {
  await page.goto('/auth/login');
  // 登录页默认二维码视图：IM 未配置时自动降级账密（v0.12.1）；
  // 已在账密视图时无「超管登录」按钮——两者都兼容
  const adminBtn = page.getByRole('button', { name: '超管登录' });
  if (await adminBtn.isVisible({ timeout: 3000 }).catch(() => false)) {
    await adminBtn.click();
  }
  await page.getByPlaceholder('本地超管账号').fill(ADMIN.username);
  // 密码框无 placeholder，取表单内第二个 textbox
  await page
    .locator('form input[type="password"]')
    .first()
    .fill(ADMIN.password);
  await page
    .locator('form button[type="submit"], form .ant-btn-primary')
    .first()
    .click();
  // 登录成功后进入首页（路由守卫完成跳转）
  await expect(page).toHaveURL(/home|projects/, { timeout: 15_000 });
}
