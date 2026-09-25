<script lang="ts" setup>
import type { Build } from '#/api/ci';

import { onBeforeUnmount, ref, watch } from 'vue';

import { getBuildsApi } from '#/api/ci';

defineOptions({ name: 'BuildDrawer' });

const props = defineProps<{
  env: 'canary' | 'prod' | 'test';
  open: boolean;
  projectId: number | undefined;
}>();

const emit = defineEmits<{ close: [] }>();

const list = ref<Build[]>([]);
const total = ref(0);
const page = ref(1);
const size = ref(10);
const loading = ref(false);

let timer: null | ReturnType<typeof setInterval> = null;

async function load() {
  if (!props.projectId) return;
  loading.value = true;
  try {
    const res = await getBuildsApi({
      env: props.env,
      page: page.value,
      projectId: props.projectId,
      size: size.value,
    });
    list.value = res.items ?? [];
    total.value = res.total ?? 0;
  } finally {
    loading.value = false;
  }
}

watch(
  () => [props.open, props.projectId, page.value],
  () => props.open && load(),
);

// 未终态的记录持续轮询（后端 jobs 每 30s 拉 gitea commit status）
watch(
  () => props.open,
  (open) => {
    if (open && !timer) {
      timer = setInterval(load, 30_000);
    } else if (!open && timer) {
      clearInterval(timer);
      timer = null;
    }
  },
  { immediate: true },
);

onBeforeUnmount(() => {
  if (timer) clearInterval(timer);
});

const envTitles: Record<string, string> = {
  canary: '灰度环境构建',
  prod: '正式环境构建',
  test: '测试环境构建',
};

const statusColors: Record<string, string> = {
  failed: 'red',
  pending: 'default',
  running: 'processing',
  success: 'green',
};
const statusLabels: Record<string, string> = {
  failed: '失败',
  pending: '排队中',
  running: '构建中',
  success: '成功',
};

function onPageChange(p: number, s: number) {
  page.value = p;
  size.value = s;
}

function fmtTime(v: string) {
  return v ? new Date(v).toLocaleString('zh-CN', { hour12: false }) : '-';
}
</script>

<template>
  <a-drawer
    :open="open"
    :title="envTitles[env] ?? '构建记录'"
    :width="860"
    @close="emit('close')"
  >
    <a-alert show-icon type="info" style="margin-bottom: 0.75rem">
      <template #message>
        标签推送到 gitea 后自动构建（正式 v*，灰度
        canary-yyyymmdd-缩写）；状态每 30 秒自动刷新。
      </template>
    </a-alert>
    <a-table
      :columns="[
        { title: '标签', dataIndex: 'tag' },
        { title: '构建人', dataIndex: 'builder', width: 100 },
        { title: '时间', key: 'time', width: 170 },
        { title: '状态', key: 'status', width: 90 },
        { title: '操作', key: 'action', width: 90 },
      ]"
      :data-source="list"
      :loading="loading"
      :pagination="{
        current: page,
        pageSize: size,
        total,
        showSizeChanger: false,
        onChange: onPageChange,
      }"
      row-key="id"
      size="middle"
    >
      <template #bodyCell="{ column, record }">
        <template v-if="column.key === 'time'">
          {{ fmtTime(record.createdAt) }}
        </template>
        <template v-else-if="column.key === 'status'">
          <a-badge
            :color="statusColors[record.status]"
            :status="record.status === 'pending' ? 'default' : undefined"
            :text="statusLabels[record.status] ?? record.status"
          />
        </template>
        <template v-else-if="column.key === 'action'">
          <a-button
            :disabled="!record.logUrl"
            :href="record.logUrl"
            size="small"
            target="_blank"
            type="link"
          >
            日志
          </a-button>
        </template>
      </template>
    </a-table>
  </a-drawer>
</template>
