<script lang="ts" setup>
import type { CanaryPolicy } from '#/api/canary';
import type { Project } from '#/api/projects';

import { computed, reactive, ref, watch } from 'vue';

import { message } from 'ant-design-vue';

import {
  createCanaryPolicyApi,
  deleteCanaryPolicyApi,
  getCanaryPoliciesApi,
  publishCanaryApi,
  updateCanaryPolicyApi,
} from '#/api/canary';
import { getPassedTagsApi } from '#/api/release';

defineOptions({ name: 'PolicyDrawer' });

const props = defineProps<{
  open: boolean;
  project: Project | undefined;
}>();

const emit = defineEmits<{ close: [] }>();

const list = ref<CanaryPolicy[]>([]);
const publishedVersion = ref(0);
const loading = ref(false);
const canaryTags = ref<string[]>([]);
const publishing = ref(false);

async function load() {
  if (!props.project) return;
  loading.value = true;
  try {
    const res = await getCanaryPoliciesApi(props.project.id);
    list.value = res.policies ?? [];
    publishedVersion.value = res.publishedVersion ?? 0;
    try {
      canaryTags.value = await getPassedTagsApi(props.project.id, 'canary');
    } catch {
      canaryTags.value = [];
    }
  } finally {
    loading.value = false;
  }
}

watch(
  () => [props.open, props.project?.id],
  () => props.open && load(),
);

// 流量总和 vs 项目上限（前端实时校验，后端兜底）
const trafficSum = computed(() =>
  list.value
    .filter((p) => p.type === 'traffic' && p.enabled)
    .reduce((acc, p) => acc + p.trafficPercent, 0),
);
const cap = computed(() => props.project?.trafficCap ?? 50);
const sumExceeded = computed(() => trafficSum.value > cap.value);

const formOpen = ref(false);
const editingId = ref<null | number>(null);
const form = reactive({
  boundTag: undefined as string | undefined,
  enabled: true,
  headerKey: 'x-canary',
  headerValue: '',
  trafficPercent: 10,
  type: 'header' as 'header' | 'traffic',
});

function openCreate() {
  editingId.value = null;
  Object.assign(form, {
    boundTag: canaryTags.value[0],
    enabled: true,
    headerKey: 'x-canary',
    headerValue: '',
    trafficPercent: 10,
    type: 'header',
  });
  formOpen.value = true;
}

function openEdit(p: CanaryPolicy) {
  editingId.value = p.id;
  Object.assign(form, {
    boundTag: p.boundTag,
    enabled: p.enabled,
    headerKey: p.headerKey || 'x-canary',
    headerValue: p.headerValue,
    trafficPercent: p.trafficPercent || 10,
    type: p.type,
  });
  formOpen.value = true;
}

async function submitForm() {
  if (!props.project || !form.boundTag) {
    message.warning('请选择绑定的灰度标签');
    return;
  }
  if (form.type === 'header' && !form.headerValue) {
    message.warning('请填写请求头的值');
    return;
  }
  const data = {
    boundTag: form.boundTag,
    enabled: form.enabled,
    headerKey: form.type === 'header' ? form.headerKey : '',
    headerValue: form.type === 'header' ? form.headerValue : '',
    trafficPercent: form.type === 'traffic' ? form.trafficPercent : 0,
    type: form.type,
  };
  try {
    if (editingId.value) {
      await updateCanaryPolicyApi(props.project.id, editingId.value, data);
      message.success('已更新（策略回到未发布状态）');
    } else {
      await createCanaryPolicyApi(props.project.id, data);
      message.success('已新增（未发布）');
    }
    formOpen.value = false;
    await load();
  } catch {
    // 拦截器提示（含流量超限等）
  }
}

async function onDelete(p: CanaryPolicy) {
  if (!props.project) return;
  await deleteCanaryPolicyApi(props.project.id, p.id);
  message.success('已删除（重新发布后生效）');
  await load();
}

async function doPublish() {
  if (!props.project) return;
  if (sumExceeded.value) {
    message.warning(`流量总和 ${trafficSum.value}% 超过上限 ${cap.value}%`);
    return;
  }
  publishing.value = true;
  try {
    const res = await publishCanaryApi(props.project.id);
    message.success(`已发布策略 v${res.version}（nginx 已 reload）`);
    await load();
  } catch {
    // 拦截器提示
  } finally {
    publishing.value = false;
  }
}
</script>

