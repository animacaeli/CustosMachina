<script lang="ts" setup>
import type { CronRun } from '#/api/cron';

import { onBeforeUnmount, ref, watch } from 'vue';

import { LoadingOutlined } from '@ant-design/icons-vue';
import { message } from 'ant-design-vue';

import { getRunApi, getRunsApi } from '#/api/cron';
import { fileDownloadUrl } from '#/api/resources/files';
import { usePagedListPolling } from '#/composables/use-paged-list-polling';

defineOptions({ name: 'CronRunsDrawer' });

const props = defineProps<{
  /** 快速执行后自动展开该运行的实时日志 */
  autoOpenRun?: number;
  jobId?: number;
  jobName?: string;
  /** 递增即强制刷新（抽屉已开且 jobId 不变时 watch 不触发） */
  nonce?: number;
  open: boolean;
}>();

const emit = defineEmits<{ 'update:open': [value: boolean] }>();

const size = 20;

// P8-M2：列表+条件轮询收敛到 usePagedListPolling（running 存在时 3s 静默刷新；
// 详情日志轮询另有 pollRunning 单条追）
const {
  items: runs,
  total,
  page,
  loading,
  load,
} = usePagedListPolling<CronRun>({
  size,
  interval: 3000,
  active: () => props.open,
  shouldPoll: (items) => items.some((r) => r.status === 'running'),
  fetch: (p, sz) => getRunsApi({ jobId: props.jobId, page: p, size: sz }),
});

// 打开抽屉 / 切换 jobId / 外部 nonce 递增（快速执行后强制刷新）→ 重置分页并
// 加载。P8-M2 重构回归修复：原 watch 只追 autoOpenRun，打开抽屉与切任务都不
// 触发 load，用户看到的恒是空表（v0.12.0 审计严重项）。
watch(
  () => [props.open, props.jobId, props.nonce] as const,
  async ([isOpen], old) => {
    if (!isOpen) return;
    if (old && (old[0] !== true || old[1] !== props.jobId)) {
      page.value = 1; // 打开或切任务回到第一页
    }
    load();
    // 快速执行链路：抽屉打开后自动展开指定运行的实时日志
    if (props.autoOpenRun) {
      try {
        const r = await getRunApi(props.autoOpenRun);
        showDetail(r);
      } catch {
        // 记录尚未可见时忽略
      }
    }
  },
  { immediate: true },
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
let pollTimer: ReturnType<typeof setInterval> | undefined;

function stopPoll() {
  if (pollTimer) {
    clearInterval(pollTimer);
    pollTimer = undefined;
  }
}

onBeforeUnmount(stopPoll);

// 运行中的记录每 2s 轮询单条（后端流式执行节流 1s 增量刷库，近实时看日志）
function pollRunning(id: number) {
  stopPoll();
  pollTimer = setInterval(async () => {
    if (!props.open) {
      stopPoll(); // 抽屉已关：详情轮询随停
      return;
    }
    try {
      const r = await getRunApi(id);
      activeRun.value = r;
      if (r.status !== 'running') stopPoll();
    } catch {
      stopPoll();
    }
  }, 2000);
}

const downloadingLog = ref(false);
async function downloadFullLog() {
  const r = activeRun.value;
  if (!r?.outputFile || !r.serverId || downloadingLog.value) return;
  downloadingLog.value = true;
  // await 后开窗已脱离用户手势会被拦截：先同步占位，拿到 URL 再跳转
  const win = window.open('', '_blank');
  try {
    const url = await fileDownloadUrl(r.serverId, r.outputFile);
    if (win) win.location.href = url;
    else message.warning('弹窗被浏览器拦截，请允许弹窗后重试');
  } catch {
    win?.close();
    // ticket 签发失败由拦截器提示
  } finally {
    downloadingLog.value = false;
  }
}

function showDetail(r: CronRun) {
  stopPoll();
  activeRun.value = r;
  detailOpen.value = true;
  if (r.status === 'running') pollRunning(r.id);
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
      <a-table-column title="状态" :width="110">
        <template #default="{ record }">
          <LoadingOutlined
            v-if="record.status === 'running'"
            class="mr-1"
            spin
          />
          <a-badge
            v-else
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
      <a-table-column title="日志" :width="80">
        <template #default="{ record }">
          <a-button size="small" type="link" @click="showDetail(record)">
            日志
          </a-button>
        </template>
      </a-table-column>
    </a-table>

    <a-modal
      v-model:open="detailOpen"
      :footer="null"
      :title="`运行 #${activeRun?.id} 输出`"
      :width="720"
      @cancel="stopPoll"
    >
      <div class="mb-1 flex items-center justify-between text-xs">
        <a-badge
          :status="activeRun?.status === 'running' ? 'processing' : undefined"
          :text="
            activeRun?.status === 'running'
              ? '执行中（实时输出，2 秒刷新）'
              : ''
          "
        />
        <a-button
          v-if="activeRun?.outputFile"
          size="small"
          type="link"
          @click="downloadFullLog"
        >
          下载全量日志
        </a-button>
      </div>
      <pre
        class="max-h-96 overflow-auto rounded bg-black/90 p-3 text-xs text-green-300"
        >{{ activeRun?.output || '（暂无输出）' }}</pre>
    </a-modal>
  </a-drawer>
</template>
