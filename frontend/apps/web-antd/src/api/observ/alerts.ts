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
