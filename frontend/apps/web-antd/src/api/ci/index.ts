import { requestClient } from '#/api/request';

/** CI 全局配置（token/secret 只回传存在性；gitee/Jenkins 为 P6-M8 新增段） */
export interface CiGlobal {
  giteaBaseUrl: string;
  hasGiteaToken: boolean;
  webhookHint: string;
  webhookSet: boolean;
  giteeBaseUrl: string;
  hasGiteeToken: boolean;
  giteeWebhookHint: string;
  giteeWebhookSet: boolean;
  jenkinsUrl: string;
  jenkinsUser: string;
  hasJenkinsToken: boolean;
  jenkinsJobHint: string;
}

export async function getCiGlobalApi() {
  return requestClient.get<CiGlobal>('/ci/global');
}

export async function saveCiGlobalApi(data: {
  giteaBaseUrl?: string;
  giteaToken?: string;
  webhookSecret?: string;
  giteeBaseUrl?: string;
  giteeToken?: string;
  giteeWebhookPass?: string;
  jenkinsUrl?: string;
  jenkinsUser?: string;
  jenkinsToken?: string;
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
  provider: 'gitea' | 'gitee';
  /** 外部构建定位 job#number（test 记录由 CI 回调携带；有值可拉 Jenkins 日志） */
  ciRef?: string;
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

/** 内嵌拉取某次构建的流水线日志（gitea Actions / Jenkins console，纯文本） */
export async function getBuildLogApi(buildId: number) {
  return requestClient.get<string>(`/builds/${buildId}/log`);
}

/** 增量日志片段（构建中 2s 轮询实时滚动） */
export interface BuildLogChunk {
  more: boolean; // 构建仍在途（还有后续）
  next: number; // 下一轮 start 游标（字节）
  text: string; // 自 start 起的新增文本
}

/** 增量拉取构建日志：start=上一轮 next；终态构建一次拉全（more=false）；
 * kind='deploy' 供 test 记录切到部署输出（默认有 ciRef 时拉 CI 构建） */
export async function getBuildLogStreamApi(
  buildId: number,
  start: number,
  kind?: 'deploy',
) {
  return requestClient.get<BuildLogChunk>(`/builds/${buildId}/log/stream`, {
    params: { kind, start },
  });
}

/** 项目分支列表（M5 槽位表单用） */
export async function getProjectBranchesApi(projectId: number) {
  return requestClient.get<string[]>(`/project-branches/${projectId}`);
}
