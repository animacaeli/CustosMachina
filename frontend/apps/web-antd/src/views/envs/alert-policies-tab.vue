<script lang="ts" setup>
/**
 * 项目告警策略（R1 模板化项目侧）：选管理员预设的告警模板 → 表单填占位符 →
 * 实时预览服务端渲染的查询 → 可覆盖触发参数 → 保存即同步 O2。
 * SQL 全程由服务端按模板渲染，本界面不提供手写入口。
 */
import type { AlertTemplate, O2Alert } from '#/api/observ/alerts';

import { computed, onMounted, reactive, ref, watch } from 'vue';

import { message } from 'ant-design-vue';

import {
  deleteO2AlertApi,
  getAlertTemplatesApi,
  getO2AlertsApi,
  renderAlertTemplateApi,
  upsertAlertFromTemplateApi,
} from '#/api/observ/alerts';
import YamlEditor from '#/components/yaml-editor.vue';

defineOptions({ name: 'ProjectAlertPoliciesTab' });

const props = defineProps<{ projectId: number }>();

const loading = ref(false);
const templates = ref<AlertTemplate[]>([]);
const alerts = ref<O2Alert[]>([]);

const projectAlerts = computed(() =>
  alerts.value.filter((a) => (a as any).projectId === props.projectId),
);

async function load() {
  loading.value = true;
  try {
    const [ts, as] = await Promise.all([
      getAlertTemplatesApi(),
      getO2AlertsApi(),
    ]);
    templates.value = ts;
    // dev 视角接口本来就只回项目侧；admin/ops 视角按 projectId 过滤
    alerts.value = (as as any[]).filter((a) => a.projectId > 0);
  } finally {
    loading.value = false;
  }
}

onMounted(load);

const columns = [
  { title: '策略名', dataIndex: 'name', width: 160 },
  { title: '类型', dataIndex: 'queryType', width: 80 },
  { title: '来源模板', key: 'tpl', width: 150 },
  { title: '级别', dataIndex: 'level', width: 80 },
  { title: '阈值', key: 'threshold', width: 90 },
  { title: '同步', dataIndex: 'syncStatus', width: 80 },
  { title: '启用', dataIndex: 'enabled', width: 70 },
  { title: '操作', key: 'action', width: 130 },
];

function tplName(id: number) {
  return templates.value.find((t) => t.id === id)?.name ?? `#${id}`;
}

const formOpen = ref(false);
const saving = ref(false);
const editingId = ref(0);
const form = reactive({
  name: '',
  templateId: undefined as number | undefined,
  streamName: '',
  description: '',
  period: 0,
  threshold: 0,
  silence: 0,
  level: '' as '' | 'critical' | 'info' | 'warn',
  enabled: true,
});
const params = ref<Record<string, string>>({});
const previewSql = ref('');
const previewing = ref(false);

const currentTpl = computed(() =>
  templates.value.find((t) => t.id === form.templateId),
);

watch(
  () => form.templateId,
  (id) => {
    params.value = {};
    previewSql.value = '';
    const t = templates.value.find((x) => x.id === id);
    if (t) {
      // 触发参数默认值带入（0/'' = 提交时服务端取模板默认）
      form.period = t.period;
      form.threshold = t.threshold;
      form.silence = t.silence;
      form.level = t.level;
      form.streamName = 'default';
      if (!form.name) form.name = '';
    }
  },
);

async function refreshPreview() {
  if (!form.templateId) return;
  previewing.value = true;
  try {
    const r = await renderAlertTemplateApi(form.templateId, params.value);
    previewSql.value = r.sql;
  } catch (error: any) {
    previewSql.value = `⚠ ${error?.response?.data?.message ?? error?.message ?? '渲染失败'}`;
  } finally {
    previewing.value = false;
  }
}

watch(params, refreshPreview, { deep: true });

function openCreate() {
  editingId.value = 0;
  Object.assign(form, {
    name: '',
    templateId: undefined,
    streamName: '',
    description: '',
    period: 0,
    threshold: 0,
    silence: 0,
    level: '',
    enabled: true,
  });
  params.value = {};
  previewSql.value = '';
  formOpen.value = true;
}

function openEdit(a: any) {
  editingId.value = a.id;
  Object.assign(form, {
    name: a.name,
    templateId: a.templateId,
    streamName: a.streamName,
    description: a.description ?? '',
    period: a.period,
    threshold: a.threshold,
    silence: a.silence,
    level: a.level,
    enabled: a.enabled,
  });
  params.value = { ...(a.paramsSnapshot ? JSON.parse(a.paramsSnapshot) : {}) };
  previewSql.value = a.sql;
  formOpen.value = true;
}

