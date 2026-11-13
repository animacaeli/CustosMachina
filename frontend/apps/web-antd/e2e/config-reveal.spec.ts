import { randomUUID } from 'node:crypto';

import { request } from '@playwright/test';

import { ADMIN, ensureAdmin, ensureUser, login, loginAs } from './auth';
import { expect, test } from './fixtures';

/**
 * 明文查看环境分流（2026-11-09 返工核心语义的权限回归）：
 * dev 角色（有 config.view、无 config.reveal）——
 *   测试槽位 dev1 文件：查看明文放行（任何登录用户）；
 *   正式环境 prod 文件：查看明文 403。
 * 走真实 JWT→handler gate→casbin 全链（非 mock）。
 */

const PROJECT = 'e2e-reveal-demo';

async function apiSetup(projectName = PROJECT) {
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
  // 幂等：项目已存在则复用（同库重跑）
  const list = await ctx.get('/api/projects', { headers: auth });
  const { data: projects } = (await list.json()) as {
    data: Array<{ id: number; name: string }>;
  };
  let projectId = projects.find((p) => p.name === projectName)?.id;
  if (!projectId) {
    const srv = await ctx.post('/api/servers', {
      data: {
        authType: 'password',
        host: '127.0.0.1',
        name: projectName,
        password: 'x',
        port: 22,
        username: 'root',
      },
      headers: auth,
    });
    const { data: server } = (await srv.json()) as { data: { id: number } };
    const proj = await ctx.post('/api/projects', {
      data: {
        name: projectName,
        repoPath: 'org/e2e-reveal-demo',
        repoUrl: 'https://gitea.local/org/e2e-reveal-demo.git',
        testSlotCount: 2,
      },
      headers: auth,
    });
    const { data: project } = (await proj.json()) as { data: { id: number } };
    projectId = project.id;
    await ctx.put(`/api/projects/${projectId}/targets`, {
      data: {
        targets: [
          { envType: 'prod', serverId: server.id },
          { envType: 'test', serverId: server.id },
        ],
      },
      headers: auth,
    });
    for (const [relPath, content] of [
      ['prod/prod-cfg.env', 'DB_HOST=10.0.0.1\nDB_PASSWORD=prod-secret-x\n'],
      ['dev1/slot-cfg.env', 'DB_HOST=127.0.0.1\nDB_PASSWORD=dev-secret-y\n'],
    ] as const) {
      const res = await ctx.post('/api/config-files', {
        data: {
          applyAction: 'none',
          content,
          format: 'env',
          name: relPath,
          path: `/tmp/${relPath.replaceAll('/', '-')}`,
          projectId,
          relPath,
          serverId: server.id,
        },
        headers: auth,
      });
      if (!res.ok())
        throw new Error(
          `config-file ${relPath} 创建失败: ${res.status()} ${await res.text()}`,
        );
    }
  }
  await ensureUser('e2e-devcfg', 'dev');
  await ctx.dispose();
  if (!projectId) throw new Error('项目创建/查找失败');
  return { projectId };
}

async function selectConfigProject(
  page: import('@playwright/test').Page,
  name: string,
) {
  const select = page.getByRole('main').locator('.ant-select').first();
  const current = select.locator('.ant-select-selection-item');
  await expect(current).toBeVisible();
  if ((await current.textContent()) !== name) {
    const loaded = page.waitForResponse(
      (response) =>
        /\/api\/projects\/\d+$/.test(response.url()) && response.ok(),
    );
    await select.locator('.ant-select-selector').click();
    await page
      .locator('.ant-select-dropdown:visible .ant-select-item-option')
      .getByText(name, { exact: true })
      .click();
    await loaded;
  }
  await expect(current).toHaveText(name);
}

test.beforeAll(async () => {
  await ensureAdmin();
  await apiSetup();
});

test.beforeEach(async ({ page }) => {
  await login(page);
});

