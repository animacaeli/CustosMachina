import { defineConfig } from '@vben/vite-config';

export default defineConfig(async () => {
  return {
    application: {},
    vite: {
      server: {
        proxy: {
          '/api': {
            changeOrigin: true,
            // CustosMachina 后端（backend/，默认 :8080），路径保持 /api 前缀
            target: 'http://localhost:8080',
            ws: true,
          },
        },
      },
    },
  };
});
