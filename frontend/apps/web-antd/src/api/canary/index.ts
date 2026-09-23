import { requestClient } from '#/api/request';

export interface CanaryPolicy {
  boundTag: string;
  createdAt: string;
  enabled: boolean;
  headerKey: string;
  headerValue: string;
  id: number;
  projectId: number;
  publishedVersion: number; // 0=未发布
  trafficPercent: number;
  type: 'header' | 'traffic';
}

export async function getCanaryPoliciesApi(projectId: number) {
  return requestClient.get<{
    policies: CanaryPolicy[];
    publishedVersion: number;
  }>(`/canary-policies/${projectId}`);
}

export interface SavePolicyInput {
  boundTag: string;
  enabled?: boolean;
  headerKey?: string;
  headerValue?: string;
  trafficPercent?: number;
  type: 'header' | 'traffic';
}

export async function createCanaryPolicyApi(
  projectId: number,
  data: SavePolicyInput,
) {
  return requestClient.post<CanaryPolicy>(
    `/canary-policies/${projectId}`,
    data,
  );
}

export async function updateCanaryPolicyApi(
  projectId: number,
  id: number,
  data: SavePolicyInput,
) {
  return requestClient.put<CanaryPolicy>(
    `/canary-policies/${projectId}/${id}`,
    data,
  );
}

export async function deleteCanaryPolicyApi(projectId: number, id: number) {
  return requestClient.delete(`/canary-policies/${projectId}/${id}`);
}

/** 聚合发布：把当前启用策略版本化整体生效（渲染 nginx 写目标机 + reload） */
export async function publishCanaryApi(projectId: number) {
  return requestClient.post<{ output: string; version: number }>(
    `/canary-policies/${projectId}/publish`,
  );
}
