/** 证书管理（P5 M5） */
import { requestClient } from '#/api/request';

export interface Cert {
  caDirUrl: string;
  createdAt: string;
  certPath: string;
  dnsProvider: string;
  domains: string;
  email: string;
  enabled: boolean;
  expiresAt?: string;
  hasCreds?: boolean;
  id: number;
  keyPath: string;
  lastError: string;
  name: string;
  nextTryAt?: string;
  serverId: number;
  status: 'failed' | 'issued' | 'pending';
}

export interface SaveCertInput {
  caDirUrl?: string;
  certPath: string;
  /** DNS provider 环境变量（如 ALICLOUD_ACCESS_KEY/SECRET_KEY）；编辑留空保留 */
  credentials?: Record<string, string>;
  dnsProvider: string;
  domains: string;
  email: string;
  enabled: boolean;
  keyPath: string;
  name: string;
  serverId: number;
}

export async function getCertsApi() {
  return requestClient.get<Cert[]>('/certs');
}

export async function createCertApi(data: SaveCertInput) {
  return requestClient.post<Cert>('/certs', data);
}

export async function updateCertApi(id: number, data: SaveCertInput) {
  return requestClient.put<Cert>(`/certs/${id}`, data);
}

export async function deleteCertApi(id: number) {
  return requestClient.delete(`/certs/${id}`);
}

/** 立即签发/续期（DNS challenge + 部署 + nginx -t 守门） */
export async function renewCertApi(id: number) {
  return requestClient.post(`/certs/${id}/renew`);
}
