/** 首页运维态势聚合（GET /home/summary） */
import { requestClient } from '#/api/request';

export interface HomeAlertItem {
  createdAt: string;
  id: number;
  level: string;
  title: string;
}

export interface HomeReleaseItem {
  createdAt: string;
  envType: string;
  id: number;
  project: string;
  releaseBy: string;
  status: string;
  tag: string;
}

export interface HomeSummary {
  alerts: { critical: number; items: HomeAlertItem[]; open: number };
  certs: { expiring14d: number };
  jobs: { failed7d: number };
  releases: { failed7d: number; recent: HomeReleaseItem[] };
  servers: { unreachable1h: number };
}

export async function getHomeSummaryApi() {
  return requestClient.get<HomeSummary>('/home/summary');
}
