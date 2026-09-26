<script lang="ts" setup>
import type { ManagedServer } from '#/api/resources/server';

import { nextTick, onBeforeUnmount, ref, watch } from 'vue';

import { FitAddon } from '@xterm/addon-fit';
import { Terminal } from '@xterm/xterm';

import { apiURL } from '#/api/request';
import { issueTicketApi } from '#/api/resources/containers';

import '@xterm/xterm/css/xterm.css';

const props = defineProps<{ server: ManagedServer | null }>();
const open = defineModel<boolean>('open', { default: false });

const fullscreen = ref(false);
const containerRef = ref<HTMLDivElement>();
let term: null | Terminal = null;
let fit: FitAddon | null = null;
let ws: null | WebSocket = null;

function wsURL(id: number, ticket: string) {
  // 由 apiURL 推导 WS 地址（跟随部署配置，而非硬编码 /api 同源）；
  // 一次性 ticket 替代长期 token
  const url = new URL(
    `${apiURL}/servers/${id}/terminal`,
    window.location.origin,
  );
  url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:';
  url.searchParams.set('ticket', ticket);
  return url.toString();
}

function cleanup() {
  window.removeEventListener('resize', onWindowResize);
  ws?.close();
  ws = null;
  term?.dispose();
  term = null;
  fit = null;
}

function onWindowResize() {
  fit?.fit();
}

async function start() {
  const s = props.server;
  if (!s || !containerRef.value || term) return;
  await nextTick();

  term = new Terminal({
    cursorBlink: true,
    fontSize: 13,
    theme: { background: '#1e1e1e' },
  });
  fit = new FitAddon();
  term.loadAddon(fit);
  term.open(containerRef.value);
  fit.fit();

  const { ticket } = await issueTicketApi();
  ws = new WebSocket(wsURL(s.id, ticket));
  ws.binaryType = 'arraybuffer';
  const sendResize = () => {
    if (ws?.readyState === WebSocket.OPEN && term) {
      ws.send(
        JSON.stringify({
          action: 'resize',
          cols: term.cols,
          rows: term.rows,
        }),
      );
    }
  };
  ws.addEventListener('open', sendResize);
  ws.addEventListener('message', (ev) => {
    term?.write(
      typeof ev.data === 'string' ? ev.data : new Uint8Array(ev.data),
    );
  });
  ws.addEventListener('close', () =>
    term?.write('\r\n\u001B[31m[连接已关闭]\u001B[0m\r\n'),
  );
  // 输入统一走二进制帧（后端区分二进制=输入、文本=控制）
  const encoder = new TextEncoder();
  term.onData((data) => {
    if (ws?.readyState === WebSocket.OPEN) {
      ws.send(encoder.encode(data) as unknown as ArrayBuffer);
    }
  });

  window.addEventListener('resize', onWindowResize);
}

// 关键：antd Modal 内容是异步挂载的（动画 + destroy-on-close），open 置真时
// containerRef 往往还是 undefined。同时监听 open 和 containerRef，ref 就绪后
// 再启动，彻底修掉"打开后黑屏无显示"的竞态。
watch([open, containerRef], ([isOpen, el]) => {
  if (isOpen && el) {
    start().then(() => setTimeout(() => fit?.fit(), 50));
  } else if (!isOpen) {
    cleanup();
  }
});

onBeforeUnmount(cleanup);

function toggleFullscreen() {
  fullscreen.value = !fullscreen.value;
  setTimeout(() => fit?.fit(), 50);
}
</script>

<template>
  <a-modal
    v-model:open="open"
    :footer="null"
    :title="`终端：${server?.name ?? ''} (${server?.host ?? ''})`"
    :width="fullscreen ? '100vw' : 1180"
    :wrap-class-name="fullscreen ? 'full-modal' : ''"
    :style="fullscreen ? { top: 0, paddingBottom: 0 } : {}"
    destroy-on-close
    @cancel="cleanup"
  >
    <div
      ref="containerRef"
      :style="{ height: fullscreen ? '94vh' : '76vh', background: '#1e1e1e' }"
      class="p-1"
    ></div>
    <template #title>
      <div class="flex items-center gap-2">
        <span>终端：{{ server?.name }} ({{ server?.host }})</span>
        <a-button size="small" @click="toggleFullscreen">
          {{ fullscreen ? '退出全屏' : '全屏' }}
        </a-button>
      </div>
    </template>
  </a-modal>
</template>
