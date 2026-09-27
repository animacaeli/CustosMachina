import { requestClient } from '#/api/request';

export interface CronScript {
  boundCount?: number;
  content: string;
  project: string;
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
  network: string;
  retry: number;
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
  /** 全量日志文件（目标机路径，超 64KB 截断时的下载兜底） */
  outputFile: string;
  /** 下载全量日志用（SFTP ticket） */
  serverId: number;
  /** running | success | failed | timeout | skipped | unknown */
  status: string;
  /** schedule | manual | retry */
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

/** 表达式预览：未来 count 次触发时间（标准 5 段 crontab） */
export async function previewScheduleApi(schedule: string, count = 5) {
  return requestClient.get<{ times: string[] }>('/cron-jobs/preview', {
    params: { count, schedule },
  });
}

// ---- 运行历史 ----

/** 单条运行记录（实时日志轮询用：running 状态时 output 增量更新） */
export async function getRunApi(id: number) {
  return requestClient.get<CronRun>(`/cron-runs/${id}`);
}

export async function getRunsApi(params: {
  jobId?: number;
  page: number;
  size: number;
}) {
  return requestClient.get<{ items: CronRun[]; total: number }>('/cron-runs', {
    params,
  });
}
