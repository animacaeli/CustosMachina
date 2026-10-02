<script lang="ts" setup>
import type { NotifyGroup, NotifyRule } from '#/api/projects';

import { onMounted, reactive, ref } from 'vue';

import { message } from 'ant-design-vue';

import {
  createNotifyRuleApi,
  deleteNotifyRuleApi,
  getNotifyGroupsApi,
  getNotifyRulesApi,
  testNotifyRuleApi,
  updateNotifyRuleApi,
} from '#/api/projects';

defineOptions({ name: 'AdminNotifyRoutes' });

const loading = ref(false);
const list = ref<NotifyRule[]>([]);
const groups = ref<NotifyGroup[]>([]);

// 事件源选项（与后端 ValidSources 对齐；预留源对应里程碑接入后生效）
const SOURCES: Array<{ label: string; value: string }> = [
  { label: '定时任务失败', value: 'cron_failed' },
  { label: '观测组件失败', value: 'observ_failed' },
  { label: '平台运维事件（服务器不可达等）', value: 'platform_ops' },
  { label: 'O2 告警（M3 接入）', value: 'o2_alert' },
  { label: '证书到期/续期失败（M5 接入）', value: 'cert_expiring' },
  { label: '备份失败（M2 接入）', value: 'backup_failed' },
  { label: 'AI 摘要（M6 接入）', value: 'ai_digest' },
];
const LEVELS: Array<{ label: string; value: string }> = [
  { label: 'info 及以上（全部）', value: 'info' },
  { label: 'warn 及以上', value: 'warn' },
  { label: '仅 critical', value: 'critical' },
];
const sourceLabel = (v: string) =>
  SOURCES.find((s) => s.value === v)?.label ?? v;
const groupLabel = (id: number) =>
  groups.value.find((g) => g.id === id)?.name ?? `#${id}`;

async function load() {
  loading.value = true;
  try {
    const [rs, gs] = await Promise.all([
      getNotifyRulesApi(),
      getNotifyGroupsApi(),
    ]);
    list.value = rs;
    groups.value = gs;
  } finally {
    loading.value = false;
  }
}

onMounted(load);

const columns = [
  { title: 'ID', dataIndex: 'id', width: 60 },
  { title: '名称', dataIndex: 'name' },
  { title: '事件源', key: 'source', width: 200 },
  { title: '最低级别', dataIndex: 'minLevel', width: 110 },
  { title: '目标群', key: 'group', width: 160 },
  { title: '静默时段', key: 'silent', width: 130 },
  { title: '聚合窗口', key: 'agg', width: 100 },
  { title: '启用', dataIndex: 'enabled', width: 70 },
  { title: '操作', key: 'action', width: 200 },
];

const formOpen = ref(false);
const saving = ref(false);
const editingId = ref<null | number>(null);
const form = reactive<{
  aggregateSec: number;
  enabled: boolean;
  groupId: number | undefined;
  minLevel: string;
  name: string;
  silentEnd: string;
  silentStart: string;
  source: string;
}>({
  name: '',
  source: 'cron_failed',
  minLevel: 'warn',
  groupId: undefined,
  silentStart: '',
  silentEnd: '',
  aggregateSec: 0,
  enabled: true,
});

function openCreate() {
  editingId.value = null;
  form.name = '';
  form.source = 'cron_failed';
  form.minLevel = 'warn';
  form.groupId = groups.value[0]?.id;
  form.silentStart = '';
  form.silentEnd = '';
  form.aggregateSec = 0;
  form.enabled = true;
  formOpen.value = true;
}

function openEdit(r: NotifyRule) {
  editingId.value = r.id;
  form.name = r.name;
  form.source = r.source;
  form.minLevel = r.minLevel;
  form.groupId = r.groupId;
  form.silentStart = r.silentStart;
  form.silentEnd = r.silentEnd;
  form.aggregateSec = r.aggregateSec;
  form.enabled = r.enabled;
  formOpen.value = true;
}

