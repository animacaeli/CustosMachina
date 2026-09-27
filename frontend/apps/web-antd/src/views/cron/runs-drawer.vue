<script lang="ts" setup>
import type { CronRun } from '#/api/cron';

import { ref, watch } from 'vue';

import { getRunsApi } from '#/api/cron';

defineOptions({ name: 'CronRunsDrawer' });

const props = defineProps<{
  jobId?: number;
  jobName?: string;
  open: boolean;
}>();

const emit = defineEmits<{ 'update:open': [value: boolean] }>();

const loading = ref(false);
const runs = ref<CronRun[]>([]);
const total = ref(0);
const page = ref(1);
const size = 20;

async function load() {
  loading.value = true;
  try {
    const res = await getRunsApi({
      jobId: props.jobId,
      page: page.value,
      size,
    });
    runs.value = res.items ?? [];
    total.value = res.total ?? 0;
  } finally {
    loading.value = false;
  }
}

watch(
  () => [props.open, props.jobId],
  ([open]) => {
    if (open) {
      page.value = 1;
      load();
    }
  },
);

const statusColor: Record<string, string> = {
  failed: 'red',
  running: 'processing',
  skipped: 'default',
  success: 'green',
  timeout: 'orange',
  unknown: 'purple',
};
const statusText: Record<string, string> = {
  failed: '失败',
  running: '运行中',
  skipped: '跳过',
  success: '成功',
  timeout: '超时',
  unknown: '结果未知',
};

const activeRun = ref<CronRun | null>(null);
const detailOpen = ref(false);

function showDetail(r: CronRun) {
  activeRun.value = r;
  detailOpen.value = true;
}

function fmtDuration(r: CronRun) {
  if (r.status === 'running') return '—';
  return `${r.durationSecs}s`;
}
</script>

<template>
  <a-drawer
    :open="open"
    :title="jobName ? `运行历史：${jobName}` : '运行历史（全部任务）'"
    :width="720"
    @close="emit('update:open', false)"
  >
    <a-table
      :data-source="runs"
      :loading="loading"
      :pagination="{
        current: page,
        pageSize: size,
        total,
        onChange: (p: number) => {
          page = p;
          load();
        },
        showTotal: (t: number) => `共 ${t} 条`,
        size: 'small',
      }"
      row-key="id"
      size="small"
    >
      <a-table-column title="ID" data-index="id" :width="60" />
      <a-table-column title="状态" :width="100">
        <template #default="{ record }">
          <a-badge
            :color="statusColor[record.status] ?? 'default'"
            :text="statusText[record.status] ?? record.status"
          />
        </template>
      </a-table-column>
      <a-table-column title="触发" :width="80">
        <template #default="{ record }">
          {{ record.trigger === 'manual' ? '手动' : '调度' }}
        </template>
      </a-table-column>
      <a-table-column title="开始时间" data-index="startedAt" :width="170">
        <template #default="{ text }">
          {{ new Date(text).toLocaleString() }}
        </template>
      </a-table-column>
      <a-table-column title="耗时" :width="70">
        <template #default="{ record }">{{ fmtDuration(record) }}</template>
      </a-table-column>
      <a-table-column title="输出" :ellipsis="true">
        <template #default="{ record }">
          <a @click="showDetail(record)">{{
            (record.output || '—').slice(0, 60)
          }}</a>
        </template>
      </a-table-column>
    </a-table>

    <a-modal
      v-model:open="detailOpen"
      :footer="null"
      :title="`运行 #${activeRun?.id} 输出`"
      :width="720"
    >
      <pre
        class="max-h-96 overflow-auto rounded bg-black/90 p-3 text-xs text-green-300"
        >{{ activeRun?.output || '（无输出）' }}</pre>
    </a-modal>
  </a-drawer>
</template>
