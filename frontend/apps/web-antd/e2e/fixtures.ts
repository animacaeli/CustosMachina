import { test as base, expect } from '@playwright/test';

/**
 * E2E 全局门禁（v0.12.15 独立复核 R2/第二批建议）：每条用例默认收集
 * pageerror，用例结束断言为空——页面级 JS 错误（未捕获异常）不再是
 * 「测试照过」的静默回归。console.error 暂不入门禁（ResizeObserver 循环
 * 等框架噪音需逐条登记 allowlist，成本收益见独立报告 §8.6 建议）。
 *
 * 用法：spec 里 `import { test, expect } from './fixtures'` 替代
 * '@playwright/test'。
 */
export const test = base.extend({
  // page 覆写：进用例前挂监听，用例结束后断言无未捕获错误
  page: async ({ page }, use) => {
    const pageErrors: string[] = [];
    page.on('pageerror', (err) => pageErrors.push(String(err)));
    await use(page);
    expect(
      pageErrors,
      '用例期间不应有未捕获页面错误（pageerror）——若为已知噪音请在 fixtures.ts 登记',
    ).toEqual([]);
  },
});

export { expect };
