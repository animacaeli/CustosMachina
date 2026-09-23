import { requestClient } from '#/api/request';

export interface ReleaseItem {
  createdAt: string;
  envType: 'canary' | 'prod';
  id: number;
  output: string;
  projectId: number;
  releaseBy: string;
  rollbackOf: null | number;
  runtime: string;
  serverId: number;
  status: 'failed' | 'success';
  tag: string;
}

export async function getReleasesApi(params: {
  env: string;
  page: number;
  projectId: number;
  size: number;
}) {
  return requestClient.get<{ items: ReleaseItem[]; total: number }>(
    '/releases',
    {
      params,
    },
  );
}

/** 已通过 CI 的标签（发布下拉用） */
export async function getPassedTagsApi(projectId: number, env: string) {
  return requestClient.get<string[]>('/releases/passed-tags', {
    params: { env, projectId },
  });
}

export async function createReleaseApi(data: {
  envType: string;
  projectId: number;
  tag: string;
}) {
  return requestClient.post<ReleaseItem>('/releases', data);
}

export async function rollbackReleaseApi(id: number) {
  return requestClient.post<ReleaseItem>(`/releases/${id}/rollback`);
}

/** compose 服务实例伸缩 */
export async function scaleComposeApi(
  serverId: number,
  data: { path: string; project: string; replicas: number; service: string },
) {
  return requestClient.post<{ output: string }>(
    `/server-compose/${serverId}/scale`,
    data,
  );
}

/** 服务器容器列表（含 compose 项目/服务/文件标签，项目视图过滤用） */
export interface ProjectContainer {
  composeFile?: string;
  composeProject?: string;
  composeService?: string;
  id: string;
  image: string;
  name: string;
  state: string;
  status: string;
}

export async function getServerContainersApi(serverId: number) {
  return requestClient.get<ProjectContainer[]>(
    `/server-containers/${serverId}`,
  );
}

/** 容器日志（tail 模式，JSON 行数组） */
export async function getContainerLogsApi(serverId: number, cid: string) {
  return requestClient.get<string[]>(
    `/server-containers/${serverId}/${cid}/logs`,
    {
      params: { tail: 200 },
    },
  );
}
