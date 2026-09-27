import { requestClient } from '#/api/request';

export interface CronScript {
  content: string;
  createdAt: string;
  createdBy: string;
  id: number;
  name: string;
  remark: string;
  /** shell | python | compose-run */
  type: 'compose-run' | 'python' | 'shell';
}

export interface CronJob {
  carrier: 'compose-run' | 'run';
  command: string;
  enabled: boolean;
  id: number;
  image: string;
  lastRunAt: null | string;
  lastStatus: string;
  name: string;
  nextRunAt: null | string;
  projectName: string;
  schedule: string;
  scriptId: number;
  serverId: number;
  service: string;
  timeoutSecs: number;
}

export interface CronJobItem {
  job: CronJob;
  scriptName: string;
}

export interface CronRun {
  createdAt: string;
  durationSecs: number;
  finishedAt: null | string;
  id: number;
  jobId: number;
  output: string;
  startedAt: string;
  /** running | success | failed | timeout | skipped | unknown */
  status: string;
  /** schedule | manual */
  trigger: string;
}

// ---- 脚本库 ----

export async function getScriptsApi() {
  return requestClient.get<{ items: CronScript[] }>('/cron-scripts');
}

export async function createScriptApi(data: Partial<CronScript>) {
  return requestClient.post<CronScript>('/cron-scripts', data);
}

export async function updateScriptApi(id: number, data: Partial<CronScript>) {
  return requestClient.put<CronScript>(`/cron-scripts/${id}`, data);
}

export async function deleteScriptApi(id: number) {
  return requestClient.delete(`/cron-scripts/${id}`);
}

// ---- 任务 ----

export async function getJobsApi() {
  return requestClient.get<{ items: CronJobItem[] }>('/cron-jobs');
}

export async function createJobApi(data: Partial<CronJob>) {
  return requestClient.post<CronJob>('/cron-jobs', data);
}

export async function updateJobApi(id: number, data: Partial<CronJob>) {
  return requestClient.put<CronJob>(`/cron-jobs/${id}`, data);
}

export async function deleteJobApi(id: number) {
  return requestClient.delete(`/cron-jobs/${id}`);
}

export async function triggerJobApi(id: number) {
  return requestClient.post<CronRun>(`/cron-jobs/${id}/trigger`);
}

// ---- 运行历史 ----

export async function getRunsApi(params: {
  jobId?: number;
  page: number;
  size: number;
}) {
  return requestClient.get<{ items: CronRun[]; total: number }>('/cron-runs', {
    params,
  });
}
