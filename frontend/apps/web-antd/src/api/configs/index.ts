/** 配置文件管理（P5 M4） */
import { requestClient } from '#/api/request';

export interface ConfigFile {
  applyAction: 'http' | 'none' | 'restart' | 'sighup';
  applyTarget: string;
  createdAt: string;
  format: 'env' | 'ini' | 'json' | 'toml' | 'yaml';
  hasContent: boolean;
  id: number;
  name: string;
  path: string;
  /** R2 文件管理器：项目归属与层级路径（首段=环境 prod/canary/test） */
  projectId: number;
  relPath: string;
  remark: string;
  sensitive: boolean;
  serverId: number;
}

export interface SaveConfigFileInput {
  applyAction: 'http' | 'none' | 'restart' | 'sighup';
  applyTarget?: string;
  format: 'env' | 'ini' | 'json' | 'toml' | 'yaml';
  /** 新建时可带初始内容 */
  content?: string;
  name: string;
  path: string;
  projectId?: number;
  /** 层级路径（首段=环境；留空=旧形态） */
  relPath?: string;
  remark?: string;
  sensitive: boolean;
  serverId: number;
}

export interface ConfigVersion {
  createdAt: string;
  createdBy: string;
  hash: string;
  hasContent: boolean;
  id: number;
  source: 'deploy' | 'edit' | 'rollback';
}

export async function getConfigFilesApi() {
  return requestClient.get<ConfigFile[]>('/config-files');
}

export async function createConfigFileApi(data: SaveConfigFileInput) {
  return requestClient.post<ConfigFile>('/config-files', data);
}

export async function updateConfigFileApi(
  id: number,
  data: SaveConfigFileInput,
) {
  return requestClient.put(`/config-files/${id}`, data);
}

export async function deleteConfigFileApi(id: number) {
  return requestClient.delete(`/config-files/${id}`);
}

export async function getConfigContentApi(id: number, reveal = false) {
  return requestClient.get<{
    content: string;
    masked: boolean;
    sensitive: boolean;
  }>(`/config-files/${id}/content`, { params: reveal ? { reveal: true } : {} });
}

export async function saveConfigContentApi(id: number, content: string) {
  return requestClient.put(`/config-files/${id}/content`, { content });
}

export async function deployConfigApi(id: number) {
  return requestClient.post(`/config-files/${id}/deploy`);
}

export async function getConfigVersionsApi(id: number) {
  return requestClient.get<ConfigVersion[]>(`/config-files/${id}/versions`);
}

export async function getConfigVersionContentApi(
  fileId: number,
  versionId: number,
) {
  return requestClient.get<{ content: string }>(
    `/config-files/${fileId}/versions/${versionId}`,
  );
}

export async function rollbackConfigApi(id: number, versionId: number) {
  return requestClient.post(`/config-files/${id}/rollback`, { versionId });
}

/** 环境同步：源环境（可选子前缀）内容完整同步到目标环境 */
export async function envSyncConfigApi(data: {
  projectId: number;
  sourceEnv: string;
  subPath?: string;
  targetEnv: string;
}) {
  return requestClient.post<{ created: number; updated: number }>(
    '/config-files/env-sync',
    data,
  );
}

/** AgileConfig 式合并视图：项目×环境聚合最终生效配置（只读预览） */
export interface MergedConfig {
  body: string;
  files: number;
  format: string;
  version: string;
}

export async function getMergedConfigApi(
  projectId: number,
  env: string,
  format: 'json' | 'yaml' = 'json',
) {
  return requestClient.get<MergedConfig>('/config-files/merged', {
    params: { env, format, project_id: projectId },
  });
}
