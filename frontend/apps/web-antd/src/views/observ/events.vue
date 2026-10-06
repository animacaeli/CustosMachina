<script lang="ts" setup>
import type { AlertEvent, SendRecord } from '#/api/observ/alerts';

import { computed, onBeforeUnmount, ref, watch } from 'vue';

import { useUserStore } from '@vben/stores';

import { message } from 'ant-design-vue';

import {
  getAlertEventsApi,
  getNotifyRecordsApi,
  handleAlertEventApi,
} from '#/api/observ/alerts';

defineOptions({ name: 'ObservEvents' });

const userStore = useUserStore();
const isAdmin = computed(() => {
  const roles = userStore.userInfo?.roles ?? [];
  return roles.includes('superadmin') || roles.includes('admin');
});

const tab = ref<'events' | 'records'>('events');
const size = 20;

// ---- 告警事件 ----
const events = ref<AlertEvent[]>([]);
const eventsTotal = ref(0);
const eventsPage = ref(1);
const eventsLoading = ref(false);
const levelFilter = ref<string>('');
const statusFilter = ref<string>('open');
const detailEvent = ref<AlertEvent | null>(null);

async function loadEvents() {
  eventsLoading.value = true;
  try {
    const res = await getAlertEventsApi({
      level: levelFilter.value || undefined,
      page: eventsPage.value,
      size,
      status: statusFilter.value || undefined,
    });
    events.value = res.items ?? [];
    eventsTotal.value = res.total ?? 0;
  } catch {
    // 拦截器已提示
  } finally {
    eventsLoading.value = false;
  }
}

async function onHandle(e: AlertEvent) {
  try {
    await handleAlertEventApi(e.id);
    message.success('已标记处理');
    await loadEvents();
  } catch {
    // 拦截器已提示
  }
}

// ---- 通知投递记录（admin）----
const records = ref<SendRecord[]>([]);
const recordsTotal = ref(0);
const recordsPage = ref(1);
const recordsLoading = ref(false);

async function loadRecords() {
  recordsLoading.value = true;
  try {
    const res = await getNotifyRecordsApi({
      page: recordsPage.value,
      size,
    });
    records.value = res.items ?? [];
    recordsTotal.value = res.total ?? 0;
  } catch {
    // 拦截器已提示
  } finally {
    recordsLoading.value = false;
  }
}

// 打开页签即加载；30s 静默轮询（未处理告警是活跃关注面）
let timer: ReturnType<typeof setInterval> | undefined;
function schedulePoll() {
  clearInterval(timer);
  timer = setInterval(() => {
    if (!document.hidden) {
      if (tab.value === 'events') loadEvents();
      else if (isAdmin.value) loadRecords();
    }
  }, 30_000);
}
onBeforeUnmount(() => clearInterval(timer));

watch(
  [tab, levelFilter, statusFilter],
  () => {
    if (tab.value === 'events') {
      eventsPage.value = 1;
      loadEvents();
    } else if (isAdmin.value) {
      recordsPage.value = 1;
      loadRecords();
    }
  },
  { immediate: true },
);
schedulePoll();

const levelText: Record<string, string> = {
  critical: '严重',
  info: '信息',
  warn: '警告',
};
const levelColor: Record<string, string> = {
  critical: 'red',
  info: 'blue',
  warn: 'orange',
};
</script>

