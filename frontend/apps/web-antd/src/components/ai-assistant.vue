<script lang="ts" setup>
import type { Conversation } from '#/api/chat';

import { computed, defineAsyncComponent, nextTick, onMounted, ref } from 'vue';
import { useRouter } from 'vue-router';

import { useAccessStore } from '@vben/stores';

import { listConversationsApi } from '#/api/chat';

/**
 * 全站 AI 悬浮助手（P6-M10）：挂在 App 根（router-view 外）——路由切换
 * 组件不销毁，抽屉内对话内容与进行中的流式输出持续。悬浮球为自绘 SVG
 * 机器人（渐变 + 呼吸/回答动画）；面板为无遮罩悬浮抽屉（打开时页面仍可
 * 操作——边看页面边问）。未登录隐藏；ChatPanel 异步加载（不点开零开销）。
 */

defineOptions({ name: 'AiAssistant' });

const ChatPanel = defineAsyncComponent(() => import('#/views/chat/chat-panel.vue'));

const accessStore = useAccessStore();
const router = useRouter();

// 登录/初始化页不显示
const visible = computed(() => !!accessStore.accessToken);

const open = ref(false);
const panelConv = ref<Conversation | null>(null);
const panelRef = ref();
const bootstrapped = ref(false);

const streaming = computed(() => panelRef.value?.isStreaming?.() ?? false);

// 快捷提问（结合平台真实能力，空状态一键发送）
const QUICK_QUESTIONS = [
  '平台现在有哪些主机？各跑着什么容器？',
  '最近有没有失败的构建或发布？',
  '每日数据库备份为什么失败了？',
  '怎么给项目配置发布通知？',
];

async function toggle() {
  open.value = !open.value;
  if (open.value && !bootstrapped.value) {
    bootstrapped.value = true;
    // 接续最近一次平台上下文会话；没有则空草稿（首条消息时新建）
    try {
      const list = await listConversationsApi();
      const last = list.find((c) => c.mode === 'platform' && !c.deleted);
      if (last) panelConv.value = last;
    } catch {
      /* 列表失败不阻塞：空草稿可用 */
    }
  }
  if (open.value) {
    await nextTick();
  }
}

function onKeydown(e: KeyboardEvent) {
  if (e.key === 'Escape' && open.value) {
    // 输入框组合/命令面板的 Esc 由面板自身处理（stopPropagation 不可靠，
    // 这里仅当焦点不在输入框时收起）
    const el = document.activeElement;
    if (!el || !(el instanceof HTMLTextAreaElement || el instanceof HTMLInputElement)) {
      open.value = false;
    }
  }
}

function newChat() {
  if (panelRef.value?.isStreaming?.()) return;
  panelConv.value = null;
}

function goFullHistory() {
  open.value = false;
  router.push('/chat');
}

// 抽屉内会话被创建/使用：同步引用（保持接续语义）
function onUsed(conv: Conversation) {
  panelConv.value = conv;
}

onMounted(() => {
  window.addEventListener('keydown', onKeydown);
});
</script>

