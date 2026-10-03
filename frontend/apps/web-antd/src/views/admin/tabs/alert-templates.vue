<script lang="ts" setup>
/**
 * 告警模板（R1 模板化）：管理员预设带 {{占位符}} 的查询体与默认触发参数，
 * 项目侧（含 dev）填参实例化后同步 O2。占位符与查询体双向一致性由后端强校验。
 */
import type { AlertPlaceholder, AlertTemplate } from '#/api/observ/alerts';

import { onMounted, reactive, ref } from 'vue';

import { message } from 'ant-design-vue';

import {
  createAlertTemplateApi,
  deleteAlertTemplateApi,
  getAlertTemplatesApi,
  renderAlertTemplateApi,
  updateAlertTemplateApi,
} from '#/api/observ/alerts';
import YamlEditor from '#/components/yaml-editor.vue';

defineOptions({ name: 'AdminAlertTemplates' });

const loading = ref(false);
const list = ref<AlertTemplate[]>([]);

async function load() {
  loading.value = true;
  try {
    list.value = await getAlertTemplatesApi();
  } finally {
    loading.value = false;
  }
}

onMounted(load);

const columns = [
  { title: 'ID', dataIndex: 'id', width: 56 },
  { title: '名称', dataIndex: 'name', width: 160 },
  { title: '分类', dataIndex: 'category', width: 100 },
  { title: '类型', dataIndex: 'queryType', width: 80 },
  { title: '占位符', key: 'ph', width: 90 },
  { title: '引用', dataIndex: 'boundCount', width: 70 },
  { title: '默认级别', dataIndex: 'level', width: 90 },
  { title: '操作', key: 'action', width: 170 },
];

const formOpen = ref(false);
const saving = ref(false);
const editingId = ref<null | number>(null);
const form = reactive({
  name: '',
  description: '',
  category: '',
  queryType: 'sql' as 'promql' | 'sql',
  query: '',
  period: 10,
  operator: '>=',
  threshold: 1,
  frequency: 1,
  silence: 10,
  level: 'warn' as 'critical' | 'info' | 'warn',
});
const placeholders = ref<Array<AlertPlaceholder>>([]);
// 渲染测试
const testParams = ref<Record<string, string>>({});
const renderedSql = ref('');
const rendering = ref(false);

function openCreate() {
  editingId.value = null;
  Object.assign(form, {
    name: '',
    description: '',
    category: '',
    queryType: 'sql',
    query: '',
    period: 10,
    operator: '>=',
    threshold: 1,
    frequency: 1,
    silence: 10,
    level: 'warn',
  });
  placeholders.value = [];
  testParams.value = {};
  renderedSql.value = '';
  formOpen.value = true;
}

function openEdit(t: AlertTemplate) {
  editingId.value = t.id;
  Object.assign(form, {
    name: t.name,
    description: t.description,
    category: t.category,
    queryType: t.queryType,
    query: t.query,
    period: t.period,
    operator: t.operator,
    threshold: t.threshold,
    frequency: t.frequency,
    silence: t.silence,
    level: t.level,
  });
  placeholders.value = t.placeholders.map((p) => ({ ...p }));
  testParams.value = {};
  renderedSql.value = '';
  formOpen.value = true;
}

function addPlaceholder() {
  placeholders.value.push({
    key: '',
    label: '',
    type: 'string',
    required: true,
    default: '',
    hint: '',
  });
}

async function save() {
  if (!form.name.trim() || !form.query.trim()) {
    message.warning('名称与查询体必填');
    return;
  }
  saving.value = true;
  try {
    const body = {
      ...form,
      placeholders: placeholders.value.filter((p) => p.key.trim() !== ''),
    };
    await (editingId.value
      ? updateAlertTemplateApi(editingId.value, body)
      : createAlertTemplateApi(body));
    message.success('已保存');
    formOpen.value = false;
    await load();
  } finally {
    saving.value = false;
  }
}

async function remove(t: AlertTemplate) {
  try {
    await deleteAlertTemplateApi(t.id);
    message.success('已删除');
    await load();
  } catch {
    // 后端带引用数提示
  }
}

async function testRender() {
  rendering.value = true;
  try {
    // 未保存的新模板先提示
    if (!editingId.value) {
      message.warning('请先保存模板再测试渲染');
      return;
    }
    const r = await renderAlertTemplateApi(editingId.value, testParams.value);
    renderedSql.value = r.sql;
  } finally {
    rendering.value = false;
  }
}
</script>

