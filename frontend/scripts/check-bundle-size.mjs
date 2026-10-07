// 前端体积预算硬门禁（v0.12.15 独立审核 N6）：构建后检查 dist/assets。
// 此前 vite chunkSizeWarningLimit 只产生 warning，CI 照样绿——预算必须
// 超限即失败。两条规则：
//  1. 禁入清单：ts.worker / css.worker / html.worker 不得出现在产物中
//     （Monaco 走 editor.api 精确入口后这些语言服务 worker 不应再被打包，
//      回归即说明有人把根入口 'monaco-editor' 引回来了）；
//  2. gzip 总预算：dist/assets 下 js+css 的 gzip 总量 ≤ BUDGET_MB（默认
//     3.5MB，当前基线 3.2MB——只许变小或等比微增，涨预算须改此默认值
//     并在提交信息说明理由）。
import { readdirSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { gzipSync } from 'node:zlib';

const DIST = new URL('../apps/web-antd/dist/', import.meta.url).pathname;
const BUDGET_MB = Number(process.env.BUDGET_MB ?? 3.5);
const FORBIDDEN = [/ts\.worker/, /css\.worker/, /html\.worker/];

// 递归收集 js/css（vben 产物分布在 dist/{assets,js,jse,css} 与根，排除 sourcemap）
function walk(dir) {
  const out = [];
  for (const f of readdirSync(dir, { withFileTypes: true })) {
    const p = join(dir, f.name);
    if (f.isDirectory()) out.push(...walk(p));
    else if (/\.(css|js)$/.test(f.name)) out.push(p);
  }
  return out;
}

let files;
try {
  files = walk(DIST);
} catch {
  console.error(`✗ 未找到 ${DIST}——先构建再跑本脚本`);
  process.exit(1);
}

const violations = files
  .map((p) => p.slice(DIST.length))
  .filter((f) => FORBIDDEN.some((re) => re.test(f)));
let total = 0;
const sizes = [];
for (const p of files) {
  const buf = readFileSync(p);
  total += gzipSync(buf).length;
  sizes.push([gzipSync(buf).length, p.slice(DIST.length)]);
}
sizes.sort((a, b) => b[0] - a[0]);

const mb = total / 1_048_576;
console.log(
  `dist js+css gzip 总量：${mb.toFixed(2)} MB / 预算 ${BUDGET_MB} MB`,
);
console.log('前 5 大资产（gzip）：');
for (const [n, f] of sizes.slice(0, 5))
  console.log(`  ${(n / 1024).toFixed(0).padStart(6)} KB  ${f}`);

let failed = false;
if (violations.length > 0) {
  console.error(
    `\n✗ 禁入清单命中（语言服务 worker 回归，检查是否误用 monaco-editor 根入口）：`,
  );
  for (const f of violations) console.error(`  - ${f}`);
  failed = true;
}
if (mb > BUDGET_MB) {
  console.error(`\n✗ 超出体积预算 ${BUDGET_MB}MB（当前 ${mb.toFixed(2)}MB）`);
  failed = true;
}
if (failed) process.exit(1);
console.log('✓ 体积预算门禁通过');
