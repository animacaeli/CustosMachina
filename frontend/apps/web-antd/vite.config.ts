import process from 'node:process';

import { defineConfig } from '@vben/vite-config';

export default defineConfig(async () => {
  return {
    application: {},
    vite: {
      build: {
        // chunk 预算（独立审核 T11）：单 chunk 超 1.2MB 构建告警——
        // monaco ts/css/html worker 是已知大块（运行时只 fetch yaml+json）
        chunkSizeWarningLimit: 1200,
      },
      resolve: {
        alias: {
          // monaco-worker-manager 以带 .js 的子路径引 editor worker，
          // 被 monaco-editor 的 exports map 拒绝，落到实际文件绕过
          'monaco-editor/esm/vs/editor/editor.worker.js': new URL(
            'node_modules/monaco-editor/esm/vs/editor/editor.worker.js',
            import.meta.url,
          ).pathname,
        },
      },
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
