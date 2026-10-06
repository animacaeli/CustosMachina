<script lang="ts" setup>
import type { Build } from '#/api/ci';

import { computed, ref, watch } from 'vue';

import { getBuildLogApi, getBuildsApi } from '#/api/ci';
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

// ---- 内嵌流水线日志 ----
const logOpen = ref(false);
const logText = ref('');
const logTitle = ref('');
const logLoading = ref(false);
async function openLog(record: Build) {
  logTitle.value = `${record.tag} 流水线日志`;
  logOpen.value = true;
  logLoading.value = true;
  try {
    logText.value = (await getBuildLogApi(record.id)) || '（无日志）';
  } catch {
    logText.value = '日志拉取失败（构建可能尚未产生流水线记录）';
  } finally {
    logLoading.value = false;
  }
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
      <a-spin :spinning="logLoading">
        <pre
          class="max-h-[70vh] overflow-auto rounded p-3 text-xs leading-5"
          style="color: #c9d1d9; background: #0b0e14"
          >{{ logText }}</pre>
      </a-spin>
    </a-drawer>
  </a-drawer>
</template>
