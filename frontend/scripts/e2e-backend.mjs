// E2E 后端启动器：定位 e2e-server 二进制（本地在 apps/web-antd 下，
// CI 构建在 backend/ 下），以子进程拉起并透传退出。
import { spawn } from 'node:child_process';
import { existsSync } from 'node:fs';
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
const child = spawn(bin, { stdio: 'inherit' });
child.on('exit', (code) => process.exit(code ?? 1));
