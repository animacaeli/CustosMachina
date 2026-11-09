<script lang="ts" setup>
import type { Build } from '#/api/ci';

import { computed, ref, watch } from 'vue';

import { getBuildLogStreamApi, getBuildsApi } from '#/api/ci';
import { usePagedListPolling } from '#/composables/use-paged-list-polling';

defineOptions({ name: 'BuildDrawer' });

const props = defineProps<{
  env: 'canary' | 'prod' | 'test';
  open: boolean;
  projectId: number | undefined;
}>();

const emit = defineEmits<{ close: [] }>();

// P8-M2：分页+轮询收敛到 usePagedListPolling（构建抽屉为 30s 常驻轮询——
// 后端 jobs 每 30s 拉 commit status，未终态记录靠它转绿）
const size = ref(10);
const {
  items: list,
  total,
  page,
  loading,
  load,
} = usePagedListPolling<Build>({
  size: 10,
  interval: 30_000,
  active: () => props.open,
  shouldPoll: () => true,
  fetch: (p, sz) =>
    props.projectId
      ? getBuildsApi({
          env: props.env,
          page: p,
          projectId: props.projectId,
          size: sz,
        })
      : Promise.resolve({}),
});

watch(
  () => props.projectId,
  () => {
    page.value = 1; // 换项目回到第一页，避免停在新项目不存在的页码
  },
);

watch(
  () => [props.open, props.projectId, page.value],
  () => {
    if (props.open) load().catch((error) => console.warn('[load]', error));
  },
);

// test 环境的 tag 形如 "分支@dev1"——拆成 标签/槽位 两列
function splitTag(tag: string): { branch: string; slot: string } {
  const at = tag.lastIndexOf('@');
  if (at === -1) return { branch: tag, slot: '-' };
  return { branch: tag, slot: tag.slice(at + 1) };
}

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

function fmtDuration(secs: number, status: string) {
  if (secs > 0) {
    return secs >= 60 ? `${Math.floor(secs / 60)}m${secs % 60}s` : `${secs}s`;
  }
  return status === 'running' ? '进行中' : '-';
}

const columns = computed(() => {
  const base = [
    { title: '来源', key: 'provider', width: 80 },
    { title: '构建人', dataIndex: 'builder', width: 100 },
    { title: '时间', key: 'time', width: 170 },
    { title: '耗时', key: 'duration', width: 90 },
    { title: '状态', key: 'status', width: 90 },
    { title: '操作', key: 'action', width: 90 },
  ];
  if (props.env === 'test') {
    // test 的 tag 形如 "分支@dev1"——拆成 标签/槽位 两列
    return [
      { title: '标签', key: 'tagcol' },
      { title: '槽位', key: 'slot', width: 80 },
      ...base,
    ];
  }
  return [{ title: '标签', dataIndex: 'tag' }, ...base];
});

// ---- 内嵌日志：增量轮询实时滚动 ----
// 构建中（pending/running）每 2s 拉增量（Jenkins progressiveText / 槽位部署输出），
// 终态一次拉全（more=false）自动停；关窗或换行后旧轮询按会话令牌自止。
// tag 记录（prod/canary）= CI 构建日志；槽位记录（test）有 ciRef（回调带
// BUILD_NUMBER）时双视图：CI 构建（Jenkins console）/ 部署输出（平台 compose），
// 存量无 ciRef 只有部署输出。
const logOpen = ref(false);
const logText = ref('');
const logTitle = ref('');
const logLoading = ref(false);
const logStreaming = ref(false); // 增量轮询进行中（构建未结束）
const logDual = ref(false); // test + ciRef：显示 CI 构建/部署输出切换
const logKind = ref<'ci' | 'deploy'>('ci');
let logRecord: Build | null = null;
let logToken = 0;
const logPreRef = ref<HTMLElement | null>(null);
// 增量追加后滚到底部（实时跟进）
watch(logText, () => {
  const el = logPreRef.value;
  if (el) el.scrollTop = el.scrollHeight;
});
const LOG_POLL_MS = 2000;

