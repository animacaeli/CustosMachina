import process from 'node:process';

import { defineConfig } from '@vben/vite-config';

export default defineConfig(async () => {
  return {
    application: {},
    vite: {
      server: {
        proxy: {
          '/api': {
            changeOrigin: true,
            // CustosMachina 后端；本机 8080 被占用时用 CUSTOS_API_TARGET 覆盖
            target: process.env.CUSTOS_API_TARGET ?? 'http://localhost:8080',
            ws: true,
          },
        },
      },
    },
  };
});