<template>
  <a-drawer :open="open" title="灰度策略" :width="920" @close="emit('close')">
    <a-alert class="mb-3" show-icon type="info">
      <template #message>
        请求头命中优先进灰度；未命中按流量比例（当前启用总和
        <a-typography-text :type="sumExceeded ? 'danger' : 'success'">
          {{ trafficSum }}% / 上限 {{ cap }}%
        </a-typography-text>
        ）。策略增删改不立即生效，需「发布」整体替换（当前生效版本
        {{ publishedVersion === 0 ? '无' : `v${publishedVersion}` }}）。
      </template>
    </a-alert>

    <div class="mb-4 flex gap-2">
      <a-button type="primary" @click="openCreate">新增策略</a-button>
      <a-popconfirm
        title="发布 = 把当前启用策略整体生效并 reload nginx，确认？"
        @confirm="doPublish"
      >
        <a-button :loading="publishing" danger type="primary">发布</a-button>
      </a-popconfirm>
    </div>

    <a-table
      :columns="[
        { title: '类型', key: 'type', width: 100 },
        { title: '匹配 / 比例', key: 'match' },
        { title: '绑定灰度标签', dataIndex: 'boundTag' },
        { title: '状态', key: 'status', width: 90 },
        { title: '操作', key: 'action', width: 130 },
      ]"
      :data-source="list"
      :loading="loading"
      :pagination="false"
      row-key="id"
      size="middle"
    >
      <template #bodyCell="{ column, record }">
        <template v-if="column.key === 'type'">
          <a-tag :color="record.type === 'header' ? 'blue' : 'purple'">
            {{ record.type === 'header' ? '请求头' : '流量' }}
          </a-tag>
        </template>
        <template v-else-if="column.key === 'match'">
          <code v-if="record.type === 'header'">
            {{ record.headerKey }}: {{ record.headerValue }}
          </code>
          <span v-else>{{ record.trafficPercent }}%</span>
        </template>
        <template v-else-if="column.key === 'status'">
          <a-tag :color="record.publishedVersion > 0 ? 'green' : 'orange'">
            {{
              record.publishedVersion > 0
                ? `已发布 v${record.publishedVersion}`
                : '未发布'
            }}
          </a-tag>
        </template>
        <template v-else-if="column.key === 'action'">
          <a-button size="small" type="link" @click="openEdit(record)">
编辑
</a-button>
          <a-popconfirm
            title="删除该策略？重新发布后生效。"
            @confirm="onDelete(record)"
          >
            <a-button danger size="small" type="link">删除</a-button>
          </a-popconfirm>
        </template>
      </template>
    </a-table>

    <a-modal
      v-model:open="formOpen"
      :title="editingId ? '编辑策略' : '新增策略'"
      @ok="submitForm"
    >
      <a-form layout="vertical" class="pt-2">
        <a-form-item label="灰度策略类型" required>
          <a-radio-group v-model:value="form.type" button-style="solid">
            <a-radio-button value="header">指定请求头</a-radio-button>
            <a-radio-button value="traffic">流量比例</a-radio-button>
          </a-radio-group>
        </a-form-item>
        <template v-if="form.type === 'header'">
          <a-form-item label="请求头键" required>
            <a-input v-model:value="form.headerKey" placeholder="x-canary" />
          </a-form-item>
          <a-form-item
            label="请求头的值"
            required
            extra="命中该值的请求进灰度实例"
          >
            <a-input
              v-model:value="form.headerValue"
              placeholder="如姓名缩写"
            />
          </a-form-item>
        </template>
        <a-form-item
          v-else
          label="流量比例（%）"
          required
          :extra="`多条策略按总和计算，上限 ${cap}%`"
        >
          <a-input-number
            v-model:value="form.trafficPercent"
            :max="100"
            :min="1"
          />
        </a-form-item>
        <a-form-item
          label="绑定灰度标签"
          required
          extra="只能选本项目已通过 CI 的 canary 标签；全部策略须绑定同一标签"
        >
          <a-select
            v-model:value="form.boundTag"
            :options="canaryTags.map((t) => ({ label: t, value: t }))"
            :placeholder="
              canaryTags.length === 0 ? '暂无已通过 CI 的灰度标签' : '选择标签'
            "
            show-search
          />
        </a-form-item>
        <a-form-item>
          <a-checkbox v-model:checked="form.enabled">启用</a-checkbox>
        </a-form-item>
      </a-form>
    </a-modal>
  </a-drawer>
</template>
