// 供应链门禁（v0.12.15 独立审核 N1）：官方 registry 生产依赖审计。
// 日常安装仍走国内镜像（.npmrc npmmirror），仅审计步骤显式走官方源
// （npmmirror 不支持 audit endpoint，不能再作长期借口）。
// critical/high 默认失败；pnpm-audit-allowlist.json 中登记且未过期的
// 公告放行（每条须有理由与到期日）。moderate/low 计数提示不阻断。
import { execFileSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const here = path.dirname(fileURLToPath(import.meta.url));
const allowlistPath = path.join(here, 'pnpm-audit-allowlist.json');

let raw;
try {
  raw = execFileSync(
    'pnpm',
    ['audit', '--prod', '--json', '--registry=https://registry.npmjs.org'],
    {
      cwd: path.join(here, '..'),
      encoding: 'utf8',
      stdio: ['ignore', 'pipe', 'pipe'],
    },
  );
} catch (error) {
  // pnpm audit 发现漏洞时退出码非 0，stdout 仍是合法 JSON
  raw = error.stdout ?? '';
}
if (!raw.trim()) {
  console.error('audit 无输出（网络/registry 异常）——门禁按失败处理');
  process.exit(1);
}

const { advisories = {} } = JSON.parse(raw);
const allow = new Map(
  (JSON.parse(readFileSync(allowlistPath, 'utf8')).allowlist ?? []).map((x) => [
    x.id,
    x,
  ]),
);
const today = new Date().toISOString().slice(0, 10);

const counts = { critical: 0, high: 0, moderate: 0, low: 0 };
const failures = [];
for (const adv of Object.values(advisories)) {
  counts[adv.severity] = (counts[adv.severity] ?? 0) + 1;
  if (adv.severity !== 'critical' && adv.severity !== 'high') continue;
  const entry = allow.get(adv.github_advisory_id) ?? allow.get(adv.url);
  if (entry && entry.expires >= today) {
    // 例外对象与公告对象须一致（独立复核 §6.3：防 advisory ID 写对但
    // module 字段写错——登记的是别的包）
    if (entry.module && entry.module !== adv.module_name) {
      failures.push(
        `例外 ${adv.github_advisory_id} 登记的 module（${entry.module}）与公告对象（${adv.module_name}）不一致，须更正 allowlist`,
      );
      continue;
    }
    console.log(
      `⏭  放行 ${adv.module_name} ${adv.github_advisory_id}（${entry.reason}，到期 ${entry.expires}）`,
    );
    continue;
  }
  if (entry && entry.expires < today) {
    failures.push(
      `${adv.module_name} ${adv.github_advisory_id}：例外已过期（${entry.expires}），须升级或续期`,
    );
    continue;
  }
  failures.push(
    `${adv.severity} ${adv.module_name} ${adv.vulnerable_versions} -> 修复于 ${adv.patched_versions}：${adv.title}`,
  );
}

console.log(
  `生产依赖审计：critical=${counts.critical} high=${counts.high} moderate=${counts.moderate} low=${counts.low}`,
);
if (failures.length > 0) {
  console.error(`\n✗ 未处置的 critical/high 共 ${failures.length} 条：`);
  for (const f of failures) console.error(`  - ${f}`);
  process.exit(1);
}
console.log('✓ 供应链门禁通过（critical/high 全部修复或登记在案）');
