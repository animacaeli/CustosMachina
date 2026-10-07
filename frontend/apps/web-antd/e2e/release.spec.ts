import { ensureAdmin, login } from './auth';
import { expect, test } from './fixtures';
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

test('发布走确认抽屉：点击打开发布面板、取消不产生发布请求（独立复核 R2 加深）', async ({
  page,
}) => {
  const name = `e2e-rel-${Date.now()}`;
  await createProject(page, name);
  // 悬浮球为全局浮层（非发布链路组成），盖住表格末行按钮——隐藏后
  // 以正常点击验收按钮可点性（独立复核第一批建议：不以 force 绕过遮挡）
  await page.addStyleTag({ content: '.ai-fab { display: none !important; }' });
  // 发布按钮（primary）点击 → 发布抽屉（确认层级：发布必须经抽屉确认，v0.12.1）。
  // 行内定位：表格每行都有「发 布」（antd 两字按钮带全角空格），全局
  // .first() 会在多项目行间歧义
  await page
    .locator('tr', { hasText: name })
    .getByRole('button', { name: '发 布' })
    .click();
  // 详情抽屉关闭后 DOM 残留（antd 不销毁），按 :visible 断言当前抽屉
  const drawer = page.locator('.ant-drawer-content:visible');
  await expect(drawer).toBeVisible({ timeout: 5000 });

  // 取消语义（独立复核 R2）：关闭抽屉不得发出任何发布 POST——
  // 「存在按钮」不是发布验收，副作用断言才是
  const releasePosts: string[] = [];
  page.on('request', (r) => {
    if (r.method() === 'POST' && r.url().includes('/api/releases')) {
      releasePosts.push(r.url());
    }
  });
  await page.locator('.ant-drawer-close:visible').first().click();
  await expect(page.locator('.ant-drawer-content:visible')).toHaveCount(0, {
    timeout: 5000,
  });
  await page.waitForTimeout(500); // 关闭动画期间潜在的迟发请求
  expect(releasePosts, '取消抽屉不应触发发布请求').toEqual([]);
});
