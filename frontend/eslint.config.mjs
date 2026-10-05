import { defineConfig } from '@vben/eslint-config';

export default defineConfig([
  {
    // oxfmt 与 vue/html-closing-bracket-newline 在多行属性模板上格式规则
    // 互相冲突（oxfmt 保留换行、eslint 要求不换行，--fix 来回打架）——
    // 以 oxfmt 为准（CI 的 pnpm lint 先跑 oxfmt --check 再跑 eslint）
    rules: {
      'vue/html-closing-bracket-newline': 'off',
    },
  },
]);
