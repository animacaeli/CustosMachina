// E2E 后端启动器：定位 e2e-server 二进制（本地在 apps/web-antd 下，
// CI 构建在 backend/ 下），以子进程拉起并透传退出。
// 每次运行使用唯一临时 SQLite（独立审核 N5：固定 /tmp/e2e-custos.db 会
// 跨运行累积数据、并行 beforeAll 互相污染），退出时清理。
import { spawn } from 'node:child_process';
import { existsSync, mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const candidates = [
  join(here, '../apps/web-antd/e2e-server'), // 脚本在 frontend/scripts/
  join(here, '../../backend/e2e-server'), // CI：backend 根
];
const bin = candidates.find((p) => existsSync(p));
if (!bin) {
  console.error(`e2e-server 二进制不存在：${candidates.join(' 或 ')}`);
  console.error(
    '本地：cd backend && go build -o ../frontend/apps/web-antd/e2e-server ./cmd/server',
  );
  process.exit(127);
}

const dir = mkdtempSync(join(tmpdir(), 'custos-e2e-'));
const child = spawn(bin, {
  stdio: 'inherit',
  env: { ...process.env, CUSTOS_DATABASE_DSN: join(dir, 'e2e.db') },
});
const cleanup = () => rmSync(dir, { recursive: true, force: true });
child.on('exit', (code) => {
  cleanup();
  process.exit(code ?? 1);
});
// playwright 关停 webServer 时向本进程发 SIGTERM/SIGINT——转发给后端，
// 由其优雅停机（notify 排空 / 限速清扫），随后 exit 钩子清理临时库
for (const sig of ['SIGTERM', 'SIGINT']) {
  process.on(sig, () => child.kill(sig));
}
