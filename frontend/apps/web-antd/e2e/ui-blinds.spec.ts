import { request } from '@playwright/test';

import { ADMIN, ensureAdmin, login } from './auth';
import { expect, test } from './fixtures';

/**
 * UI 盲区补测（2026-11-09 用户质询后补）：真实鼠标路径过弹窗交互——
 * 新建文件表单 / 新建文件夹校验 / 环境同步选项。IAB 环境合成事件打不开
 * antd 弹层，此 spec 用 playwright 真事件。
 */

const PROJECT = 'ui-blinds-demo';

async function apiSetup() {
  const ctx = await request.newContext({
    baseURL: process.env.E2E_API_URL ?? 'http://127.0.0.1:18080',
  });
  const loginRes = await ctx.post('/api/auth/login', {
    data: { password: ADMIN.password, username: ADMIN.username },
  });
  const { data: loginData } = (await loginRes.json()) as {
    data: { accessToken: string };
  };
  const auth = { Authorization: `Bearer ${loginData.accessToken}` };
  const list = await ctx.get('/api/projects', { headers: auth });
  const { data: projects } = (await list.json()) as {
    data: Array<{ id: number; name: string }>;
  };
  const existing = projects.find((p) => p.name === PROJECT);
  if (existing) {
    await ctx.dispose();
    return existing.id;
  }
  const srv = await ctx.post('/api/servers', {
    data: {
      authType: 'password',
      host: '127.0.0.1',
      name: 'ui-blinds-srv',
      password: 'x',
      port: 22,
      username: 'root',
    },
    headers: auth,
  });
  const { data: server } = (await srv.json()) as { data: { id: number } };
  const proj = await ctx.post('/api/projects', {
    data: {
      name: PROJECT,
      repoPath: 'org/ui-blinds-demo',
      repoUrl: 'https://gitea.local/org/ui-blinds-demo.git',
      testSlotCount: 2,
    },
    headers: auth,
  });
  const { data: project } = (await proj.json()) as { data: { id: number } };
  await ctx.put(`/api/projects/${project.id}/targets`, {
    data: {
      targets: [
        { envType: 'prod', serverId: server.id },
        { envType: 'test', serverId: server.id },
      ],
    },
    headers: auth,
  });
  const res = await ctx.post('/api/config-files', {
    data: {
      applyAction: 'none',
      content: 'DB_HOST=10.0.0.1\nDB_PASSWORD=blinds-secret\n',
      format: 'env',
      name: 'blinds',
      path: '/tmp/blinds.env',
      projectId: project.id,
      relPath: 'prod/blinds.env',
      serverId: server.id,
    },
    headers: auth,
  });
  if (!res.ok())
    throw new Error(`造数失败: ${res.status()} ${await res.text()}`);
  await ctx.dispose();
  return project.id;
}

async function openProject(page: import('@playwright/test').Page) {
  await page.goto('/configs/files');
  await expect(page.getByText('配置文件').first()).toBeVisible({
    timeout: 10_000,
  });
  await page.getByRole('main').locator('.ant-select-selector').first().click();
  await page
    .locator(
      '.ant-select-dropdown:not(.ant-select-dropdown-hidden) .ant-select-item-option',
    )
    .filter({ hasText: PROJECT })
    .click();
  // 关闭残留下拉层（避免后续 select 判定受干扰）
  await page.keyboard.press('Escape');
  // 树环境层已挂载该项目（目录收起态，文件在用例内展开后断言）
  await expect(
    page
      .locator('.ant-tree-node-content-wrapper')
      .filter({ hasText: 'prod' })
      .first(),
  ).toBeVisible({ timeout: 5000 });
}

test.beforeAll(async () => {
  await ensureAdmin();
  await apiSetup();
});

test.beforeEach(async ({ page }) => {
  await login(page);
});

test('新建文件表单：无敏感开关 + 新语义提示', async ({ page }) => {
  await openProject(page);
  // 右键 prod 目录（teleport 自定义菜单，非 antd menu）→ 新建文件
  await page
    .locator('.ant-tree-node-content-wrapper')
    .filter({ hasText: 'prod' })
    .first()
    .click({ button: 'right' });
  // 精确匹配：「新建文件夹」包含「新建文件」子串（exact 会 trim 模板空白）
  await page.getByText('新建文件', { exact: true }).click();
  await expect(page.getByText('新建配置文件')).toBeVisible({ timeout: 5000 });
  // 敏感开关已废除
  await expect(page.getByText(/敏感文件/)).toHaveCount(0);
  // 层级路径提示为新语义
  await expect(page.getByText(/首段为 prod 或槽位名/).first()).toBeVisible();
  // 层级路径输入含环境段（右键带出）
  await expect(page.getByTitle('prod')).toHaveCount(0); // addon 非标题——占位断言
  const relVal = await page
    .getByPlaceholder('prod/app.yaml 或 dev1/app.yaml（首段=prod/槽位）')
    .inputValue();
  expect(relVal).toContain('prod');
});

test('环境同步弹窗：选项 = prod + 槽位，无 canary', async ({ page }) => {
  await openProject(page);
  // 选中文件 → 更多 → 环境同步
  await page
    .locator('.ant-tree-node-content-wrapper')
    .filter({ hasText: 'prod' })
    .first()
    .click();
  await page
    .locator('.ant-tree-node-content-wrapper')
    .filter({ hasText: 'blinds.env' })
    .first()
    .click();
  await page.getByRole('button', { name: /更多/ }).hover();
  await page
    .locator('.ant-dropdown-menu-item')
    .filter({ hasText: '环境同步' })
    .click();
  // 弹窗打开（源环境预填 prod）
  await expect(page.getByText(/已存在则生成新版本/)).toBeVisible({
    timeout: 5000,
  });
  await page.locator('.ant-modal .ant-select-selector').nth(1).click();
  await page.waitForTimeout(800);
  // 几何可见过滤：antd 关闭态下拉可能不带 hidden class（仅 display 控制）
  const opts = await page.evaluate(() =>
    [
      ...document.querySelectorAll(
        '.ant-select-dropdown .ant-select-item-option',
      ),
    ]
      .filter((o) => o.getBoundingClientRect().height > 0)
      .map((o) => (o.textContent ?? '').trim()),
  );
  const joined = opts.join(',');
  // 源已预填 prod → 目标下拉过滤掉源（只剩槽位）；canary 不在值域
  expect(joined, '同步目标应为槽位集合').toContain('dev1');
  expect(joined, '同步目标应过滤源环境 prod').not.toContain('prod');
  expect(joined, '同步目标不应有 canary').not.toContain('canary');
});

test('管理后台配置拉取页：签发表单默认环境为新值域', async ({ page }) => {
  await page.goto('/admin?section=delivery&tab=pull-tokens');
  await expect(page.getByText('配置拉取').first()).toBeVisible({
    timeout: 10_000,
  });
  // 签发凭证：环境默认值不再是旧 prod,test 形态
  await page.getByRole('button', { name: '签发凭证' }).click();
  const envsInput = page
    .locator('.ant-modal')
    .getByPlaceholder('prod,dev1,dev2');
  await expect(envsInput).toBeVisible({ timeout: 5000 });
  expect(await envsInput.inputValue()).toBe('prod,dev1');
  await page.keyboard.press('Escape');
});
