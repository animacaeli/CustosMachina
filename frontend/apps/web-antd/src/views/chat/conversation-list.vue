<script lang="ts" setup>
import type { Conversation } from '#/api/chat';
import type { PlatformUser } from '#/api/system/user';

import { computed, onMounted, ref } from 'vue';

import { useUserStore } from '@vben/stores';

import { DeleteOutlined, PlusOutlined } from '@ant-design/icons-vue';

import { deleteConversationApi, listConversationsApi } from '#/api/chat';
import { getUserListApi } from '#/api/system/user';

/**
 * 会话历史子视图（P6-M10 用户定调：AI 对话不做独立菜单页，全部功能收进
 * 悬浮抽屉——本组件即抽屉内的历史列表）。管理员可按用户查看与显示已软删
 * 会话（留档），与原 /chat 侧栏同语义。
 */

defineOptions({ name: 'ConversationList' });

const props = defineProps<{
  activeId?: null | number;
}>();

const emit = defineEmits<{
  (e: 'pick', conv: Conversation): void;
  (e: 'new', mode: 'general' | 'platform'): void;
}>();

const conversations = ref<Conversation[]>([]);
const loading = ref(false);

const userStore = useUserStore();
const isAdmin = computed(() => {
  const roles = userStore.userInfo?.roles ?? [];
  return roles.includes('superadmin') || roles.includes('admin');
});
// vben userInfo.userId 是 string，统一转 number（与后端 uint 对齐）
const myId = computed(() => Number(userStore.userInfo?.userId ?? 0) || 0);
const filterUserId = ref<number>(0);
const showDeleted = ref(false);
const users = ref<PlatformUser[]>([]);

async function load() {
  loading.value = true;
  try {
    conversations.value = await listConversationsApi(
      isAdmin.value
        ? { deleted: showDeleted.value, userId: filterUserId.value || myId.value }
        : {},
    );
  } finally {
    loading.value = false;
  }
}

function pick(c: Conversation) {
  if (c.deleted) return; // 已删会话留档只读，不进对话视图
  emit('pick', c);
}

async function removeConversation(id: number) {
  await deleteConversationApi(id);
  if (isAdmin.value && showDeleted.value) {
    await load();
    return;
  }
  conversations.value = conversations.value.filter((c) => c.id !== id);
}

onMounted(async () => {
  filterUserId.value = myId.value;
  if (isAdmin.value) {
    try {
      users.value = await getUserListApi();
    } catch {
      users.value = [
        { displayName: '我', id: myId.value, username: '我' } as PlatformUser,
      ];
    }
  }
  await load();
});

defineExpose({ load });
</script>

<template>
  <div class="flex h-full min-h-0 flex-col">
    <!-- admin 视图：用户筛选 + 显示已删除 -->
    <div v-if="isAdmin" class="flex items-center gap-2 px-3 pt-3">
      <a-select
        :value="filterUserId || myId"
        class="min-w-0 flex-1"
        size="small"
        @change="
          (v: any) => {
            filterUserId = v ?? myId;
            load();
          }
        "
      >
        <a-select-option
          v-for="u in users"
          :key="u.id"
          :value="u.id"
        >
          {{ u.displayName || u.username }}（{{ u.username }}）
        </a-select-option>
      </a-select>
      <a-checkbox
        :checked="showDeleted"
        class="shrink-0"
        @change="
          (e: any) => {
            showDeleted = Boolean(e.target.checked);
            load();
          }
        "
      >
        <span class="text-xs">已删除</span>
      </a-checkbox>
    </div>

    <div class="grid grid-cols-2 gap-2 p-3">
      <a-button size="small" type="primary" @click="emit('new', 'general')">
        <PlusOutlined /> 新对话
      </a-button>
      <a-button size="small" @click="emit('new', 'platform')">平台上下文</a-button>
    </div>

    <a-spin :spinning="loading">
      <div class="min-h-0 flex-1 overflow-y-auto px-2 pb-2">
        <div
          v-for="c in conversations"
          :key="c.id"
          class="group mb-1 flex cursor-pointer items-center gap-2 rounded-md px-2 py-2 hover:bg-accent"
          :class="c.id === props.activeId ? 'bg-accent' : ''"
          @click="pick(c)"
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
            ok-text="删除"
            ok-type="danger"
            title="删除后你不再可见（管理员可留档查看）"
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
    </a-spin>
  </div>
</template>
