import process from 'node:process';

import { defineConfig } from '@playwright/test';

/**
 * 主链路 E2E（独立审核第 3 批 Q1）：
 * 双 webServer——后端（go 构建产物 + 临时 sqlite）与前端 dev server（代理
 * /api 到后端）。CI 与本地同一套配置。
 */
export default defineConfig({
  testDir: './e2e',
  timeout: 30_000,
  retries: process.env.CI ? 1 : 0,
  // 共享单后端实例，SQLite 并发写会互相 busy——串行跑（全套 ~1.5min）
  workers: 1,
  reporter: process.env.CI ? 'github' : 'list',
  use: {
    baseURL: process.env.E2E_WEB_URL ?? 'http://localhost:5666',
    screenshot: 'only-on-failure',
    trace: 'retain-on-failure',
  },
  webServer: [
    {
      // DSN 由 e2e-backend.mjs 用唯一临时目录注入（N5：不再固定复用一个库）
      command:
        'CUSTOS_HTTP_ADDR=127.0.0.1:18080 CUSTOS_LOG_DIR= CUSTOS_AUTH_JWT_SECRET=e2e-secret-0123456789abcdef0123456789abcdef CUSTOS_AUTH_ALLOW_DEFAULT_SECRET=1 node ../../scripts/e2e-backend.mjs',
      url: 'http://127.0.0.1:18080/api/healthz',
      reuseExistingServer: false,
      timeout: 60_000,
      stdout: 'ignore',
    },
    {
      command:
        'CUSTOS_API_TARGET=http://127.0.0.1:18080 pnpm exec vite --port 5666 --host',
      url: 'http://localhost:5666',
      reuseExistingServer: false,
      timeout: 120_000,
      stdout: 'ignore',
    },
  ],
});