<template>
  <template v-if="visible">
    <!-- 悬浮球：自绘 SVG 机器人（渐变 + 呼吸；回答时眼睛扫描动画） -->
    <button
      class="ai-fab group fixed bottom-6 right-6 z-[1000] flex h-14 w-14 items-center justify-center rounded-full shadow-lg transition-transform duration-200 hover:-translate-y-1"
      :class="open ? 'scale-95' : 'ai-breathing'"
      title="AI 助手"
      type="button"
      @click="toggle"
    >
      <svg
        class="h-9 w-9"
        fill="none"
        viewBox="0 0 48 48"
        xmlns="http://www.w3.org/2000/svg"
      >
        <defs>
          <linearGradient id="aiFabGrad" x1="0" y1="0" x2="48" y2="48">
            <stop offset="0%" stop-color="var(--primary)" />
            <stop offset="100%" stop-color="rgb(139 92 246)" />
          </linearGradient>
        </defs>
        <!-- 头部圆角矩形底盘 -->
        <rect fill="url(#aiFabGrad)" height="30" rx="10" width="36" x="6" y="12" />
        <!-- 天线 -->
        <line stroke="rgb(139 92 246)" stroke-linecap="round" stroke-width="2.5" x1="24" x2="24" y1="8" y2="12" />
        <circle :class="{ 'ai-antenna-busy': streaming }" cx="24" cy="6.5" fill="rgb(139 92 246)" r="2.5" />
        <!-- 耳侧 -->
        <rect fill="rgb(139 92 246)" height="10" rx="2.5" width="4" x="2" y="22" />
        <rect fill="rgb(139 92 246)" height="10" rx="2.5" width="4" x="42" y="22" />
        <!-- 眼睛：常态两点；回答时扫描动画 -->
        <g :class="streaming ? 'ai-eyes-busy' : ''" fill="#fff">
          <circle class="ai-eye" cx="17" cy="27" r="3" />
          <circle class="ai-eye" cx="31" cy="27" r="3" />
        </g>
        <!-- 嘴：微笑弧 -->
        <path d="M18 33 Q24 37 30 33" stroke="rgb(255 255 255 / 0.85)" stroke-linecap="round" stroke-width="2" />
      </svg>
      <!-- 回答中的光圈 -->
      <span
        v-if="streaming"
        class="ai-halo absolute inset-0 rounded-full border-2 border-primary/60"
      ></span>
    </button>

    <!-- 悬浮面板：自绘 fixed 浮层（无遮罩、路由切换不销毁——挂在 App 根；
         不用 a-drawer：inline 模式点击页面会被 antd 置关，行为不可控） -->
    <transition name="ai-pop">
      <div
        v-if="open"
        class="bg-card fixed top-[8vh] right-[5.5rem] bottom-24 z-[999] flex w-[420px] max-w-[calc(100vw-7rem)] flex-col overflow-hidden rounded-2xl border border-border shadow-2xl"
      >
        <!-- 面板头部 -->
        <div class="flex items-center gap-2 border-b border-border px-4 py-3">
          <span class="text-sm font-medium">AI 助手</span>
          <a-tag v-if="panelConv?.mode === 'platform'" color="geekblue">平台上下文</a-tag>
          <span v-if="panelConv?.deleted" class="text-xs text-red-500">已删除 · 只读</span>
          <div class="flex-1"></div>
          <a-button size="small" type="text" @click="newChat">新对话</a-button>
          <a-button size="small" type="text" @click="goFullHistory">完整历史</a-button>
          <a-button size="small" type="text" @click="open = false">✕</a-button>
        </div>

        <!-- 快捷提问（无会话时的欢迎态） -->
        <div v-if="!panelConv && !(panelRef?.isStreaming?.())" class="border-b border-border p-4">
          <div class="mb-2 text-xs text-muted-foreground">
            有问题随时问——已接入平台实时数据（主机 / 构建 / 发布 / 定时任务）
          </div>
          <div class="grid grid-cols-1 gap-2">
            <button
              v-for="q in QUICK_QUESTIONS"
              :key="q"
              class="rounded-md border border-border bg-background px-3 py-2 text-left text-xs transition-colors hover:border-primary/50 hover:bg-accent"
              type="button"
              @click="panelRef?.sendText(q)"
            >
              {{ q }}
            </button>
          </div>
        </div>

        <!-- 对话面板（异步加载；切路由不销毁） -->
        <Suspense>
          <ChatPanel
            ref="panelRef"
            :conv="panelConv"
            initial-mode="platform"
            @used="onUsed"
          />
          <template #fallback>
            <div class="flex flex-1 items-center justify-center text-muted-foreground">
              加载对话组件…
            </div>
          </template>
        </Suspense>
      </div>
    </transition>
  </template>
</template>

<style scoped>
/* 常态呼吸 */
.ai-breathing {
  animation: ai-breathe 3.2s ease-in-out infinite;
}
@keyframes ai-breathe {
  0%,
  100% {
    transform: scale(1);
    box-shadow: 0 4px 14px rgb(0 0 0 / 0.18);
  }
  50% {
    transform: scale(1.045);
    box-shadow: 0 6px 20px rgb(0 0 0 / 0.26);
  }
}

/* 回答中：天线闪烁 + 眼睛左右扫描 + 光圈扩散 */
.ai-antenna-busy {
  animation: ai-blink 0.9s ease-in-out infinite;
}
@keyframes ai-blink {
  0%,
  100% {
    opacity: 1;
  }
  50% {
    opacity: 0.25;
  }
}
.ai-eyes-busy .ai-eye {
  animation: ai-scan 1.2s ease-in-out infinite;
}
.ai-eyes-busy .ai-eye:nth-child(2) {
  animation-delay: 0.12s;
}
@keyframes ai-scan {
  0%,
  100% {
    transform: translateX(0);
  }
  50% {
    transform: translateX(2.5px);
  }
}
.ai-halo {
  animation: ai-halo 1.4s ease-out infinite;
}
@keyframes ai-halo {
  0% {
    transform: scale(1);
    opacity: 0.9;
  }
  100% {
    transform: scale(1.35);
    opacity: 0;
  }
}
</style>

<style>
/* 面板进出：右下滑入/出 */
.ai-pop-enter-active,
.ai-pop-leave-active {
  transition:
    transform 0.22s ease,
    opacity 0.22s ease;
}
.ai-pop-enter-from,
.ai-pop-leave-to {
  transform: translateY(12px) scale(0.98);
  opacity: 0;
}
</style>