import { expect, test } from '@playwright/test';

import { ensureAdmin, login } from './auth';

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
