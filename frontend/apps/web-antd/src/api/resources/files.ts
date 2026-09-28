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
  return requestClient.get<{ content: string }>(
    `/server-files/${serverId}/read`,
    {
      params: { path },
    },
  );
}

/**
 * 取带一次性 ticket 的下载 URL（浏览器 <a>/window.open 导航不带 Authorization
 * header，鉴权无 cookie——无 ticket 必 401）。
 */
export async function fileDownloadUrl(serverId: number, path: string) {
  const { ticket } = await requestClient.post<{ ticket: string }>(
    '/auth/tickets',
  );
  return `/api/server-files/${serverId}/download?path=${encodeURIComponent(path)}&ticket=${encodeURIComponent(ticket)}`;
}

export async function writeFileApi(
  serverId: number,
  path: string,
  content: string,
) {
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

export async function removeApi(
  serverId: number,
  path: string,
  isDir: boolean,
) {
  return requestClient.post(`/server-files/${serverId}/remove`, {
    isDir,
    path,
  });
}
