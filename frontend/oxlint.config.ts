import { oxlintConfig } from '@vben/oxlint-config';

import { defineConfig } from 'oxlint';

export default defineConfig({
  ...oxlintConfig,
  overrides: [
    ...(oxlintConfig.overrides ?? []),
    {
      // Vue 组件惯用 `!` 非空断言（模板 ref/事件回调参数），Vue 官方风格也如此。
      // FileReader 等一次性事件 `on<event> =` 直赋比 addEventListener 更简洁。
      files: ['*.vue', '**/*.vue'],
      rules: {
        'typescript/no-non-null-assertion': 'off',
        'unicorn/prefer-add-event-listener': 'off',
      },
    },
  ],
});