<template>
  <div class="p-4">
    <a-card>
      <template #title>
        <span class="text-base">告警历史</span>
        <span class="text-muted-foreground ml-2 text-xs font-normal">
          事件留痕与处理状态；投递记录用于排查「为什么没收到通知」
        </span>
      </template>
      <a-tabs v-model:active-key="tab">
        <a-tab-pane key="events" tab="告警事件">
          <div class="mb-3 flex gap-2">
            <a-select
              v-model:value="levelFilter"
              :allow-clear="true"
              placeholder="级别"
              style="width: 120px"
              :options="[
                { label: '严重', value: 'critical' },
                { label: '警告', value: 'warn' },
                { label: '信息', value: 'info' },
              ]"
            />
            <a-select
              v-model:value="statusFilter"
              :allow-clear="true"
              placeholder="状态"
              style="width: 120px"
              :options="[
                { label: '未处理', value: 'open' },
                { label: '已处理', value: 'handled' },
              ]"
            />
            <a-button @click="loadEvents">刷新</a-button>
          </div>
          <a-table
            :columns="[
              { title: '级别', key: 'level', width: 90 },
              { title: '标题', key: 'title' },
              { title: '时间', key: 'createdAt', width: 170 },
              { title: '状态', key: 'status', width: 90 },
              { title: '操作', key: 'actions', width: 130 },
            ]"
            :data-source="events"
            :loading="eventsLoading"
            :pagination="{
              current: eventsPage,
              pageSize: size,
              total: eventsTotal,
              onChange: (p: number) => {
                eventsPage = p;
                loadEvents();
              },
              showTotal: (t: number) => `共 ${t} 条`,
              size: 'small',
            }"
            row-key="id"
            size="small"
          >
            <template #bodyCell="{ column, record }">
              <template v-if="column.key === 'level'">
                <a-tag :color="levelColor[record.level] ?? 'default'">
                  {{ levelText[record.level] ?? record.level }}
                </a-tag>
              </template>
              <template v-else-if="column.key === 'title'">
                <a @click="detailEvent = record">{{ record.title }}</a>
                <div class="text-muted-foreground text-xs">
                  {{ record.source }}
                </div>
              </template>
              <template v-else-if="column.key === 'createdAt'">
                <span class="text-xs">
                  {{ new Date(record.createdAt).toLocaleString() }}
                </span>
              </template>
              <template v-else-if="column.key === 'status'">
                <a-badge
                  :status="record.status === 'open' ? 'error' : 'success'"
                  :text="record.status === 'open' ? '未处理' : '已处理'"
                />
              </template>
              <template v-else-if="column.key === 'actions'">
                <a-button
                  v-if="record.status === 'open'"
                  size="small"
                  type="link"
                  @click="onHandle(record)"
                >
                  标记处理
                </a-button>
                <span
                  v-if="record.status === 'handled'"
                  class="text-muted-foreground text-xs"
                >
                  {{ record.handledBy }}
                </span>
              </template>
            </template>
          </a-table>
        </a-tab-pane>

        <a-tab-pane v-if="isAdmin" key="records" tab="通知投递记录">
          <div class="mb-3">
            <a-button @click="loadRecords">刷新</a-button>
          </div>
          <a-table
            :columns="[
              { title: '时间', key: 'createdAt', width: 170 },
              { title: '群', key: 'groupId', width: 80 },
              { title: '标题', key: 'title' },
              { title: '结果', key: 'status', width: 90 },
              { title: '错误', key: 'error' },
            ]"
            :data-source="records"
            :loading="recordsLoading"
            :pagination="{
              current: recordsPage,
              pageSize: size,
              total: recordsTotal,
              onChange: (p: number) => {
                recordsPage = p;
                loadRecords();
              },
              showTotal: (t: number) => `共 ${t} 条`,
              size: 'small',
            }"
            row-key="id"
            size="small"
          >
            <template #bodyCell="{ column, record }">
              <template v-if="column.key === 'createdAt'">
                <span class="text-xs">
                  {{ new Date(record.createdAt).toLocaleString() }}
                </span>
              </template>
              <template v-else-if="column.key === 'status'">
                <a-badge
                  :status="record.status === 'ok' ? 'success' : 'error'"
                  :text="record.status === 'ok' ? '成功' : '失败'"
                />
              </template>
              <template v-else-if="column.key === 'error'">
                <span class="text-xs">{{ record.error || '—' }}</span>
              </template>
            </template>
          </a-table>
        </a-tab-pane>
      </a-tabs>
    </a-card>

    <a-modal
      :footer="null"
      :open="!!detailEvent"
      :title="detailEvent?.title"
      :width="640"
      @cancel="detailEvent = null"
    >
      <pre
        class="max-h-96 overflow-auto rounded bg-black/90 p-3 text-xs text-green-300"
        >{{ detailEvent?.detail || '（无详情）' }}</pre>
    </a-modal>
  </div>
</template>
