import { ensureAdmin, ensureOpsUser, ensureUser, login, loginAs } from './auth';
import { expect, test } from './fixtures';

test.beforeAll(async () => {
  await ensureAdmin();
});

test.beforeEach(async ({ page }) => {
  await login(page);
});

test('登录后首页渲染运维态势与系统就绪度（第 2 批交付回归）', async ({
  page,
}) => {
  await expect(page.getByText('运维态势')).toBeVisible();
  const statLabels = [
    '未处理告警',
    '不可达主机(1h)',
    '失败发布(7d)',
    '失败任务(7d)',
    '证书 14d 到期',
  ];
  for (const label of statLabels) {
    await expect(page.getByText(label).first()).toBeVisible();
  }
  await expect(page.getByText('系统就绪度')).toBeVisible();
});

test('就绪度面板默认展开且检查项可见（超管+缺失项语义）', async ({ page }) => {
  // 超管有缺失项时默认展开（v0.12.10 语义）——检查项直接可见即可；
  // 折叠交互留给手动验收（antd extra 槽按钮在 a11y 树的暴露不稳定）
  await expect(page.getByText('通知群').first()).toBeVisible({
    timeout: 10_000,
  });
  await expect(page.getByText('主机 / 集群').first()).toBeVisible();
  await expect(page.getByText(/项必配/).first()).toBeVisible();
});

test('ops 角色首页态势/就绪度可读（复核 §7.1 回归：种子 v27——非超管 403）', async ({
  page,
}) => {
  const uid = await ensureOpsUser();
  await loginAs(page, uid, 'E2E Ops');
  // 直击裁决链：summary/readiness 必须回 200（回归时非超管全 403，
  // 此前 E2E 仅超管登录恰好绕过——所有现存门禁都覆盖不到）
  const resp = page.waitForResponse((r) =>
    r.url().includes('/api/home/summary'),
  );
  await page.goto('/home');
  const summary = await resp;
  expect(summary.status()).toBe(200);
  await expect(page.getByText('运维态势')).toBeVisible();
});

test('guest 角色首页态势被拒 403（独立复核 R2：权限拒绝面——防止种子误放开）', async ({
  page,
}) => {
  const uid = await ensureUser('e2e-guest', 'guest');
  await loginAs(page, uid, 'e2e-guest');
  const resp = page.waitForResponse((r) =>
    r.url().includes('/api/home/summary'),
  );
  await page.goto('/home');
  const summary = await resp;
  // guest 不在 home 种子面（扫码即得角色，与 v26 收权方向一致）——
  // 若未来种子误放开 guest，本用例红
  expect(summary.status()).toBe(403);
});