async function save() {
  if (!form.templateId) {
    message.warning('请选择告警模板');
    return;
  }
  if (!form.name.trim()) {
    message.warning('请填写策略名');
    return;
  }
  saving.value = true;
  try {
    await upsertAlertFromTemplateApi({
      id: editingId.value || undefined,
      name: form.name.trim(),
      templateId: form.templateId,
      projectId: props.projectId,
      params: params.value,
      streamName: form.streamName || undefined,
      description: form.description,
      period: form.period || undefined,
      threshold: form.threshold || undefined,
      silence: form.silence || undefined,
      level: form.level || undefined,
      enabled: form.enabled,
    });
    message.success(editingId.value ? '已更新并同步' : '已创建并同步');
    formOpen.value = false;
    await load();
  } finally {
    saving.value = false;
  }
}

async function remove(a: O2Alert) {
  await deleteO2AlertApi(a.id);
  message.success('已删除');
  await load();
}
</script>

<template>
  <div class="flex flex-col gap-3">
    <div class="flex items-center justify-between">
      <span class="text-gray-400">
        基于管理员预设的告警模板配置本项目的告警策略，保存后自动同步 O2
      </span>
      <a-button type="primary" @click="openCreate">新建策略</a-button>
    </div>
    <a-table
      :columns="columns"
      :data-source="projectAlerts"
      :loading="loading"
      :pagination="false"
      row-key="id"
      size="small"
    >
      <template #bodyCell="{ column, record }">
        <template v-if="column.key === 'tpl'">
          {{ tplName((record as any).templateId) }}
        </template>
        <template v-else-if="column.key === 'threshold'">
          {{ record.operator }} {{ record.threshold }} /
          {{ record.period }} 分钟
        </template>
        <template v-else-if="column.key === 'syncStatus'">
          <a-tag
            :color="
              record.syncStatus === 'synced'
                ? 'green'
                : record.syncStatus === 'failed'
                  ? 'red'
                  : 'blue'
            "
          >
            {{ record.syncStatus }}
          </a-tag>
        </template>
        <template v-else-if="column.key === 'action'">
          <a-space>
            <a-button size="small" type="link" @click="openEdit(record)">
              编辑
            </a-button>
            <a-popconfirm title="确认删除该策略？" @confirm="remove(record)">
              <a-button danger size="small" type="link">删除</a-button>
            </a-popconfirm>
          </a-space>
        </template>
      </template>
    </a-table>

    <a-drawer
      v-model:open="formOpen"
      :title="editingId ? '编辑告警策略' : '新建告警策略'"
      width="720"
    >
      <a-form layout="vertical" class="flex flex-col gap-1">
        <div class="grid grid-cols-2 gap-3">
          <a-form-item label="策略名" required>
            <a-input
              v-model:value="form.name"
              placeholder="如：支付服务错误突增"
            />
          </a-form-item>
          <a-form-item label="告警模板" required>
            <a-select
              v-model:value="form.templateId"
              :options="
                templates.map((t) => ({
                  label: `${t.name}${t.category ? `（${t.category}）` : ''}`,
                  value: t.id,
                }))
              "
              placeholder="选择管理员预设的模板"
              show-search
              option-filter-prop="label"
            />
          </a-form-item>
        </div>

        <template v-if="currentTpl">
          <a-alert
            :message="currentTpl.description || currentTpl.name"
            type="info"
          />
          <a-divider class="my-2" orientation="left" plain>模板参数</a-divider>
          <div class="grid grid-cols-2 gap-2">
            <a-input
              v-for="p in currentTpl.placeholders"
              :key="p.key"
              v-model:value="params[p.key]"
              :addon-before="p.label || p.key"
              :placeholder="
                p.hint || (p.required ? '必填' : `默认 ${p.default || '空'}`)
              "
            />
          </div>
          <a-divider class="my-2" orientation="left" plain>
            渲染预览（服务端产出，参数变化自动刷新）
          </a-divider>
          <YamlEditor
            :model-value="previewSql"
            height="140px"
            language="sql"
            read-only
          />
          <a-spin v-if="previewing" size="small" />

          <a-divider class="my-2" orientation="left" plain>
            触发参数（已带入模板默认）
          </a-divider>
          <div class="grid grid-cols-4 gap-3">
            <a-form-item label="窗口(分)">
              <a-input-number
                v-model:value="form.period"
                class="w-full"
                :min="1"
                :max="1440"
              />
            </a-form-item>
            <a-form-item label="阈值">
              <a-input-number
                v-model:value="form.threshold"
                class="w-full"
                :min="0"
              />
            </a-form-item>
            <a-form-item label="静默(分)">
              <a-input-number
                v-model:value="form.silence"
                class="w-full"
                :min="0"
                :max="1440"
              />
            </a-form-item>
            <a-form-item label="级别">
              <a-select
                v-model:value="form.level"
                :options="[
                  { label: 'info', value: 'info' },
                  { label: 'warn', value: 'warn' },
                  { label: 'critical', value: 'critical' },
                ]"
              />
            </a-form-item>
          </div>
        </template>
      </a-form>
      <template #footer>
        <a-space>
          <a-button @click="formOpen = false">取消</a-button>
          <a-button :loading="saving" type="primary" @click="save">
            保存并同步
          </a-button>
        </a-space>
      </template>
    </a-drawer>
  </div>
</template>
