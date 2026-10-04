import { requestClient } from '#/api/request';

/** P6-M5 配置拉取凭证（应用级只读，限定 app×env） */

export interface PullToken {
  app: string;
  createdAt: string;
  enabled: boolean;
  envs: string;
  id: number;
  lastUsedAt: string;
  name: string;
  plaintext?: string; // 仅签发响应返回一次
}

export interface PullTokenOut extends PullToken {
  plaintext: string;
}

export async function listPullTokensApi() {
  return requestClient.get<PullToken[]>('/config-pull-tokens');
}

export async function createPullTokenApi(data: {
  app: string;
  envs: string;
  name?: string;
}) {
  return requestClient.post<PullTokenOut>('/config-pull-tokens', data);
}

export async function setPullTokenEnabledApi(id: number, enabled: boolean) {
  return requestClient.put(`/config-pull-tokens/${id}/enabled`, { enabled });
}
