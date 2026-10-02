<script lang="ts" setup>
import type { O2Alert, O2Settings } from '#/api/observ/alerts';

import { onMounted, reactive, ref } from 'vue';

import { message } from 'ant-design-vue';

import {
  createO2AlertApi,
  deleteO2AlertApi,
  getO2AlertsApi,
  getO2SettingsApi,
  saveO2SettingsApi,
  syncO2AlertsApi,
  updateO2AlertApi,
} from '#/api/observ/alerts';

defineOptions({ name: 'ObservAlerts' });

const loading = ref(false);
const list = ref<O2Alert[]>([]);
const settings = ref<O2Settings>({ configured: false, email: '', org: '' });
const syncing = ref(false);

async function load() {
  loading.value = true;
  try {
    const [as, st] = await Promise.all([getO2AlertsApi(), getO2SettingsApi()]);
    list.value = as;
    settings.value = st;
  } finally {
    loading.value = false;
  }
}

onMounted(load);

const columns = [
  { title: 'ID', dataIndex: 'id', width: 56 },
  { title: '名称', dataIndex: 'name' },
  { title: '数据流', key: 'stream', width: 160 },
  { title: '条件', key: 'cond', width: 170 },
  { title: '级别', dataIndex: 'level', width: 90 },
  { title: '同步', key: 'sync', width: 110 },
  { title: '启用', dataIndex: 'enabled', width: 70 },
  { title: '操作', key: 'action', width: 150 },
];

const formOpen = ref(false);
const saving = ref(false);
const editingId = ref<null | number>(null);
const form = reactive({
  name: '',
  streamName: '',
  streamType: 'logs' as 'logs' | 'metrics' | 'traces',
  sql: '',
  period: 10,
  operator: '>=',
  threshold: 1,
  frequency: 1,
  silence: 10,
  enabled: true,
  description: '',
  level: 'warn' as 'critical' | 'info' | 'warn',
});

function openCreate() {
  editingId.value = null;
  Object.assign(form, {
    name: '',
    streamName: '',
    streamType: 'logs',
    sql: '',
    period: 10,
    operator: '>=',
    threshold: 1,
    frequency: 1,
    silence: 10,
    enabled: true,
    description: '',
    level: 'warn',
  });
  formOpen.value = true;
}

function openEdit(a: O2Alert) {
  editingId.value = a.id;
  Object.assign(form, {
    name: a.name,
    streamName: a.streamName,
    streamType: a.streamType,
    sql: a.sql,
    period: a.period,
    operator: a.operator,
    threshold: a.threshold,
    frequency: a.frequency,
    silence: a.silence,
    enabled: a.enabled,
    description: a.description,
    level: a.level,
  });
  formOpen.value = true;
}

async function save() {
  if (!form.name || !form.streamName || !form.sql) {
    message.warning('请填写名称、数据流与 SQL');
    return;
  }
  saving.value = true;
  try {
    if (editingId.value) {
      await updateO2AlertApi(editingId.value, form);
      message.success('已更新（正在同步 O2）');
    } else {
      await createO2AlertApi(form);
      message.success('已创建（正在同步 O2）');
    }
    formOpen.value = false;
    setTimeout(load, 800);
  } catch (error: any) {
    message.error(error?.response?.data?.message || '保存失败');
  } finally {
    saving.value = false;
  }
}

async function remove(a: O2Alert) {
  await deleteO2AlertApi(a.id);
  message.success('已删除');
  await load();
}

async function syncAll() {
  syncing.value = true;
  try {
    const r = await syncO2AlertsApi();
    message.success(`已触发 ${r.synced} 条同步`);
    setTimeout(load, 1500);
  } finally {
    syncing.value = false;
  }
}

// O2 连接配置
const cfgOpen = ref(false);
const cfgSaving = ref(false);
const cfg = reactive({ email: '', password: '', org: 'default' });

function openCfg() {
  cfg.email = settings.value.email;
  cfg.password = '';
  cfg.org = settings.value.org || 'default';
  cfgOpen.value = true;
}

async function saveCfg() {
  cfgSaving.value = true;
  try {
    settings.value = await saveO2SettingsApi({
      email: cfg.email || undefined,
      password: cfg.password || undefined,
      org: cfg.org || undefined,
    });
    message.success('O2 连接已保存');
    cfgOpen.value = false;
  } finally {
    cfgSaving.value = false;
  }
}

const syncColor: Record<string, string> = {
  synced: 'green',
  failed: 'red',
  pending: 'orange',
};
const syncText: Record<string, string> = {
  synced: '已同步',
  failed: '失败',
  pending: '同步中',
};
const levelColor: Record<string, string> = {
  info: 'blue',
  warn: 'orange',
  critical: 'red',
};
</script>

