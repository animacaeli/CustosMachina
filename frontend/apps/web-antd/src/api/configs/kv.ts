import { requestClient } from '#/api/request';

/** P6-M7 K/V 配置（AgileConfig 共存）：平台为真相源，单向推送 + 对账 */

export interface KVItem {
  createdAt: string;
  env: 'canary' | 'prod' | 'test';
  id: number;
  key: string;
  projectId: number;
  remark?: string;
  sensitive: boolean;
  updatedAt: string;
  value: string; // 敏感项为 ******（reveal 接口取明文）
}

export interface KVDiff {
  drifted: string[];
  extra: string[];
  missing: string[];
}

export async function listKVApi(projectId: number, env?: string) {
  return requestClient.get<KVItem[]>('/config-kv', {
    params: { project_id: projectId, ...(env ? { env } : {}) },
  });
}

export async function createKVApi(data: {
  env: string;
  key: string;
  projectId: number;
  remark?: string;
  sensitive: boolean;
  value: string;
}) {
  return requestClient.post<KVItem>('/config-kv', data);
}

export async function updateKVApi(
  id: number,
  data: {
    env: string;
    key: string;
    projectId: number;
    remark?: string;
    sensitive: boolean;
    value: string;
  },
) {
  return requestClient.put<KVItem>(`/config-kv/${id}`, data);
}

export async function deleteKVApi(id: number) {
  return requestClient.delete(`/config-kv/${id}`);
}

export async function revealKVApi(id: number) {
  return requestClient.get<string>(`/config-kv/${id}/reveal`);
}

export async function pushKVApi(projectId: number, env: string) {
  return requestClient.post<number>('/config-kv/push', { env, projectId });
}

export async function reconcileKVApi(projectId: number, env: string) {
  return requestClient.post<KVDiff>('/config-kv/reconcile', { env, projectId });
}

export async function getKVSettingsApi() {
  return requestClient.get<{ configured: boolean; endpoint: string }>(
    '/config-kv/provider-settings',
  );
}

export async function saveKVSettingsApi(data: {
  endpoint?: string;
  password?: string;
  user?: string;
}) {
  return requestClient.put('/config-kv/provider-settings', data);
}
