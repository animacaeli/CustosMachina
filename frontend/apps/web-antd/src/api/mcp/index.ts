import { requestClient } from '#/api/request';

/** MCP 接入凭证（P6-M2）：明文只在签发时返回一次 */

export interface McpTokenRow {
  calls7d: number;
  enabled: boolean;
  id: number;
  lastUsedAt: null | string;
  name: string;
  role: 'admin' | 'dev';
}

export async function listMcpTokensApi() {
  return requestClient.get<McpTokenRow[]>('/mcp/tokens');
}

export async function createMcpTokenApi(data: { name: string; role: string }) {
  return requestClient.post<{ plaintext: string; role: string }>(
    '/mcp/tokens',
    data,
  );
}

export async function revokeMcpTokenApi(id: number) {
  return requestClient.post(`/mcp/tokens/${id}/revoke`);
}

export async function enableMcpTokenApi(id: number) {
  return requestClient.post(`/mcp/tokens/${id}/enable`);
}
