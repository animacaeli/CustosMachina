<script lang="ts" setup>
import type { BackupJob, BackupRun } from '#/api/system/backup';

import { onMounted, reactive, ref } from 'vue';

import { message } from 'ant-design-vue';

import { getServerListApi } from '#/api/resources/server';
import {
  createBackupJobApi,
  deleteBackupJobApi,
  getBackupJobsApi,
  getBackupRunsApi,
  runBackupJobApi,
  updateBackupJobApi,
} from '#/api/system/backup';

defineOptions({ name: 'SystemBackup' });

const loading = ref(false);
const list = ref<BackupJob[]>([]);
const servers = ref<Array<{ id: number; name: string }>>([]);
const historyJobId = ref<0 | number>(0);
const history = ref<BackupRun[]>([]);
const historyLoading = ref(false);
const running = ref(0);

async function load() {
  loading.value = true;
  try {
    const [js, ss] = await Promise.all([
      getBackupJobsApi(),
      getServerListApi(),
    ]);
    list.value = js;
    servers.value = ss;
  } finally {
    loading.value = false;
  }
}

onMounted(load);

const typeText: Record<string, string> = {
  platform_self: '平台自身',
  remote_dir: '远端目录',
};

const columns = [
  { title: 'ID', dataIndex: 'id', width: 56 },
  { title: '名称', dataIndex: 'name' },
  { title: '类型', dataIndex: 'type', width: 100 },
  { title: '调度', key: 'schedule', width: 130 },
  { title: '存储', key: 'storage', width: 150 },
  { title: '保留', dataIndex: 'retentionCount', width: 60 },
  { title: '启用', dataIndex: 'enabled', width: 70 },
  { title: '操作', key: 'action', width: 230 },
];

const formOpen = ref(false);
const saving = ref(false);
const editingId = ref<null | number>(null);
const form = reactive({
  name: '',
  type: 'platform_self' as 'platform_self' | 'remote_dir',
  schedule: '',
  enabled: true,
  serverId: undefined as number | undefined,
  remotePath: '',
  storage: 'local' as 'local' | 's3',
  s3Endpoint: '',
  s3Bucket: '',
  s3Prefix: '',
  s3AccessKey: '',
  s3SecretKey: '',
  localDir: '',
  passphrase: '',
  retentionCount: 7,
});

function openCreate() {
  editingId.value = null;
  Object.assign(form, {
    name: '',
    type: 'platform_self',
    schedule: '',
    enabled: true,
    serverId: undefined,
    remotePath: '',
    storage: 'local',
    s3Endpoint: '',
    s3Bucket: '',
    s3Prefix: '',
    s3AccessKey: '',
    s3SecretKey: '',
    localDir: '',
    passphrase: '',
    retentionCount: 7,
  });
  formOpen.value = true;
}

function openEdit(j: BackupJob) {
  editingId.value = j.id;
  Object.assign(form, {
    name: j.name,
    type: j.type,
    schedule: j.schedule,
    enabled: j.enabled,
    serverId: j.serverId || undefined,
    remotePath: j.remotePath,
    storage: j.storage,
    s3Endpoint: j.s3Endpoint,
    s3Bucket: j.s3Bucket,
    s3Prefix: j.s3Prefix,
    s3AccessKey: '',
    s3SecretKey: '',
    localDir: j.localDir,
    passphrase: '',
    retentionCount: j.retentionCount,
  });
  formOpen.value = true;
}

async function save() {
  if (!form.name) {
    message.warning('请填写任务名称');
    return;
  }
  if (form.storage === 'local' && !form.localDir) {
    message.warning('本地存储须填写产物目录');
    return;
  }
  if (
    form.storage === 's3' &&
    (!form.s3Endpoint || !form.s3Bucket || !form.s3AccessKey)
  ) {
    message.warning('S3 存储须填写 endpoint/bucket/accessKey');
    return;
  }
  if (form.type === 'remote_dir' && (!form.serverId || !form.remotePath)) {
    message.warning('远端目录任务须选择主机并填写目录');
    return;
  }
  if (form.type === 'platform_self' && !editingId.value && !form.passphrase) {
    message.warning('平台自身备份必须设置口令（用于加密密钥份额）');
    return;
  }
  saving.value = true;
  try {
    const payload = {
      ...form,
      serverId: form.serverId ?? 0,
      schedule: form.schedule || '',
    };
    if (editingId.value) {
      await updateBackupJobApi(editingId.value, payload);
      message.success('任务已更新');
    } else {
      await createBackupJobApi(payload);
      message.success('任务已创建');
    }
    formOpen.value = false;
    await load();
  } catch (error: any) {
    message.error(error?.response?.data?.message || '保存失败');
  } finally {
    saving.value = false;
  }
}

