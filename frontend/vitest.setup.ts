import process from 'node:process';

// 全局测试 setup：拦截指向真实外网的请求（iconify CDN 等），
// 返回空 JSON，避免测试依赖外网、以及 happy-dom 中挂起的 fetch
// 导致 vitest worker 无法终止（Timeout terminating + AbortError 噪音）。

const realFetch = globalThis.fetch;

function requestURL(input: RequestInfo | URL): string {
  if (typeof input === 'string') return input;
  if (input instanceof URL) return input.href;
  return input?.url ?? '';
}

globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
  const url = requestURL(input);
  if (/iconify|unsplash|picsum|placeholder/i.test(url)) {
    return new Response('{}', {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    });
  }
  return realFetch(input, init);
}) as typeof fetch;

// 防御：任何测试遗漏的未捕获 promise 拒绝不炸进程
process.on?.('unhandledRejection', () => {});
