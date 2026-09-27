import { requestClient } from '#/api/request';

export interface FileEntry {
  isDir: boolean;
  mtime: number;
  name: string;
  size: number;
}

export async function listFilesApi(serverId: number, path: string) {
  return requestClient.get<{ entries: FileEntry[]; path: string }>(
    `/server-files/${serverId}/list`,
    { params: { path } },
  );
}

export async function readFileApi(serverId: number, path: string) {
  return requestClient.get<{ content: string }>(`/server-files/${serverId}/read`, {
    params: { path },
  });
}

export function fileDownloadUrl(serverId: number, path: string) {
  return `/api/server-files/${serverId}/download?path=${encodeURIComponent(path)}`;
}

export async function writeFileApi(serverId: number, path: string, content: string) {
  return requestClient.post(`/server-files/${serverId}/write`, {
    content,
    path,
  });
}

export async function uploadFileApi(
  serverId: number,
  dir: string,
  file: globalThis.File,
) {
  const form = new FormData();
  form.append('dir', dir);
  form.append('file', file);
  return requestClient.post<{ path: string }>(
    `/server-files/${serverId}/upload`,
    form,
  );
}

export async function mkdirApi(serverId: number, path: string) {
  return requestClient.post(`/server-files/${serverId}/mkdir`, { path });
}

export async function renameApi(serverId: number, from: string, to: string) {
  return requestClient.post(`/server-files/${serverId}/rename`, { from, to });
}

export async function removeApi(serverId: number, path: string, isDir: boolean) {
  return requestClient.post(`/server-files/${serverId}/remove`, { isDir, path });
}
