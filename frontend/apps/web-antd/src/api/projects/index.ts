import { requestClient } from '#/api/request';

/** 通知群（webhook 已剔除，仅 hasWebhook） */
export interface NotifyGroup {
  channel?: 'smtp' | 'webhook';
  id: number;
  name: string;
  remark?: string;
  scope: string;
  target?: string;
}

export interface SaveNotifyGroupInput {
  channel?: 'smtp' | 'webhook';
  name: string;
  remark?: string;
  scope: string;
  target?: string;
  webhook?: string;
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

/** 通知路由规则（统一通知路由，P5 M1） */
export interface NotifyRule {
  aggregateSec: number;
  createdAt: string;
  enabled: boolean;
  groupId: number;
  id: number;
  minLevel: 'critical' | 'info' | 'warn';
  name: string;
  silentEnd: string; // HH:MM，空 = 无静默
  silentStart: string;
  source: string;
}

export interface SaveNotifyRuleInput {
  aggregateSec: number;
  enabled: boolean;
  groupId: number;
  minLevel: 'critical' | 'info' | 'warn';
  name: string;
  silentEnd?: string;
  silentStart?: string;
  source: string;
}

export async function getNotifyRulesApi() {
  return requestClient.get<NotifyRule[]>('/notify-rules');
}

export async function createNotifyRuleApi(data: SaveNotifyRuleInput) {
  return requestClient.post<NotifyRule>('/notify-rules', data);
}

export async function updateNotifyRuleApi(
  id: number,
  data: SaveNotifyRuleInput,
) {
  return requestClient.put<NotifyRule>(`/notify-rules/${id}`, data);
}

export async function deleteNotifyRuleApi(id: number) {
  return requestClient.delete(`/notify-rules/${id}`);
}

/** 用规则的 source/级别发一条测试事件（走完整路由管线，含静默/聚合） */
export async function testNotifyRuleApi(id: number) {
  return requestClient.post(`/notify-rules/${id}/test`);
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
  provider: 'gitea' | 'gitee';
  ciJob: string;
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
  provider?: 'gitea' | 'gitee';
  ciJob?: string; // provider=gitee 时的 Jenkins job 名
  repoPath: string;
  repoUrl: string;
  slotGraceDays?: number;
  testSlotCount?: number;
  trafficCap?: number;
}

export interface EnvTarget {
  clusterId?: number; // k3s 运行时的目标集群（P8-M3.2）
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

/** P6-M9 渠道凭据设置（Telegram Bot / SMTP，平台级） */
export interface ChannelSettings {
  smtpConfigured: boolean;
  smtpFrom: string;
  smtpHost: string;
  smtpPort: string;
  smtpUser: string;
}

export async function getChannelSettingsApi() {
  return requestClient.get<ChannelSettings>('/notify-settings/channels');
}

export async function saveChannelSettingsApi(data: {
  smtpFrom?: string;
  smtpHost?: string;
  smtpPass?: string;
  smtpPort?: string;
  smtpUser?: string;
}) {
  return requestClient.put('/notify-settings/channels', data);
}
