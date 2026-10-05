import { requestClient } from '#/api/request';

/** P6-M7 配置中心（AgileConfig 纯后端通道）：文件为唯一编辑入口，
 * env/ini 文件下发时自动同步；此处仅同步/对账/连接设置 */

export interface AgileDiff {
  drifted: string[];
  extra: string[];
  missing: string[];
}

export async function syncAgileApi(projectId: number, env: string) {
  return requestClient.post<number>('/config-kv/sync', { env, projectId });
}

export async function reconcileAgileApi(projectId: number, env: string) {
  return requestClient.post<AgileDiff>('/config-kv/reconcile', { env, projectId });
}

export async function getAgileSettingsApi() {
  return requestClient.get<{ configured: boolean; endpoint: string }>(
    '/config-kv/provider-settings',
  );
}

export async function saveAgileSettingsApi(data: {
  endpoint?: string;
  password?: string;
  user?: string;
}) {
  return requestClient.put('/config-kv/provider-settings', data);
}
