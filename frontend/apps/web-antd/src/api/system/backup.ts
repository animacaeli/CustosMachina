/** 备份任务（P5 M2） */
import { requestClient } from '#/api/request';

export interface BackupJob {
  createdAt: string;
  enabled: boolean;
  hasPassphrase?: boolean;
  hasS3Secret?: boolean;
  id: number;
  localDir: string;
  name: string;
  nextRunAt?: string;
  remotePath: string;
  retentionCount: number;
  s3Bucket: string;
  s3Endpoint: string;
  s3Prefix: string;
  schedule: string;
  serverId: number;
  storage: 'local' | 's3';
  type: 'platform_self' | 'remote_dir';
}

export interface SaveBackupJobInput {
  enabled: boolean;
  /** platform_self 密钥份额口令；留空 = 保留原值 */
  passphrase?: string;
  retentionCount: number;
  /** 远端目录路径（remote_dir） */
  remotePath?: string;
  /** 标准 5 段 crontab；空 = 仅手动 */
  schedule?: string;
  s3AccessKey?: string;
  s3Bucket?: string;
  s3Endpoint?: string;
  s3Prefix?: string;
  /** 留空 = 保留原值 */
  s3SecretKey?: string;
  serverId: number;
  storage: 'local' | 's3';
  type: 'platform_self' | 'remote_dir';
  name: string;
  localDir?: string;
}

export interface BackupRun {
  artifact: string;
  createdAt: string;
  id: number;
  jobId: number;
  jobName: string;
  output: string;
  sizeBytes: number;
  startedAt: string;
  status: 'failed' | 'running' | 'success' | 'unknown';
  trigger: 'manual' | 'schedule';
}

export async function getBackupJobsApi() {
  return requestClient.get<BackupJob[]>('/backup-jobs');
}

export async function createBackupJobApi(data: SaveBackupJobInput) {
  return requestClient.post<BackupJob>('/backup-jobs', data);
}

export async function updateBackupJobApi(id: number, data: SaveBackupJobInput) {
  return requestClient.put<BackupJob>(`/backup-jobs/${id}`, data);
}

export async function deleteBackupJobApi(id: number) {
  return requestClient.delete(`/backup-jobs/${id}`);
}

export async function runBackupJobApi(id: number) {
  return requestClient.post<BackupRun>(`/backup-jobs/${id}/run`);
}

export async function getBackupRunsApi(id: number) {
  return requestClient.get<BackupRun[]>(`/backup-jobs/${id}/runs`);
}
