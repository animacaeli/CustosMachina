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

  // tokenization 生效判据：敲入 shell 代码后 .view-lines 内出现**多类**
  // mtk 高亮 span。检测力说明（v0.12.16 复核 P3-N1 反例实证）：未注册
  // 语言 Monaco 仍产出基础行容器 span（mtk1 单类，纯文本渲染），故
  // 「count > 0」两态恒真无检测力；注册生效时同一输入实测产出 7 类
  // mtk class——断言 distinct class ≥ 2 才能区分两态（未注册=1 类必红）
  await page.locator('.monaco-editor .view-lines').first().click();
  await page.keyboard.type('if [ -f /etc/hosts ]; then echo ok; fi');
  await expect
    .poll(
      async () =>
        await page
          .locator('.monaco-editor .view-lines span[class*="mtk"]')
          .evaluateAll((els) => new Set(els.map((el) => el.className)).size),
      {
        message:
          'shell 高亮应产出 ≥2 类 mtk token class（未注册时仅 1 类纯文本，断言必红）',
        timeout: 10_000,
      },
    )
    .toBeGreaterThanOrEqual(2);
});
