import type { Page } from '@playwright/test';

import { Buffer } from 'node:buffer';
import { createHmac } from 'node:crypto';
import process from 'node:process';

import { expect, request } from '@playwright/test';

/** E2E 共用：初始化超管 + UI 登录（独立审核第 3 批最小集合之①） */
export const ADMIN = { username: 'e2eadmin', password: 'e2e-pass-123' };

/** 与 playwright.config.ts webServer 环境一致（E2E 专用，非生产密钥） */
const E2E_JWT_SECRET = 'e2e-secret-0123456789abcdef0123456789abcdef';

const API_BASE = process.env.E2E_API_URL ?? 'http://127.0.0.1:18080';

export async function ensureAdmin() {
  const ctx = await request.newContext({ baseURL: API_BASE });
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

// ---- 非超管登录态（v0.12.15：E2E 门禁盲区——此前仅超管登录，恰好绕过
// 权限种子回归[复核 §7.1]。普通用户无密码无法走 UI 账密，用已知 E2E
// secret 手签 JWT 注入登录态，覆盖 JWT→authz→casbin 全链真实裁决） ----

/** HS256 手签（后端 jwt.Claims：uid/name/adm；Parse 只认 HMAC+secret） */
export function signE2EToken(uid: number, name: string, adm = false): string {
  const b64 = (obj: unknown) =>
    Buffer.from(JSON.stringify(obj)).toString('base64url');
  const header = b64({ alg: 'HS256', typ: 'JWT' });
  const payload = b64({
    adm,
    exp: Math.floor(Date.now() / 1000) + 3600,
    name,
    uid,
  });
  const sig = createHmac('sha256', E2E_JWT_SECRET)
    .update(`${header}.${payload}`)
    .digest('base64url');
  return `${header}.${payload}.${sig}`;
}

/** 超管建 ops 用户（幂等：已存在时复用），返回用户 id */
export async function ensureOpsUser(): Promise<number> {
  const ctx = await request.newContext({ baseURL: API_BASE });
  const loginRes = await ctx.post('/api/auth/login', {
    data: { password: ADMIN.password, username: ADMIN.username },
  });
  if (!loginRes.ok())
    throw new Error(`超管 API 登录失败: ${loginRes.status()}`);
  const { data: loginData } = (await loginRes.json()) as {
    data: { accessToken: string };
  };
  const list = await ctx.get('/api/users', {
    headers: { Authorization: `Bearer ${loginData.accessToken}` },
  });
  const { data: users } = (await list.json()) as {
    data: Array<{ id: number; username: string }>;
  };
  const existing = users.find((u) => u.username === 'e2e-ops');
  if (existing) {
    await ctx.dispose();
    return existing.id;
  }
  const res = await ctx.post('/api/users', {
    data: { displayName: 'E2E Ops', roles: 'ops', username: 'e2e-ops' },
    headers: { Authorization: `Bearer ${loginData.accessToken}` },
  });
  if (!res.ok()) throw new Error(`创建 ops 用户失败: ${res.status()}`);
  const { data: created } = (await res.json()) as { data: { id: number } };
  await ctx.dispose();
  return created.id;
}

/** 注入非超管登录态：沿用 beforeEach 的超管会话拿 storage key，替换 token 后重载 */
export async function loginAs(page: Page, uid: number, name: string) {
  // namespace 含版本号（动态），从已登录会话取真实 key——版本变化不失效
  const key = await page.evaluate(() => {
    const k = Object.keys(localStorage).find((s) => s.endsWith('core-access'));
    if (!k)
      throw new Error(
        `未找到 core-access storage key: ${Object.keys(localStorage).join(',')}`,
      );
    return k;
  });
  await page.evaluate(
    ([k, token]) => {
      localStorage.setItem(
        k,
        JSON.stringify({
          accessCodes: [],
          accessToken: token,
          isLockScreen: false,
          refreshToken: null,
        }),
      );
    },
    [key, signE2EToken(uid, name)],
  );
  // 载入新身份（/user/info 按 token 的 uid 重新拉取）
  await page.goto('/home');
  await expect(page).toHaveURL(/home|projects/, { timeout: 15_000 });
}
