import { ensureAdmin, login } from './auth';
import { expect, test } from './fixtures';

test.beforeAll(async () => {
  await ensureAdmin();
});

test.beforeEach(async ({ page }) => {
  await login(page);
});

// v0.12.15：Monaco 从根入口（全量语言注册，ts/css/html worker 进产物）改为
// editor.api 精确入口 + 显式注册 json/shell/python/ini/sql——回归点有二：
// ①「语言未注册时打开编辑器抛错/白屏」（pageerror 由 fixtures 全局门禁断言）；
// ②「基础语言未注册时 Monaco 静默降级纯文本」（不抛错——v0.12.15 复核
// §八.5 的 sql 回归即属此类，检测须断言 tokenization 生效而非仅等容器渲染）。
test('脚本编辑器加载且 shell tokenization 生效（editor.api 重构回归）', async ({
  page,
}) => {
  await page.goto('/cron/scripts');
  await page.getByRole('button', { name: '新增脚本' }).click();
  // Monaco 容器与输入区渲染
  await expect(page.locator('.monaco-editor').first()).toBeVisible({
    timeout: 10_000,
  });
  await expect(page.locator('.monaco-editor textarea').first()).toBeVisible();

  // tokenization 生效判据：敲入 shell 代码后 .view-lines 内出现 mtk*
  // 高亮 span（未注册语言渲染纯文本，无 mtk 分段——静默降级盲区的检测）
  // textarea 是 Monaco 隐藏 IME 辅助元素（readonly aria-hidden，点击不稳）——
  // 点击 .view-lines 主体聚焦后键盘输入
  await page.locator('.monaco-editor .view-lines').first().click();
  await page.keyboard.type('if [ -f /etc/hosts ]; then echo ok; fi');
  await expect
    .poll(
      async () =>
        page.locator('.monaco-editor .view-lines span[class*="mtk"]').count(),
      {
        message: 'shell 高亮 span 应出现（语言注册回归时为 0）',
        timeout: 10_000,
      },
    )
    .toBeGreaterThan(0);
});
