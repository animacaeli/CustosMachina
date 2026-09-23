<script lang="ts" setup>
import { computed } from 'vue';

/**
 * 单指标迷你面积图（对标阿里云监控缩略图）：渐变填充折线 + 当前值。
 * 纯 SVG 手绘——表格每行两个实例，起 ECharts 太重。
 */
const props = defineProps<{
  color?: string;
  points: number[];
}>();

const W = 110;
const H = 30;

const color = computed(() => props.color ?? '#1890ff');

const path = computed(() => {
  const pts = props.points.slice(-40);
  if (pts.length < 2) return { line: '', area: '' };
  const min = 0;
  const max = Math.max(100, ...pts); // 百分比指标固定 0~100 基准
  const step = W / (pts.length - 1);
  const y = (v: number) =>
    (H - 2 - ((Math.min(v, max) - min) / (max - min || 1)) * (H - 6)).toFixed(
      1,
    );
  let line = '';
  for (let i = 0; i < pts.length; i++) {
    line += `${i === 0 ? 'M' : 'L'}${(i * step).toFixed(1)},${y(pts[i] ?? 0)}`;
  }
  const area = `${line}L${W},${H}L0,${H}Z`;
  return { line, area };
});

const current = computed(() => {
  const pts = props.points;
  return pts.length > 0 ? pts[pts.length - 1] : undefined;
});

const gid = computed(() => {
  let hash = 7;
  for (const ch of color.value) {
    hash = Math.trunc(hash * 31 + (ch.codePointAt(0) ?? 0));
  }
  return `sg-${Math.abs(hash)}`;
});
</script>

<template>
  <div class="flex items-center gap-1">
    <svg :height="H" :width="W" class="overflow-visible" viewBox="0 0 110 30">
      <defs>
        <linearGradient :id="gid" x1="0" x2="0" y1="0" y2="1">
          <stop :stop-color="color" offset="0%" stop-opacity="0.35" />
          <stop :stop-color="color" offset="100%" stop-opacity="0.02" />
        </linearGradient>
      </defs>
      <path :d="path.area" :fill="`url(#${gid})`" stroke="none" />
      <path
        :d="path.line"
        fill="none"
        :stroke="color"
        stroke-width="1.5"
        stroke-linejoin="round"
      />
    </svg>
    <span
      v-if="current !== undefined"
      class="w-11 shrink-0 text-right text-xs tabular-nums"
      :style="{ color }"
    >
      {{ current.toFixed(1) }}%
    </span>
  </div>
</template>
