import { requestClient } from '#/api/request';

/** 受管服务器（后端 resources.Server，凭据字段已剔除，仅 hasCredential） */
export interface HostInfo {
  cpuCores: number;
  cpuModel: string;
  diskBytes: number;
  diskUsed: number;
  memBytes: number;
  netMbps: number;
  probedAt: string;
}

export interface ManagedServer {
  authType: 'key' | 'password';
  agentVersion: string;
  createdAt: string;
  /** 主机配置探测结果（JSON 字符串；环境探测时刷新，空 = 未探测） */
  hostInfo: string;
  groupId: null | number;
  hasCredential: boolean;
  host: string;
  id: number;
  lastSeen: null | string;
  metricSecs: number;
  name: string;
  port: number;
  remark: string;
  status: 'reachable' | 'unknown' | 'unreachable';
}

export interface ServerGroup {
  createdAt: string;
  id: number;
  name: string;
  remark: string;
  serverCount: number;
}

export interface ServerPayload {
  authType: 'key' | 'password';
  host: string;
  metricSecs?: number;
  name: string;
  passphrase?: string;
  password?: string;
  port: number;
  privateKey?: string;
  remark?: string;
  /** 更新时留空/不下发即保留原用户名 */
  username?: string;
  groupId?: null | number;
}

export async function getServerListApi() {
  return requestClient.get<ManagedServer[]>('/servers');
}

export async function createServerApi(data: ServerPayload) {
  return requestClient.post<ManagedServer>('/servers', data);
}

/** 更新：凭据字段全部可省略，省略即保留原凭据 */
export async function updateServerApi(
  id: number,
  data: Partial<ServerPayload>,
) {
  return requestClient.put<ManagedServer>(`/servers/${id}`, data);
}

export async function deleteServerApi(id: number) {
  return requestClient.delete(`/servers/${id}`);
}

/** 连通性测试：失败时后端返回 502 + message */
export async function testServerApi(id: number) {
  return requestClient.post<{ message: string }>(`/servers/${id}/test`);
}

/** 表单"测试连通"：用填写中的凭据即席连接，不落库 */
export async function testConnectionApi(data: ServerPayload) {
  return requestClient.post<{ message: string }>(
    '/servers/test-connection',
    data,
  );
}

export interface MetricPoint {
  cpuPct: number;
  memTotal: number;
  /** epoch 毫秒（后端 time.Time 序列化后由前端解析） */
  ts: string;
  memUsed: number;
}

export interface ServerEvent {
  createdAt: string;
  id: number;
  message: string;
  serverId: number;
  type: string;
}

/** 列表 sparkline：全部服务器的近期采样（内存环形缓冲，不含历史） */
export async function getLatestMetricsApi() {
  return requestClient.get<Record<string, MetricPoint[]>>(
    '/server-metrics/latest',
  );
}

/** 单服务器曲线（hours: 1~168；1.5h 内走内存，更早走库） */
export async function getServerMetricsApi(id: number, hours = 1) {
  return requestClient.get<MetricPoint[]>(
    `/server-metrics/${id}?hours=${hours}`,
  );
}

export async function getServerEventsApi(id: number) {
  return requestClient.get<ServerEvent[]>(`/server-events/${id}`);
}

export async function getGroupListApi() {
  return requestClient.get<ServerGroup[]>('/server-groups');
}

export async function createGroupApi(data: { name: string; remark?: string }) {
  return requestClient.post<ServerGroup>('/server-groups', data);
}

export async function updateGroupApi(
  id: number,
  data: { name: string; remark?: string },
) {
  return requestClient.put<ServerGroup>(`/server-groups/${id}`, data);
}

export async function deleteGroupApi(id: number) {
  return requestClient.delete(`/server-groups/${id}`);
}