async function save() {
  if (!form.name || !form.groupId) {
    message.warning('请填写名称并选择目标群');
    return;
  }
  if ((form.silentStart === '') !== (form.silentEnd === '')) {
    message.warning('静默起止时间须成对填写');
    return;
  }
  saving.value = true;
  try {
    const payload = {
      name: form.name,
      source: form.source,
      minLevel: form.minLevel as 'critical' | 'info' | 'warn',
      groupId: form.groupId,
      silentStart: form.silentStart,
      silentEnd: form.silentEnd,
      aggregateSec: form.aggregateSec,
      enabled: form.enabled,
    };
    if (editingId.value) {
      await updateNotifyRuleApi(editingId.value, payload);
      message.success('规则已更新');
    } else {
      await createNotifyRuleApi(payload);
      message.success('规则已创建');
    }
    formOpen.value = false;
    await load();
  } finally {
    saving.value = false;
  }
}

async function remove(r: NotifyRule) {
  await deleteNotifyRuleApi(r.id);
  message.success('已删除');
  await load();
}

const testing = ref(0);
async function test(r: NotifyRule) {
  testing.value = r.id;
  try {
    await testNotifyRuleApi(r.id);
    message.success('测试事件已发出（聚合窗口请稍候查看群消息）');
  } finally {
    testing.value = 0;
  }
}
</script>

<template>
  <div>
    <div class="mb-3 flex items-center justify-between">
      <span class="text-muted-foreground text-xs">
        事件按来源与级别路由到目标群；critical
        永不被静默；未匹配任何规则时走运维群兜底
      </span>
      <a-button type="primary" @click="openCreate">新建规则</a-button>
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
        <template v-if="column.key === 'source'">
          {{ sourceLabel(record.source) }}
        </template>
        <template v-else-if="column.key === 'group'">
          {{ groupLabel(record.groupId) }}
        </template>
        <template v-else-if="column.key === 'silent'">
          {{
            record.silentStart
              ? `${record.silentStart}~${record.silentEnd}`
              : '—'
          }}
        </template>
        <template v-else-if="column.key === 'agg'">
          {{ record.aggregateSec ? `${record.aggregateSec}s` : '—' }}
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
              :loading="testing === record.id"
              @click="test(record)"
            >
              测试
            </a-button>
            <a-button size="small" @click="openEdit(record)">编辑</a-button>
            <a-popconfirm title="确认删除该规则？" @confirm="remove(record)">
              <a-button danger size="small">删除</a-button>
            </a-popconfirm>
          </a-space>
        </template>
      </template>
    </a-table>

    <a-modal
      v-model:open="formOpen"
      :confirm-loading="saving"
      :title="editingId ? '编辑路由规则' : '新建路由规则'"
      @ok="save"
    >
      <a-form layout="vertical">
        <a-form-item label="规则名称" required>
          <a-input
            v-model:value="form.name"
            placeholder="如：任务失败→运维群"
          />
        </a-form-item>
        <a-form-item label="事件源" required>
          <a-select v-model:value="form.source" :options="SOURCES" />
        </a-form-item>
        <a-form-item label="最低级别" required>
          <a-select v-model:value="form.minLevel" :options="LEVELS" />
        </a-form-item>
        <a-form-item label="目标通知群" required>
          <a-select
            v-model:value="form.groupId"
            :options="groups.map((g) => ({ label: g.name, value: g.id }))"
            placeholder="选择通知群"
          />
        </a-form-item>
        <a-form-item label="静默时段（本地时区 HH:MM，支持跨零点；留空不静默）">
          <div class="flex items-center gap-2">
            <a-time-picker
              v-model:value="form.silentStart"
              format="HH:mm"
              :allow-empty="true"
              placeholder="开始"
              value-format="HH:mm"
            />
            <span>~</span>
            <a-time-picker
              v-model:value="form.silentEnd"
              format="HH:mm"
              :allow-empty="true"
              placeholder="结束"
              value-format="HH:mm"
            />
          </div>
        </a-form-item>
        <a-form-item
          label="聚合窗口（秒，同类事件窗口内合并为一条；0 = 立即投递）"
        >
          <a-input-number
            v-model:value="form.aggregateSec"
            :max="3600"
            :min="0"
            placeholder="如 300"
            style="width: 100%"
          />
        </a-form-item>
        <a-form-item label="启用">
          <a-switch v-model:checked="form.enabled" />
        </a-form-item>
      </a-form>
    </a-modal>
  </div>
</template>
