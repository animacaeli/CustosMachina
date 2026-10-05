<script lang="ts" setup>
import type { Conversation } from '#/api/chat';
import type { PlatformUser } from '#/api/system/user';

import { computed, onMounted, ref } from 'vue';

import { useUserStore } from '@vben/stores';

import { DeleteOutlined, PlusOutlined } from '@ant-design/icons-vue';
import { message as antMessage } from 'ant-design-vue';

import { deleteConversationApi, listConversationsApi } from '#/api/chat';
import { getUserListApi } from '#/api/system/user';

import ChatPanel from './chat-panel.vue';

defineOptions({ name: 'AiChat' });

// ---- 会话列表 ----
const conversations = ref<Conversation[]>([]);
// 当前会话（对象引用，ChatPanel 按 conv 切换；null = 草稿新对话）
const currentConv = ref<Conversation | null>(null);
const current = computed(() => currentConv.value);
const panelRef = ref();
// 侧栏折叠（收起时只留展开按钮——会话历史收进侧栏，非树形层级）
const sidebarCollapsed = ref(false);
// admin 全量视图：查看全平台会话（含归属人）；普通用户恒 false
const userStore = useUserStore();
const isAdmin = computed(() => {
  const roles = userStore.userInfo?.roles ?? [];
  return roles.includes('superadmin') || roles.includes('admin');
});
// admin 视图：按用户查看对话历史（默认当前用户）+ 可选显示已软删会话（留档）
// vben userInfo.userId 是 string，统一转 number（与后端 uint 对齐）
const myId = computed(() => Number(userStore.userInfo?.userId ?? 0) || 0);
// 只读视图：已删会话（留档）或他人的会话（跨用户仅查看，不能代发消息）
const readonlyView = computed(
  () =>
    !!current.value &&
    (current.value.deleted === true || current.value.userId !== myId.value),
);
const filterUserId = ref<number>(0);
const showDeleted = ref(false);
const users = ref<PlatformUser[]>([]);

async function loadConversations() {
  conversations.value = await listConversationsApi(
    isAdmin.value
      ? { deleted: showDeleted.value, userId: filterUserId.value || myId.value }
      : {},
  );
}

async function onFilterUser(val: number | undefined) {
  filterUserId.value = val ?? myId.value;
  currentConv.value = null;
  await loadConversations();
}

async function toggleDeleted(checked: any) {
  showDeleted.value = Boolean(checked);
  currentConv.value = null;
  await loadConversations();
}

// 惰性建会话：点「新对话/平台上下文」只进草稿态，首条消息发送才真正创建——
// 新建后没开口的对话不落库、不进历史（空会话直接丢弃）
const draftMode = ref<'general' | 'platform' | null>(null);
const activeMode = computed(() => current.value?.mode ?? draftMode.value);

function newConversation(mode: 'general' | 'platform') {
  draftMode.value = mode;
  currentConv.value = null;
  // 正在看别人的历史时新建：切回自己的视图（新会话归属当前管理员）
  if (isAdmin.value && filterUserId.value !== myId.value) {
    filterUserId.value = myId.value;
    loadConversations();
  }
}

// ChatPanel 惰性建会话后回填列表并选中
function onUsed(conv: Conversation) {
  if (!conversations.value.some((c) => c.id === conv.id)) {
    conversations.value.unshift(conv);
  }
  currentConv.value = conv;
}

async function openConversation(id: number) {
  if (panelRef.value?.isStreaming()) {
    antMessage.warning('当前会话回复进行中，请先停止');
    return;
  }
  draftMode.value = null;
  currentConv.value = conversations.value.find((c) => c.id === id) ?? null;
}

async function removeConversation(id: number) {
  await deleteConversationApi(id);
  // 软删除：勾选「显示已删除」时会话仍在（带标记），刷新即可；否则直接移除
  if (isAdmin.value && showDeleted.value) {
    await loadConversations();
    return;
  }
  conversations.value = conversations.value.filter((c) => c.id !== id);
  if (currentConv.value?.id === id) {
    currentConv.value = null;
  }
}

onMounted(async () => {
  filterUserId.value = myId.value;
  if (isAdmin.value) {
    try {
      users.value = await getUserListApi();
    } catch {
      users.value = [{ displayName: '我', id: myId.value, username: '我' } as PlatformUser];
    }
  }
  await loadConversations();
});
</script>