<template>
  <div class="p-4">
    <div class="mb-3 flex items-center justify-between">
      <span class="text-muted-foreground text-xs">
        平台定义告警 → 自动同步 O2（destination/webhook
        由平台维护）；触发后经「通知路由」投递
      </span>
      <a-space>
        <a-button @click="openCfg">
          O2 连接{{ settings.configured ? '' : '（未配置）' }}
        </a-button>
        <a-button :loading="syncing" @click="syncAll">全量同步</a-button>
        <a-button type="primary" @click="openCreate">新建告警</a-button>
      </a-space>
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
        <template v-if="column.key === 'stream'">
          {{ record.streamName }}（{{ record.streamType }}）
        </template>
        <template v-else-if="column.key === 'cond'">
          {{ record.operator }} {{ record.threshold }} 条 /
          {{ record.period }} 分钟，每 {{ record.frequency }} 分钟查
        </template>
        <template v-else-if="column.dataIndex === 'level'">
          <a-tag :color="levelColor[record.level]">{{ record.level }}</a-tag>
        </template>
        <template v-else-if="column.key === 'sync'">
          <a-tooltip v-if="record.syncError" :title="record.syncError">
            <a-tag :color="syncColor[record.syncStatus]">
              {{ syncText[record.syncStatus] }}?
            </a-tag>
          </a-tooltip>
          <a-tag v-else :color="syncColor[record.syncStatus]">
            {{ syncText[record.syncStatus] ?? record.syncStatus }}
          </a-tag>
        </template>
        <template v-else-if="column.dataIndex === 'enabled'">
          <a-tag :color="record.enabled ? 'green' : 'default'">
            {{ record.enabled ? '启用' : '停用' }}
          </a-tag>
        </template>
        <template v-else-if="column.key === 'action'">
          <a-space>
            <a-button size="small" @click="openEdit(record)">编辑</a-button>
            <a-popconfirm title="确认删除？" @confirm="remove(record)">
              <a-button danger size="small">删除</a-button>
            </a-popconfirm>
          </a-space>
        </template>
      </template>
    </a-table>

    <a-modal
      v-model:open="formOpen"
      :confirm-loading="saving"
      :title="editingId ? '编辑告警' : '新建告警'"
      width="620px"
      @ok="save"
    >
      <a-form layout="vertical">
        <a-form-item label="告警名称" required>
          <a-input v-model:value="form.name" placeholder="如 error-spike" />
        </a-form-item>
        <a-form-item label="数据流" required>
          <div class="flex gap-2">
            <a-input
              v-model:value="form.streamName"
              class="flex-1"
              placeholder="日志流名（如 default）"
            />
            <a-select
              v-model:value="form.streamType"
              class="w-28"
              :options="[
                { label: 'logs', value: 'logs' },
                { label: 'metrics', value: 'metrics' },
                { label: 'traces', value: 'traces' },
              ]"
            />
          </div>
        </a-form-item>
        <a-form-item label="SQL 查询" required>
          <a-textarea
            v-model:value="form.sql"
            :rows="4"
            placeholder="SELECT count(*) AS cnt WHERE level = 'ERROR'"
          />
          <span class="text-muted-foreground text-xs">
            查询结果列值会与阈值比较（取首行首数值列）
          </span>
        </a-form-item>
        <a-form-item label="触发条件">
          <div class="flex flex-wrap items-center gap-2">
            <span>窗口</span>
            <a-input-number v-model:value="form.period" :min="1" />
            <span>分钟内命中</span>
            <a-select
              v-model:value="form.operator"
              class="w-20"
              :options="
                ['>=', '>', '<=', '<', '=', '!='].map((v) => ({
                  label: v,
                  value: v,
                }))
              "
            />
            <a-input-number v-model:value="form.threshold" :min="1" />
            <span>条</span>
          </div>
        </a-form-item>
        <a-form-item label="检查频率（分钟）与触发后静默（分钟）">
          <div class="flex items-center gap-2">
            <a-input-number v-model:value="form.frequency" :min="1" />
            <span>/</span>
            <a-input-number v-model:value="form.silence" :min="0" />
          </div>
        </a-form-item>
        <a-form-item label="通知级别（平台通知路由用）">
          <a-select
            v-model:value="form.level"
            :options="[
              { label: 'info', value: 'info' },
              { label: 'warn', value: 'warn' },
              { label: 'critical', value: 'critical' },
            ]"
            class="w-32"
          />
        </a-form-item>
        <a-form-item label="备注">
          <a-input v-model:value="form.description" />
        </a-form-item>
        <a-form-item label="启用">
          <a-switch v-model:checked="form.enabled" />
        </a-form-item>
      </a-form>
    </a-modal>

    <a-modal
      v-model:open="cfgOpen"
      :confirm-loading="cfgSaving"
      title="OpenObserve 连接配置"
      @ok="saveCfg"
    >
      <a-form layout="vertical">
        <a-form-item label="认证用户（O2 root 用户邮箱）">
          <a-input v-model:value="cfg.email" placeholder="admin@example.com" />
        </a-form-item>
        <a-form-item label="密码（留空保留）">
          <a-input-password v-model:value="cfg.password" />
        </a-form-item>
        <a-form-item label="组织">
          <a-input v-model:value="cfg.org" placeholder="default" />
        </a-form-item>
        <p class="text-muted-foreground text-xs">
          O2 地址在「观测组件」页配置；平台对外地址（webhook
          回流）由部署环境注入。 凭证 AES 加密落库。
        </p>
      </a-form>
    </a-modal>
  </div>
</template>