test('dev 角色：dev1 明文放行 / prod 明文 403', async ({ page }) => {
  // 换 dev 身份（手签 JWT 注入——dev 无密码，走不了 UI 账密）
  const uid = await ensureUser('e2e-devcfg', 'dev');
  await loginAs(page, uid, 'e2e-devcfg');

  await page.goto('/configs/files');
  await expect(page.getByText('配置文件').first()).toBeVisible({
    timeout: 10_000,
  });
  await selectConfigProject(page, PROJECT);
  await expect(page.getByText('dev1', { exact: true })).toBeVisible({
    timeout: 5000,
  });

  // ---- dev1 文件：默认脱敏 + 查看明文放行 ----
  await page
    .locator('.ant-tree-node-content-wrapper')
    .filter({ hasText: 'dev1' })
    .first()
    .click({ timeout: 8000 });
  await expect(page.getByText('slot-cfg.env').first()).toBeVisible({
    timeout: 5000,
  });
  await page
    .locator('.ant-tree-node-content-wrapper')
    .filter({ hasText: 'slot-cfg.env' })
    .first()
    .click({ timeout: 8000 });
  await expect(
    page.getByText('脱敏视图（敏感字段；编辑禁用）'),
    'dev1 默认脱敏视图',
  ).toBeVisible({ timeout: 5000 });
  await page.getByRole('button', { name: /查看明文/ }).click();
  await expect(page.getByText('已显示明文（本次查看已落审计）')).toBeVisible({
    timeout: 5000,
  });

  // ---- prod 文件：默认脱敏可读 + 查看明文 403 ----
  await page
    .locator('.ant-tree-node-content-wrapper')
    .filter({ hasText: 'prod' })
    .first()
    .click();
  await page
    .locator('.ant-tree-node-content-wrapper')
    .filter({ hasText: 'prod-cfg.env' })
    .first()
    .click();
  await expect(
    page.getByText('脱敏视图（敏感字段；编辑禁用）'),
    'prod 默认脱敏视图（config.view 可读）',
  ).toBeVisible({ timeout: 5000 });
  await page.getByRole('button', { name: /查看明文/ }).click();
  await expect(
    page.getByText(/正式环境明文查看需独立权限/),
    'prod reveal 应 403',
  ).toBeVisible({ timeout: 5000 });
});

test('超管可操作目录，退役目录清理和项目关注跨刷新保留', async ({ page }) => {
  const projectName = 'e2e-slot-' + randomUUID();
  const { projectId } = await apiSetup(projectName);
  const api = await request.newContext({
    baseURL: process.env.E2E_API_URL ?? 'http://127.0.0.1:18080',
  });
  try {
    const loginRes = await api.post('/api/auth/login', {
      data: { password: ADMIN.password, username: ADMIN.username },
    });
    const { data } = await loginRes.json();
    const headers = { Authorization: `Bearer ${data.accessToken}` };
    const update = await api.put(`/api/projects/${projectId}`, {
      headers,
      data: {
        name: projectName,
        repoPath: 'org/e2e-reveal-demo',
        repoUrl: 'https://gitea.local/org/e2e-reveal-demo.git',
        remark: '项目备注回归验证',
        testSlotCount: 1,
      },
    });
    expect(update.ok()).toBe(true);

    await page.goto('/configs/files');
    await selectConfigProject(page, projectName);
    const node = (name: string) =>
      page.locator('.ant-tree-node-content-wrapper').filter({
        has: page.getByText(name, { exact: true }),
      });
    await node('dev1').click({ button: 'right' });
    await expect(page.getByText('新建文件夹', { exact: true })).toBeVisible();
    await expect(page.getByText('删除目录', { exact: true })).toHaveCount(0);

    await page.mouse.click(5, 5);
    await node('dev2').click({ button: 'right' });
    await page.getByText('删除目录', { exact: true }).click();
    await page.getByRole('button', { name: /删\s*除/ }).click();
    await expect(node('dev2')).toHaveCount(0);
    await page.reload();
    await selectConfigProject(page, projectName);
    await expect(node('dev1')).toBeVisible();
    await expect(node('dev2')).toHaveCount(0);

    await node('dev1').click();
    await page.route('**/api/config-files/*/versions', (route) =>
      route.fulfill({
        status: 500,
        json: { code: 500, message: '历史版本暂不可用' },
      }),
    );
    await node('slot-cfg.env').click();
    await expect(page.getByText('历史版本暂不可用')).toBeVisible();
    await expect(
      page.locator('.editor-host .monaco-editor .view-lines'),
    ).toContainText('DB_HOST');
    await page.getByRole('button', { name: /查看明文/ }).click();
    const save = page.getByRole('button', { name: '仅保存' });
    await expect(
      page.getByText('已显示明文（本次查看已落审计）'),
    ).toBeVisible();
    await expect(save).toBeDisabled();
    await page.locator('.editor-host .monaco-editor .view-lines').click();
    await page.keyboard.type('#');
    await expect(save).toBeEnabled();

    await page.goto('/projects');
    const row = page.getByRole('row').filter({ hasText: projectName });
    await expect(row.getByText('项目备注回归验证')).toBeVisible();
    const star = row.getByRole('button', { name: '关注项目' });
    await star.click();
    await expect(star).toHaveAttribute('aria-pressed', 'true');
    await page.reload();
    await expect(star).toHaveAttribute('aria-pressed', 'true');
    await star.click();
    await expect(star).toHaveAttribute('aria-pressed', 'false');
  } finally {
    await api.dispose();
  }
});
