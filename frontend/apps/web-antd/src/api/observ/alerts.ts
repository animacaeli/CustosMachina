/** O2 观测告警（P5 M3） */
import { requestClient } from '#/api/request';

export interface O2Alert {
  createdAt: string;
  description: string;
  enabled: boolean;
  frequency: number;
  id: number;
  level: 'critical' | 'info' | 'warn';
  name: string;
  operator: string;
  period: number;
  silence: number;
  sql: string;
  streamName: string;
  streamType: 'logs' | 'metrics' | 'traces';
  syncError: string;
  syncStatus: 'failed' | 'pending' | 'synced';
  threshold: number;
}

export interface SaveO2AlertInput {
  description: string;
  enabled: boolean;
  frequency: number;
  level: 'critical' | 'info' | 'warn';
  name: string;
  operator: string;
  period: number;
  silence: number;
  sql: string;
  streamName: string;
  streamType: 'logs' | 'metrics' | 'traces';
  threshold: number;
}

export interface O2Settings {
  configured: boolean;
  email: string;
  org: string;
}

export async function getO2AlertsApi() {
  return requestClient.get<O2Alert[]>('/observ/alerts');
}

export async function createO2AlertApi(data: SaveO2AlertInput) {
  return requestClient.post<O2Alert>('/observ/alerts', data);
}

export async function updateO2AlertApi(id: number, data: SaveO2AlertInput) {
  return requestClient.put(`/observ/alerts/${id}`, data);
}

export async function deleteO2AlertApi(id: number) {
  return requestClient.delete(`/observ/alerts/${id}`);
}

/** 全量重新同步到 O2（失败重试入口） */
export async function syncO2AlertsApi() {
  return requestClient.post<{ synced: number }>('/observ/alerts/sync');
}

export async function getO2SettingsApi() {
  return requestClient.get<O2Settings>('/observ/o2-settings');
}

export async function saveO2SettingsApi(data: {
  email?: string;
  /** 留空保留 */
  password?: string;
  org?: string;
}) {
  return requestClient.put<O2Settings>('/observ/o2-settings', data);
}

/** 告警模板（R1 模板化） */
export interface AlertPlaceholder {
  default: string;
  hint: string;
  key: string;
  label: string;
  required: boolean;
  type: 'number' | 'string';
}

export interface AlertTemplate {
  boundCount: number;
  category: string;
  createdAt: string;
  description: string;
  frequency: number;
  id: number;
  level: 'critical' | 'info' | 'warn';
  name: string;
  operator: string;
  period: number;
  placeholders: AlertPlaceholder[];
  query: string;
  queryType: 'promql' | 'sql';
  silence: number;
  threshold: number;
}

export interface SaveAlertTemplateInput {
  category: string;
  description: string;
  frequency: number;
  level: 'critical' | 'info' | 'warn';
  name: string;
  operator: string;
  period: number;
  placeholders: AlertPlaceholder[];
  query: string;
  queryType: 'promql' | 'sql';
  silence: number;
  threshold: number;
}

export async function getAlertTemplatesApi() {
  return requestClient.get<AlertTemplate[]>('/observ/alert-templates');
}

export async function createAlertTemplateApi(data: SaveAlertTemplateInput) {
  return requestClient.post<AlertTemplate>('/observ/alert-templates', data);
}

export async function updateAlertTemplateApi(
  id: number,
  data: SaveAlertTemplateInput,
) {
  return requestClient.put<AlertTemplate>(
    `/observ/alert-templates/${id}`,
    data,
  );
}

export async function deleteAlertTemplateApi(id: number) {
  return requestClient.delete(`/observ/alert-templates/${id}`);
}

/** 渲染预览：占位符填参 → 服务端产出最终查询（含转义） */
export async function renderAlertTemplateApi(
  templateId: number,
  params: Record<string, string>,
) {
  return requestClient.post<{ sql: string }>('/observ/alert-templates/render', {
    params,
    templateId,
  });
}

/** 项目侧经模板实例化（创建/更新二合一：id=0 创建）——SQL 服务端渲染 */
export async function upsertAlertFromTemplateApi(data: {
  description?: string;
  enabled: boolean;
  frequency?: number;
  id?: number;
  level?: 'critical' | 'info' | 'warn';
  name: string;
  operator?: string;
  params: Record<string, string>;
  period?: number;
  projectId: number;
  silence?: number;
  streamName?: string;
  templateId: number;
  threshold?: number;
}) {
  return requestClient.post('/observ/alerts/from-template', data);
}