<template>
  <div class="flex flex-col gap-3">
    <div class="flex justify-end">
      <a-button type="primary" @click="openCreate">新建模板</a-button>
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
        <template v-if="column.key === 'ph'">
          {{ record.placeholders?.length ?? 0 }} 个
        </template>
        <template v-else-if="column.key === 'action'">
          <a-space>
            <a-button size="small" type="link" @click="openEdit(record)">
              编辑
            </a-button>
            <a-popconfirm title="确认删除该模板？" @confirm="remove(record)">
              <a-button danger size="small" type="link">删除</a-button>
            </a-popconfirm>
          </a-space>
        </template>
      </template>
    </a-table>

    <a-drawer
      v-model:open="formOpen"
      :title="editingId ? '编辑告警模板' : '新建告警模板'"
      width="760"
    >
      <a-form layout="vertical" class="flex flex-col gap-1">
        <div class="grid grid-cols-2 gap-3">
          <a-form-item label="模板名称" required>
            <a-input
              v-model:value="form.name"
              placeholder="如：服务错误日志突增"
            />
          </a-form-item>
          <a-form-item label="描述">
            <a-input
              v-model:value="form.description"
              placeholder="项目侧选模板时可见的说明"
            />
          </a-form-item>
        </div>
        <div class="grid grid-cols-2 gap-3">
          <a-form-item label="分类">
            <a-input
              v-model:value="form.category"
              placeholder="如：日志 / 数据库 / 主机"
            />
          </a-form-item>
          <a-form-item label="查询类型">
            <a-select
              v-model:value="form.queryType"
              :options="[
                { label: 'SQL（日志流）', value: 'sql' },
                { label: 'PromQL（指标）', value: 'promql' },
              ]"
            />
          </a-form-item>
        </div>
        <a-form-item
          :extra="
            form.queryType === 'promql'
              ? 'PromQL 查询指标流（如 100 * (1 - avg by(instance) (rate(node_cpu_seconds_total[2m]))))'
              : 'SQL 查询日志流，占位符用 {{key}} 引用'
          "
          label="查询体"
          required
        >
          <YamlEditor
            v-model="form.query"
            height="180px"
            :language="form.queryType === 'promql' ? 'plaintext' : 'sql'"
          />
        </a-form-item>

        <a-divider class="my-2" orientation="left" plain>占位符定义</a-divider>
        <div
          v-for="(p, i) in placeholders"
          :key="i"
          class="grid grid-cols-12 items-center gap-2"
        >
          <a-input v-model:value="p.key" class="col-span-2" placeholder="key" />
          <a-input
            v-model:value="p.label"
            class="col-span-3"
            placeholder="显示名"
          />
          <a-select
            v-model:value="p.type"
            :options="[
              { label: '字符串', value: 'string' },
              { label: '数字', value: 'number' },
            ]"
            class="col-span-2"
          />
          <a-input
            v-model:value="p.default"
            class="col-span-2"
            placeholder="默认值"
          />
          <a-checkbox v-model:checked="p.required" class="col-span-2">
            必填
          </a-checkbox>
          <a-button
            danger
            size="small"
            type="link"
            @click="placeholders.splice(i, 1)"
          >
            删
          </a-button>
        </div>
        <a-button block class="mt-1" @click="addPlaceholder">
          + 添加占位符
        </a-button>

        <a-divider class="my-2" orientation="left" plain>
          默认触发参数（实例化可覆盖）
        </a-divider>
        <div class="grid grid-cols-5 gap-3">
          <a-form-item label="窗口(分)">
            <a-input-number
              v-model:value="form.period"
              class="w-full"
              :min="1"
              :max="1440"
            />
          </a-form-item>
          <a-form-item label="比较符">
            <a-select
              v-model:value="form.operator"
              :options="
                ['>=', '<=', '>', '<', '=', '!='].map((v) => ({
                  label: v,
                  value: v,
                }))
              "
            />
          </a-form-item>
          <a-form-item label="阈值">
            <a-input-number
              v-model:value="form.threshold"
              class="w-full"
              :min="0"
            />
          </a-form-item>
          <a-form-item label="频率(分)">
            <a-input-number
              v-model:value="form.frequency"
              class="w-full"
              :min="1"
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

        <a-divider v-if="editingId" class="my-2" orientation="left" plain>
          渲染测试
        </a-divider>
        <template v-if="editingId">
          <div class="grid grid-cols-3 gap-2">
            <a-input
              v-for="p in placeholders"
              :key="p.key"
              v-model:value="testParams[p.key]"
              :placeholder="`${p.label || p.key}${p.required ? ' *' : ''}`"
            />
          </div>
          <a-button
            class="mt-2"
            :loading="rendering"
            size="small"
            @click="testRender"
          >
            测试渲染
          </a-button>
          <YamlEditor
            v-if="renderedSql"
            :model-value="renderedSql"
            class="mt-2"
            height="120px"
            language="sql"
            read-only
          />
        </template>
      </a-form>
      <template #footer>
        <a-space>
          <a-button @click="formOpen = false">取消</a-button>
          <a-button :loading="saving" type="primary" @click="save">
            保存
          </a-button>
        </a-space>
      </template>
    </a-drawer>
  </div>
</template>
