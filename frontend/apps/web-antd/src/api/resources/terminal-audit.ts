import { requestClient } from '#/api/request';

/** P6-M6 终端审计与细粒度授权 */

export interface TerminalSession {
  file: string;
  legacy: boolean;
  operator: string;
  server: string;
  serverId: number;
  size: number;
  start: string;
}

/** 某主机的登录名级终端授权（admin） */
export async function getTerminalAclsApi(serverId: number) {
  return requestClient.get<string[]>(`/rbac/terminal-acls/${serverId}`);
}

export async function setTerminalAclsApi(serverId: number, usernames: string[]) {
  return requestClient.put(`/rbac/terminal-acls/${serverId}`, { usernames });
}

/** 终端会话列表（admin；serverId 省略 = 全部主机） */
export async function listTerminalSessionsApi(serverId?: number) {
  return requestClient.get<TerminalSession[]>('/server-terminals', {
    params: serverId ? { server_id: serverId } : undefined,
  });
}

/** 会话录制内容（asciinema cast 文本） */
export async function getTerminalCastApi(serverId: number, file: string) {
  return requestClient.get<string>(`/server-terminals/content`, {
    params: { file, server_id: serverId },
    responseType: 'text',
  });
}
