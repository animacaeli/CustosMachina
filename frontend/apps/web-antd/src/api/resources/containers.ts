import { requestClient } from '#/api/request';

/** 一次性短时 ticket：WS/SSE 握手用（避免长期 token 落访问日志） */
export async function issueTicketApi() {
  return requestClient.post<{ ticket: string; ttlSeconds: number }>(
    '/auth/tickets',
  );
}

export interface DockerContainer {
  composeFile?: string;
  composeProject?: string;
  id: string;
  image: string;
  name: string;
  state: string;
  status: string;
}

/** 读取远端 compose 部署文件 */
export async function getComposeFileApi(serverId: number, path: string) {
  return requestClient.get<{ content: string; path: string }>(
    `/server-compose/${serverId}/file`,
    { params: { path } },
  );
}

/** 覆盖保存（远端自动备份 .bak.时间戳），保存前后端都会做校验 */
export async function saveComposeFileApi(
  serverId: number,
  path: string,
  content: string,
) {
  return requestClient.put<{ message: string }>(
    `/server-compose/${serverId}/file`,
    {
      content,
      path,
    },
  );
}

/** 按部署文件强制重建项目容器 */
export async function recreateComposeApi(
  serverId: number,
  path: string,
  project: string,
) {
  return requestClient.post<{ output: string }>(
    `/server-compose/${serverId}/recreate`,
    { path, project },
  );
}

export interface EnvProbe {
  composeErr: string;
  composeVer: string;
  distro: string;
  distroLike: string;
  dockerErr: string;
  dockerVersion: string;
  ready: boolean;
}

export interface ContainerStats {
  cpuPct: string;
  memLimit: number;
  memUsed: number;
}

export interface ContainerStatItem {
  cpuPerc: string;
  id: string;
  memPerc: string;
  memUsage: string;
}

/** 批量取全部容器资源占用（后端一条 SSH 命令，抽屉轮询用） */
export async function statsAllApi(serverId: number) {
  return requestClient.get<ContainerStatItem[]>(
    `/server-container-stats/${serverId}`,
  );
}

export async function listContainersApi(serverId: number) {
  return requestClient.get<DockerContainer[]>(`/server-containers/${serverId}`);
}

export async function containerActionApi(
  serverId: number,
  cid: string,
  action: 'start' | 'stop',
) {
  return requestClient.post(`/server-containers/${serverId}/${cid}/${action}`);
}

export async function containerLogsApi(
  serverId: number,
  cid: string,
  tail = 200,
) {
  return requestClient.get<string[]>(
    `/server-containers/${serverId}/${cid}/logs?tail=${tail}`,
  );
}

export async function containerStatsApi(serverId: number, cid: string) {
  return requestClient.get<ContainerStats>(
    `/server-containers/${serverId}/${cid}/stats`,
  );
}

export async function probeEnvApi(serverId: number) {
  return requestClient.get<EnvProbe>(`/server-env/${serverId}`);
}

export async function installGuideApi(distro: string) {
  return requestClient.get<{ distro: string; guide: string }>(
    `/server-env-guide/${encodeURIComponent(distro)}`,
  );
}

export async function deployComposeApi(
  serverId: number,
  yamlText: string,
  name?: string,
) {
  return requestClient.post<{ dir?: string; output: string }>(
    `/server-compose/${serverId}`,
    { name, yaml: yamlText },
  );
}
