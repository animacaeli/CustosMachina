import { requestClient } from '#/api/request';

export interface ObservConfigFile {
  content: string;
  filename: string;
}

export interface ObservComponent {
  category: 'logs' | 'metrics';
  compose: string;
  configFiles: ObservConfigFile[];
  image: string;
  name: string;
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

export async function deployObservApi(data: {
  compose?: string;
  component: string;
  configFiles?: Record<string, string>;
  serverId: number;
}) {
  return requestClient.post<{ output: string }>('/observ/deploy', data);
}

export async function uninstallObservApi(serverId: number, component: string) {
  return requestClient.post('/observ/uninstall', {
    component,
    serverId,
  });
}
