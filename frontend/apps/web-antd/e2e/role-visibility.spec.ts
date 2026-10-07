import { ensureAdmin, login } from './auth';
import { expect, test } from './fixtures';

test.beforeAll(async () => {
  await ensureAdmin();
});

test.beforeEach(async ({ page }) => {
  await login(page);
});

test('角色页高危摘要列（v0.12.12 U4 回归）', async ({ page }) => {
  await page.goto('/system/role');
  await expect(page.getByText('动作集（高危摘要）').first()).toBeVisible({
    timeout: 10_000,
  });
  // 内置 admin 角色应有红色高危徽标（至少 prod 发布）
  await expect(page.getByText('prod 发布').first()).toBeVisible();
  // 内置角色不可编辑但应显示禁用操作按钮而非仅灰色文字
  await expect(page.getByText(/详\s*情/).first()).toBeVisible();
});

test('配置中心文件页结构渲染（v0.12.14 加深：原用例仅查页标题，名实不符）', async ({
  page,
}) => {
  await page.goto('/configs/files');
  // 空库下的结构断言：文件管理器三件套——页标题 + 地址栏 + 合并视图入口
  await expect(page.getByText('配置文件').first()).toBeVisible({
    timeout: 10_000,
  });
  await expect(
    page.getByPlaceholder('输入路径跳转，如 prod/sub'),
    '地址栏（v0.8.3 实时跳转）应渲染',
  ).toBeVisible();
  await expect(
    page.getByRole('button', { name: /合并视图/ }),
    '合并视图入口（M7）应渲染',
  ).toBeVisible();
  await expect(page.getByText(/暂无文件/).first()).toBeVisible();
});
