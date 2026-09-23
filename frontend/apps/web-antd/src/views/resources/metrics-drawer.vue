<script lang="ts" setup>
import type { ManagedServer, ServerEvent } from '#/api/resources/server';

import { ref, watch } from 'vue';

import { EchartsUI, useEcharts } from '@vben/plugins/echarts';

import {
  getServerEventsApi,
  getServerMetricsApi,
} from '#/api/resources/server';

const props = defineProps<{ server: ManagedServer | null }>();
const open = defineModel<boolean>('open', { default: false });

const RANGES = [
  { label: '1 小时', value: 1 },
  { label: '6 小时', value: 6 },
  { label: '24 小时', value: 24 },
  { label: '7 天', value: 168 },
];
const rangeHours = ref(1);
const loading = ref(false);
const events = ref<ServerEvent[]>([]);

const chartRef = ref<typeof EchartsUI>();
const { renderEcharts } = useEcharts(chartRef);

async function load() {
  const s = props.server;
  if (!s) return;
  loading.value = true;
  try {
    const [points, evs] = await Promise.all([
      getServerMetricsApi(s.id, rangeHours.value),
      getServerEventsApi(s.id),
    ]);
    events.value = evs;
    const ts = points.map((p) => new Date(p.ts).getTime());
    renderEcharts({
      animation: false,
      grid: { left: 40, right: 16, top: 32, bottom: 40 },
      legend: { data: ['CPU %', '内存 %'], top: 0 },
      series: [
        {
          name: 'CPU %',
          type: 'line',
          showSymbol: false,
          data: ts.map((t, i) => [t, round1(points[i]?.cpuPct ?? 0)]),
        },
        {
          name: '内存 %',
          type: 'line',
          showSymbol: false,
          areaStyle: { opacity: 0.08 },
          data: ts.map((t, i) => {
            const p = points[i];
            return [
              t,
              p && p.memTotal > 0 ? round1((p.memUsed / p.memTotal) * 100) : 0,
            ];
          }),
        },
      ],
      tooltip: { trigger: 'axis' },
      xAxis: { type: 'time' },
      yAxis: { max: 100, min: 0, type: 'value' },
    });
  } finally {
    loading.value = false;
  }
}

function round1(v: number) {
  return Math.round(v * 10) / 10;
}

watch(
  () => [open.value, props.server?.id, rangeHours.value] as const,
  ([isOpen]) => {
    if (isOpen && props.server) load();
  },
);

const EVENT_TAG: Record<string, { color: string; text: string }> = {
  recovered: { color: 'green', text: '恢复' },
  terminal_session: { color: 'geekblue', text: '终端会话' },
  unreachable: { color: 'red', text: '不可达' },
};
</script>

<template>
  <a-drawer
    v-model:open="open"
    :title="`资源曲线：${server?.name ?? ''}`"
    :width="720"
  >
    <a-spin :spinning="loading">
      <div class="flex items-center gap-2 pb-3">
        <a-radio-group
          v-model:value="rangeHours"
          button-style="solid"
          size="small"
        >
          <a-radio-button v-for="r in RANGES" :key="r.value" :value="r.value">
            {{ r.label }}
          </a-radio-button>
        </a-radio-group>
        <span class="text-xs text-gray-400">实线 CPU / 填充 内存</span>
      </div>
      <EchartsUI ref="chartRef" height="320px" />

      <div class="pt-4">
        <div class="pb-2 font-medium">状态事件（最近 50 条）</div>
        <a-timeline v-if="events.length > 0" class="pl-1">
          <a-timeline-item
            v-for="e in events"
            :key="e.id"
            :color="EVENT_TAG[e.type]?.color ?? 'blue'"
          >
            {{ new Date(e.createdAt).toLocaleString() }}
            <a-tag :color="EVENT_TAG[e.type]?.color ?? 'default'" class="ml-1">
              {{ EVENT_TAG[e.type]?.text ?? e.type }}
            </a-tag>
            <div class="text-xs text-gray-500">{{ e.message }}</div>
          </a-timeline-item>
        </a-timeline>
        <a-empty v-else description="暂无事件" />
      </div>
    </a-spin>
  </a-drawer>
</template>
