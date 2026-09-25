import { requestClient } from '#/api/request';

/** CI 全局配置（token/secret 只回传存在性） */
export interface CiGlobal {
  giteaBaseUrl: string;
  hasGiteaToken: boolean;
  webhookHint: string;
  webhookSet: boolean;
}

export async function getCiGlobalApi() {
  return requestClient.get<CiGlobal>('/ci/global');
}

export async function saveCiGlobalApi(data: {
  giteaBaseUrl?: string;
  giteaToken?: string;
  webhookSecret?: string;
}) {
  return requestClient.put<CiGlobal>('/ci/global', data);
}

/** 镜像仓库 */
export interface Registry {
  address: string;
  hasCredential: boolean;
  id: number;
  name: string;
  remark: string;
  type: 'aliyun' | 'gitea' | 'harbor' | 'tencent';
}

export async function getRegistriesApi() {
  return requestClient.get<Registry[]>('/registries');
}

export async function createRegistryApi(data: {
  address: string;
  credential?: string;
  name: string;
  remark?: string;
  type: string;
}) {
  return requestClient.post<Registry>('/registries', data);
}

export async function updateRegistryApi(
  id: number,
  data: {
    address: string;
    credential?: string;
    name: string;
    remark?: string;
    type: string;
  },
) {
  return requestClient.put<Registry>(`/registries/${id}`, data);
}

export async function deleteRegistryApi(id: number) {
  return requestClient.delete(`/registries/${id}`);
}

/** 构建记录 */
export interface Build {
  builder: string;
  createdAt: string;
  durationSecs: number;
  envType: 'canary' | 'prod' | 'test';
  id: number;
  logUrl: string;
  projectId: number;
  sha: string;
  source: string;
  status: 'failed' | 'pending' | 'running' | 'success';
  tag: string;
}

export async function getBuildsApi(params: {
  env: string;
  page: number;
  projectId: number;
  size: number;
}) {
  return requestClient.get<{ items: Build[]; total: number }>('/builds', {
    params,
  });
}

/** 内嵌拉取某次构建的 gitea 流水线日志（纯文本） */
export async function getBuildLogApi(buildId: number) {
  return requestClient.get<string>(`/builds/${buildId}/log`);
}

/** 项目分支列表（M5 槽位表单用） */
export async function getProjectBranchesApi(projectId: number) {
  return requestClient.get<string[]>(`/project-branches/${projectId}`);
}