function sleep(ms: number) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function logKindLabel(): string {
  // test 记录区分双视图；prod/canary 恒为 CI 构建日志
  if (logKind.value === 'deploy') return '部署日志';
  return 'CI 构建日志';
}

async function openLog(record: Build, kind?: 'ci' | 'deploy') {
  logRecord = record;
  const live = record.status === 'pending' || record.status === 'running';
  logDual.value = record.envType === 'test' && !!record.ciRef;
  logKind.value = kind ?? (logDual.value ? 'ci' : 'deploy');
  logTitle.value = `${record.tag} ${logKindLabel()}${live ? '（实时）' : ''}`;
  logOpen.value = true;
  logText.value = '';
  logLoading.value = true;
  const token = ++logToken;
  let start = 0;
  try {
    for (;;) {
      const chunk = await getBuildLogStreamApi(
        record.id,
        start,
        logKind.value === 'deploy' ? 'deploy' : undefined,
      );
      if (token !== logToken) return; // 已关窗/查看其他记录/切换视图
      if (chunk.text) logText.value += chunk.text;
      start = chunk.next;
      if (!chunk.more) break;
      logLoading.value = false; // 首段到达即展示，后续增量滚动
      logStreaming.value = true;
      await sleep(LOG_POLL_MS);
    }
    if (!logText.value) logText.value = '（无日志）';
  } catch {
    if (!logText.value) logText.value = '日志拉取失败（构建可能尚未产生日志）';
  } finally {
    if (token === logToken) {
      logLoading.value = false;
      logStreaming.value = false;
    }
  }
}

function onLogKindChange() {
  // 视图切换：重开会话（token 自增止掉旧轮询，从游标 0 重拉）
  if (logRecord) openLog(logRecord, logKind.value);
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
      :columns="columns"
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
        <template v-if="column.key === 'tagcol'">
          {{ splitTag(record.tag).branch }}
        </template>
        <template v-else-if="column.key === 'slot'">
          {{ splitTag(record.tag).slot }}
        </template>
        <template v-else-if="column.key === 'provider'">
          <a-tag v-if="record.provider === 'gitee'" color="blue">gitee</a-tag>
          <a-tag v-else color="cyan">gitea</a-tag>
        </template>
        <template v-else-if="column.key === 'time'">
          {{ fmtTime(record.createdAt) }}
        </template>
        <template v-else-if="column.key === 'duration'">
          {{ fmtDuration(record.durationSecs, record.status) }}
        </template>
        <template v-else-if="column.key === 'status'">
          <a-badge
            :color="statusColors[record.status]"
            :status="record.status === 'pending' ? 'default' : undefined"
            :text="statusLabels[record.status] ?? record.status"
          />
        </template>
        <template v-else-if="column.key === 'action'">
          <a-button size="small" type="link" @click="openLog(record)">
            日志
          </a-button>
        </template>
      </template>
    </a-table>

    <a-drawer
      :open="logOpen"
      :title="logTitle"
      :width="820"
      @close="logOpen = false"
    >
      <a-radio-group
        v-if="logDual"
        v-model:value="logKind"
        button-style="solid"
        size="small"
        style="margin-bottom: 0.5rem"
        @change="onLogKindChange"
      >
        <a-radio-button value="ci">CI 构建</a-radio-button>
        <a-radio-button value="deploy">部署输出</a-radio-button>
      </a-radio-group>
      <a-spin :spinning="logLoading">
        <div v-if="logStreaming" class="mb-1 text-xs text-blue-400">
          ⏳ 实时获取中（每 2 秒增量）…
        </div>
        <pre
          ref="logPreRef"
          class="max-h-[70vh] overflow-auto rounded p-3 text-xs leading-5"
          style="color: #c9d1d9; background: #0b0e14"
          >{{ logText }}</pre>
      </a-spin>
    </a-drawer>
  </a-drawer>
</template>
