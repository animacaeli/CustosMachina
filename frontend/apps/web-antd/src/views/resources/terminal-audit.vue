<script lang="ts" setup>
import type { TerminalSession } from '#/api/resources/terminal-audit';

import { onBeforeUnmount, ref, watch } from 'vue';

import { message } from 'ant-design-vue';

import {
  getTerminalCastApi,
  listTerminalSessionsApi,
} from '#/api/resources/terminal-audit';

/**
 * 终端会话审计（P6-M6）：录制列表 + 网页回放（asciinema-player 自托管，
 * cast 经 Blob URL 加载——无外部 CDN 依赖）。旧 .log 格式由后端即时转单帧。
 */

defineOptions({ name: 'TerminalAudit' });

const props = defineProps<{
  open: boolean;
  serverId?: number;
}>();

const emit = defineEmits<{
  (e: 'update:open', v: boolean): void;
}>();

const sessions = ref<TerminalSession[]>([]);
const loading = ref(false);
const playing = ref<null | TerminalSession>(null);
const playerBox = ref<HTMLElement>();
// asciinema-player 按需动态加载（首播才拉包）
let playerInstance: null | { dispose?: () => void } = null;

async function load() {
  loading.value = true;
  try {
    sessions.value = await listTerminalSessionsApi(props.serverId);
  } finally {
    loading.value = false;
  }
}

async function play(s: TerminalSession) {
  playing.value = s;
  try {
    const cast = await getTerminalCastApi(s.serverId, s.file);
    const mod = await import('asciinema-player');
    await new Promise((r) => setTimeout(r, 50)); // 等 playerBox 渲染
    if (!playerBox.value) return;
    playerInstance?.dispose?.();
    const blob = new Blob([cast], { type: 'text/plain' });
    const url = URL.createObjectURL(blob);
    // 库类型重载与当前 DOM 包装不兼容（as any 收口：参数形状按官方文档）
    const createFn = mod.create as unknown as (
      el: HTMLElement,
      source: Record<string, unknown>,
      opts: Record<string, unknown>,
    ) => { dispose?: () => void };
    playerInstance = createFn(playerBox.value!, { data: url }, {
      cols: 120,
      rows: 32,
      autoPlay: true,
      idleTimeLimit: 2,
    });
  } catch (e: any) {
    message.error(e?.response?.data?.message ?? '加载录制失败');
  }
}

function stopPlay() {
  playerInstance?.dispose?.();
  playerInstance = null;
  playing.value = null;
}

function fmtSize(n: number) {
  return n > 1024 * 1024
    ? `${(n / 1024 / 1024).toFixed(1)} MB`
    : `${Math.max(1, Math.round(n / 1024))} KB`;
}

watch(
  () => props.open,
  (v) => {
    if (v) {
      stopPlay();
      load();
    }
  },
);

onBeforeUnmount(stopPlay);
</script>

<template>
  <a-drawer
    :open="props.open"
    :title="playing ? `会话回放 · ${playing.operator}` : '终端会话审计'"
    :width="860"
    @close="emit('update:open', false)"
  >
    <div v-if="playing" class="flex h-full flex-col gap-3">
      <div class="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
        <a-button size="small" @click="stopPlay">← 返回列表</a-button>
        <span>{{ playing.server }}（#{{ playing.serverId }}）</span>
        <span>{{ playing.operator }}</span>
        <span>{{ playing.start.replace('T', ' ').slice(0, 19) }}</span>
        <a-tag v-if="playing.legacy" color="orange">旧格式：无时序单帧</a-tag>
      </div>
      <div
        ref="playerBox"
        class="min-h-0 flex-1 overflow-auto rounded-md bg-black/95 p-2"
      ></div>
    </div>
    <a-table
      v-else
      :data-source="sessions"
      :loading="loading"
      :pagination="{ pageSize: 15 }"
      row-key="file"
      size="small"
    >
      <a-table-column title="主机" data-index="server" :width="160" />
      <a-table-column title="操作人" data-index="operator" :width="110" />
      <a-table-column title="开始时间" :width="160">
        <template #default="{ record }">
          {{ record.start?.replace('T', ' ').slice(0, 19) }}
        </template>
      </a-table-column>
      <a-table-column title="大小" :width="90">
        <template #default="{ record }">{{ fmtSize(record.size) }}</template>
      </a-table-column>
      <a-table-column title="格式" :width="90">
        <template #default="{ record }">
          <a-tag v-if="record.legacy" color="orange">旧</a-tag>
          <a-tag v-else color="green">cast</a-tag>
        </template>
      </a-table-column>
      <a-table-column title="操作" :width="80">
        <template #default="{ record }">
          <a-button size="small" type="link" @click="play(record)">回放</a-button>
        </template>
      </a-table-column>
      <template #emptyText>
        <a-empty
          :image-style="{ height: '48px' }"
          description="暂无会话记录（录制保留 7 天）"
        />
      </template>
    </a-table>
  </a-drawer>
</template>
