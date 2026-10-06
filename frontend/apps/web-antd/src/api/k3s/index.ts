import { requestClient } from '#/api/request';

/** P8-M3.3 k3s 集群管理（admin） */

export interface Cluster {
  createdAt: string;
  domain?: string;
  id: number;
  name: string;
  remark?: string;
  server?: string;
}

export async function listClustersApi() {
  return requestClient.get<Cluster[]>('/k3s-clusters');
}

export async function createClusterApi(data: {
  domain?: string;
  kubeconfig?: string;
  name: string;
  remark?: string;
}) {
  return requestClient.post<Cluster>('/k3s-clusters', data);
}

export async function updateClusterApi(
  id: number,
  data: {
    domain?: string;
    kubeconfig?: string;
    name: string;
    remark?: string;
  },
) {
  return requestClient.put<Cluster>(`/k3s-clusters/${id}`, data);
}

export async function deleteClusterApi(id: number) {
  return requestClient.delete(`/k3s-clusters/${id}`);
}

export async function testClusterApi(id: number) {
  return requestClient.post<{ version: string }>(`/k3s-clusters/${id}/test`);
}

export async function deployObservStackApi(id: number) {
  return requestClient.post<{ summary: string }>(
    `/k3s-clusters/${id}/observ-stack`,
  );
}