async function remove(j: BackupJob) {
  await deleteBackupJobApi(j.id);
  message.success('已删除');
  await load();
}

async function run(j: BackupJob) {
  running.value = j.id;
  try {
    const runResult = await runBackupJobApi(j.id);
    if (runResult.status === 'success') {
      message.success(`备份完成：${runResult.artifact}`);
    } else {
      message.warning(runResult.output || '备份失败，查看运行历史');
    }
    await loadHistory(j.id);
  } finally {
    running.value = 0;
  }
}

async function loadHistory(id: number) {
  historyJobId.value = id;
  historyLoading.value = true;
  try {
    history.value = await getBackupRunsApi(id);
  } finally {
    historyLoading.value = false;
  }
}

const statusColor: Record<string, string> = {
  success: 'green',
  failed: 'red',
  running: 'blue',
  unknown: 'orange',
};
const statusText: Record<string, string> = {
  success: '成功',
  failed: '失败',
  running: '运行中',
  unknown: '未知',
};

function humanBytes(n: number) {
  if (!n) return '—';
  const units = ['B', 'KB', 'MB', 'GB'];
  let v = n;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(1)}${units[i]}`;
}
</script>

<template>
  <div class="p-4">
    <div class="mb-3 flex items-center justify-between">
      <span class="text-muted-foreground text-xs">
        platform_self 含密钥份额（口令加密），恢复见 docs/RESTORE.md 与
        deploy/restore.sh；失败告警走「管理后台 → 通知路由」backup_failed 源
      </span>
      <a-button type="primary" @click="openCreate">新建任务</a-button>
    </div>

    <a-table
      :columns="columns"
      :data-source="list"
      :loading="loading"
      :pagination="false"
      row-key="id"
      size="small"
    >
      <template #bodyCell="{ column, record }">
        <template v-if="column.dataIndex === 'type'">
          {{ typeText[record.type] ?? record.type }}
        </template>
        <template v-else-if="column.key === 'schedule'">
          {{ record.schedule || '仅手动' }}
        </template>
        <template v-else-if="column.key === 'storage'">
          {{
            record.storage === 's3'
              ? `S3：${record.s3Bucket}/${record.s3Prefix}`
              : `本地：${record.localDir}`
          }}
        </template>
        <template v-else-if="column.dataIndex === 'enabled'">
          <a-tag :color="record.enabled ? 'green' : 'default'">
            {{ record.enabled ? '启用' : '停用' }}
          </a-tag>
        </template>
        <template v-else-if="column.key === 'action'">
          <a-space>
            <a-button
              size="small"
              type="primary"
              :loading="running === record.id"
              @click="run(record)"
            >
              立即备份
            </a-button>
            <a-button size="small" @click="loadHistory(record.id)">
              历史
            </a-button>
            <a-button size="small" @click="openEdit(record)">编辑</a-button>
            <a-popconfirm title="确认删除该任务？" @confirm="remove(record)">
              <a-button danger size="small">删除</a-button>
            </a-popconfirm>
          </a-space>
        </template>
      </template>
    </a-table>

    <!-- 运行历史 -->
    <div v-if="historyJobId" class="mt-6">
      <div class="mb-2 flex items-center justify-between">
        <h3 class="text-base font-medium">
          运行历史（任务 #{{ historyJobId }}）
        </h3>
        <a-button size="small" @click="loadHistory(historyJobId)">
          刷新
        </a-button>
      </div>
      <a-table
        :columns="[
          { title: '时间', dataIndex: 'startedAt', width: 180 },
          { title: '触发', dataIndex: 'trigger', width: 80 },
          { title: '状态', dataIndex: 'status', width: 90 },
          { title: '产物', dataIndex: 'artifact' },
          { title: '大小', key: 'size', width: 90 },
          { title: '输出', dataIndex: 'output' },
        ]"
        :data-source="history"
        :loading="historyLoading"
        :pagination="{ pageSize: 10 }"
        row-key="id"
        size="small"
      >
        <template #bodyCell="{ column, record }">
          <template v-if="column.dataIndex === 'status'">
            <a-tag :color="statusColor[record.status]">
              {{ statusText[record.status] ?? record.status }}
            </a-tag>
          </template>
          <template v-else-if="column.key === 'size'">
            {{ humanBytes(record.sizeBytes) }}
          </template>
        </template>
      </a-table>
    </div>

    <a-modal
      v-model:open="formOpen"
      :confirm-loading="saving"
      :title="editingId ? '编辑备份任务' : '新建备份任务'"
      width="640px"
      @ok="save"
    >
      <a-form layout="vertical">
        <a-form-item label="任务名称" required>
          <a-input v-model:value="form.name" placeholder="如：平台每日备份" />
        </a-form-item>
        <a-form-item label="类型" required>
          <a-radio-group v-model:value="form.type">
            <a-radio value="platform_self">
              平台自身（DB + 数据目录 + 密钥份额）
            </a-radio>
            <a-radio value="remote_dir">远端目录</a-radio>
          </a-radio-group>
        </a-form-item>
        <template v-if="form.type === 'remote_dir'">
          <a-form-item label="目标主机" required>
            <a-select
              v-model:value="form.serverId"
              :options="servers.map((s) => ({ label: s.name, value: s.id }))"
              placeholder="选择主机"
            />
          </a-form-item>
          <a-form-item label="远端目录" required>
            <a-input
              v-model:value="form.remotePath"
              placeholder="/data/myapp"
            />
          </a-form-item>
        </template>
        <a-form-item
          label="调度表达式（标准 5 段 crontab，如 0 3 * * *；留空 = 仅手动）"
        >
          <a-input
            v-model:value="form.schedule"
            placeholder="0 3 * * * = 每天凌晨 3 点"
          />
        </a-form-item>
        <a-form-item label="存储" required>
          <a-radio-group v-model:value="form.storage">
            <a-radio value="local">本地目录</a-radio>
            <a-radio value="s3">S3 兼容对象存储</a-radio>
          </a-radio-group>
        </a-form-item>
        <template v-if="form.storage === 'local'">
          <a-form-item
            label="产物目录（建议在 /data 之外，避免自备份递归排除）"
            required
          >
            <a-input
              v-model:value="form.localDir"
              placeholder="/data/backups-ext"
            />
          </a-form-item>
        </template>
        <template v-else>
          <a-form-item label="Endpoint" required>
            <a-input
              v-model:value="form.s3Endpoint"
              placeholder="s3.cn-hangzhou.aliyuncs.com"
            />
          </a-form-item>
          <a-form-item label="Bucket" required>
            <a-input v-model:value="form.s3Bucket" />
          </a-form-item>
          <a-form-item label="AccessKey / SecretKey" required>
            <div class="flex gap-2">
              <a-input
                v-model:value="form.s3AccessKey"
                class="flex-1"
                placeholder="AccessKey"
              />
              <a-input-password
                v-model:value="form.s3SecretKey"
                class="flex-1"
                :placeholder="editingId ? '留空保留' : 'SecretKey'"
              />
            </div>
          </a-form-item>
        </template>
        <a-form-item
          v-if="form.type === 'platform_self'"
          label="备份口令（加密密钥份额；恢复时必需，请妥善保管）"
          :required="!editingId"
        >
          <a-input-password
            v-model:value="form.passphrase"
            :placeholder="editingId ? '留空保留原口令' : '设置备份口令'"
          />
        </a-form-item>
        <a-form-item label="保留份数（超限自动删最旧）">
          <a-input-number
            v-model:value="form.retentionCount"
            :max="365"
            :min="1"
          />
        </a-form-item>
        <a-form-item label="启用">
          <a-switch v-model:checked="form.enabled" />
        </a-form-item>
      </a-form>
    </a-modal>
  </div>
</template>
