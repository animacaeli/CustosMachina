import { requestClient } from '#/api/request';

/** 通知群（webhook 已剔除，仅 hasWebhook） */
export interface NotifyGroup {
  createdAt: string;
  hasWebhook: boolean;
  id: number;
  name: string; // 【P】xxx / 【dev】xxx
  remark: string;
  scope: 'dev' | 'prod';
}

export interface SaveNotifyGroupInput {
  name: string;
  /** 生产类 prod（正式/灰度可选） | 测试类 dev */
  scope: 'dev' | 'prod';
  remark?: string;
  webhook?: string; // 留空保留
}

export async function getNotifyGroupsApi() {
  return requestClient.get<NotifyGroup[]>('/notify-groups');
}

export async function createNotifyGroupApi(data: SaveNotifyGroupInput) {
  return requestClient.post<NotifyGroup>('/notify-groups', data);
}

export async function updateNotifyGroupApi(
  id: number,
  data: SaveNotifyGroupInput,
) {
  return requestClient.put<NotifyGroup>(`/notify-groups/${id}`, data);
}

export async function deleteNotifyGroupApi(id: number) {
  return requestClient.delete(`/notify-groups/${id}`);
}

/** 发一条测试消息验证 webhook */
export async function testNotifyGroupApi(id: number) {
  return requestClient.post(`/notify-groups/${id}/test`);
}

export async function getOpsGroupApi() {
  return requestClient.get<{ configured: boolean; groupId: number }>(
    '/notify-settings/ops-group',
  );
}

export async function setOpsGroupApi(groupId: number) {
  return requestClient.put('/notify-settings/ops-group', { groupId });
}

/** 项目（CI token 已剔除，仅 hasCiToken） */
export interface Project {
  composePath: string;
  defaultBranch: string;
  /** <norm>-<env> 项目段（后端统一下发，容器视图过滤用） */
  deployPrefix: string;
  hasCiToken: boolean;
  id: number;
  name: string;
  notifyCanaryGroupId: null | number;
  notifyOnSuccess: boolean;
  notifyProdGroupId: null | number;
  notifyTestGroupId: null | number;
  repoPath: string;
  repoUrl: string;
  slotGraceDays: number;
  testSlotCount: number;
  trafficCap: number;
}

export interface SaveProjectInput {
  composePath?: string;
  defaultBranch?: string;
  ciToken?: string; // 留空保留
  name: string;
  notifyCanaryGroupId?: null | number;
  notifyOnSuccess?: boolean;
  notifyProdGroupId?: null | number;
  notifyTestGroupId?: null | number;
  repoPath: string;
  repoUrl: string;
  slotGraceDays?: number;
  testSlotCount?: number;
  trafficCap?: number;
}

export interface EnvTarget {
  createdAt: string;
  envType: 'canary' | 'prod' | 'test';
  id: number;
  projectId: number;
  runtime: 'compose' | 'k3s';
  serverId: number;
}

export async function getProjectsApi() {
  return requestClient.get<Project[]>('/projects');
}

export async function getProjectApi(id: number) {
  return requestClient.get<{ project: Project; targets: EnvTarget[] }>(
    `/projects/${id}`,
  );
}

export async function createProjectApi(data: SaveProjectInput) {
  return requestClient.post<Project>('/projects', data);
}

export async function updateProjectApi(id: number, data: SaveProjectInput) {
  return requestClient.put<Project>(`/projects/${id}`, data);
}

export async function deleteProjectApi(id: number) {
  return requestClient.delete(`/projects/${id}`);
}

export interface SaveTargetsInput {
  targets: Array<{
    envType: 'canary' | 'prod' | 'test';
    runtime?: 'compose' | 'k3s';
    serverId: number;
  }>;
}

export async function saveProjectTargetsApi(
  id: number,
  data: SaveTargetsInput,
) {
  return requestClient.put(`/projects/${id}/targets`, data);
}
