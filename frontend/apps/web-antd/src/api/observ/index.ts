import { requestClient } from '#/api/request';

export interface ObservComponent {
  image: string;
  name: 'cadvisor' | 'vector';
  needsO2Url: boolean;
  remark: string;
}

export async function getObservListApi() {
  return requestClient.get<{ components: ObservComponent[]; o2Url: string }>(
    '/observ',
  );
}

export async function setO2UrlApi(url: string) {
  return requestClient.put('/observ/o2-url', { url });
}

/** running | stopped | absent */
export async function getObservStatusApi(serverId: number) {
  return requestClient.get<Record<string, string>>('/observ/status', {
    params: { serverId },
  });
}

export async function deployObservApi(serverId: number, component: string) {
  return requestClient.post<{ output: string }>('/observ/deploy', {
    component,
    serverId,
  });
}

export async function uninstallObservApi(serverId: number, component: string) {
  return requestClient.post<{ output: string }>('/observ/uninstall', {
    component,
    serverId,
  });
}