<template>
  <div class="flex h-full gap-3 p-3">
    <!-- 会话侧栏：折叠后只留展开按钮（会话历史整体收起，非树形层级） -->
    <div
      v-if="!sidebarCollapsed"
      class="flex w-72 shrink-0 flex-col rounded-lg border border-border bg-card"
    >
      <!-- 折叠图标同一行：用户下拉（默认当前用户）+「显示已删除会话历史」；均仅 admin 可见 -->
      <div class="flex items-center gap-2 px-3 pt-3">
        <a-button size="small" type="text" title="收起会话列表" @click="sidebarCollapsed = true">
          <span class="text-muted-foreground">⟨</span>
        </a-button>
        <a-select
          v-if="isAdmin"
          :value="filterUserId || myId"
          class="min-w-0 flex-1"
          size="small"
          @change="onFilterUser($event)"
        >
          <a-select-option v-for="u in users" :key="u.id" :value="u.id">
            {{ u.displayName || u.username }}（{{ u.username }}）
          </a-select-option>
        </a-select>
        <a-checkbox
          v-if="isAdmin"
          class="shrink-0"
          :checked="showDeleted"
          @change="toggleDeleted($event.target.checked)"
        >
          <span class="whitespace-nowrap text-xs">显示已删除会话历史</span>
        </a-checkbox>
      </div>
      <div class="flex gap-2 p-3">
        <a-button block type="primary" @click="newConversation('general')">
          <PlusOutlined /> 新对话
        </a-button>
        <a-button block @click="newConversation('platform')">
          平台上下文
        </a-button>
      </div>
      <div class="flex-1 overflow-y-auto px-2 pb-2">
        <div
          v-for="c in conversations"
          :key="c.id"
          class="group mb-1 flex cursor-pointer items-center gap-2 rounded-md px-2 py-2 hover:bg-accent"
          :class="c.id === currentConv?.id ? 'bg-accent' : ''"
          @click="openConversation(c.id)"
        >
          <div class="min-w-0 flex-1">
            <div
              class="truncate text-sm"
              :class="c.deleted ? 'text-muted-foreground line-through' : ''"
            >
              {{ c.title || '新对话' }}
            </div>
            <div class="mt-0.5 flex items-center gap-1 text-xs text-muted-foreground">
              <span v-if="c.deleted" class="text-red-500">已删除</span>
              <span v-if="c.deleted">·</span>
              <span v-if="c.owner" class="text-primary">{{ c.owner }}</span>
              <span v-if="c.owner">·</span>
              <span>{{ c.mode === 'platform' ? '平台上下文' : '通用' }}</span>
              <span>·</span>
              <span>{{ (c.updatedAt ?? '').slice(5, 16).replace('T', ' ') }}</span>
            </div>
          </div>
          <a-popconfirm
            v-if="!c.deleted"
            title="删除后你不再可见（管理员可留档查看）"
            ok-text="删除"
            ok-type="danger"
            @confirm="removeConversation(c.id)"
          >
            <a-button
              class="opacity-40 group-hover:opacity-100"
              danger
              size="small"
              title="删除会话"
              type="text"
              @click.stop
            >
              <DeleteOutlined />
            </a-button>
          </a-popconfirm>
        </div>
        <a-empty
          v-if="conversations.length === 0"
          :image-style="{ height: '48px' }"
          description="暂无会话"
        />
      </div>
    </div>

    <!-- 折叠态：窄条展开按钮 -->
    <div
      v-if="sidebarCollapsed"
      class="flex w-9 shrink-0 flex-col items-center rounded-lg border border-border bg-card py-3"
    >
      <a-button size="small" type="text" title="展开会话列表" @click="sidebarCollapsed = false">
        <span class="text-muted-foreground">⟩</span>
      </a-button>
    </div>

    <!-- 对话主区 -->
    <div
      class="flex min-w-0 flex-1 flex-col rounded-lg border border-border bg-card"
    >
      <template v-if="current || draftMode">
        <div class="flex items-center gap-2 border-b border-border px-4 py-2.5">
          <span class="text-sm font-medium">{{ current?.title || '新对话' }}</span>
          <a-tag v-if="activeMode === 'platform'" color="geekblue">
            平台上下文
          </a-tag>
          <a-tag v-if="current?.deleted" color="red">已删除 · 留档只读</a-tag>
          <a-tag v-else-if="current && current.userId !== myId" color="orange">
            他人会话 · 只读
          </a-tag>
          <div class="flex-1"></div>
        </div>

        <ChatPanel
          ref="panelRef"
          :conv="currentConv"
          :initial-mode="draftMode ?? 'platform'"
          :readonly="readonlyView"
          @used="onUsed"
        />
      </template>

      <div v-else class="flex flex-1 flex-col items-center justify-center gap-3">
        <div class="text-4xl">🤖</div>
        <div class="text-muted-foreground">
          新建对话开始——通用模式不接平台数据，平台上下文模式自动注入全平台实时数据
        </div>
        <div class="flex gap-2">
          <a-button type="primary" @click="newConversation('general')">
            新对话
          </a-button>
          <a-button @click="newConversation('platform')">平台上下文</a-button>
        </div>
      </div>
    </div>
</div>
</template>
