<script lang="ts" setup>
import type { NotificationItem } from '@vben/layouts';

import type { InboxAction } from '#/api/core/inbox';

import { computed, onMounted, onUnmounted, ref, watch } from 'vue';
import { useRouter } from 'vue-router';

import { AuthenticationLoginExpiredModal } from '@vben/common-ui';
import { useWatermark } from '@vben/hooks';
import {
  BasicLayout,
  LockScreen,
  Notification,
  UserDropdown,
} from '@vben/layouts';
import { preferences, usePreferences } from '@vben/preferences';
import { useAccessStore, useUserStore } from '@vben/stores';

import { getInboxApi, updateInboxApi } from '#/api/core/inbox';
import { useAuthStore } from '#/store';
import LoginForm from '#/views/_core/authentication/login.vue';

const notifications = ref<NotificationItem[]>([]);

const router = useRouter();
const userStore = useUserStore();
const authStore = useAuthStore();
const accessStore = useAccessStore();
const { destroyWatermark, updateWatermark } = useWatermark();
const { isDark } = usePreferences();
const unread = ref(0);
const showDot = computed(() => unread.value > 0);
const inboxOpen = ref(false);
const olderItems = ref<NotificationItem[]>([]);
const inboxItems = computed(() => [
  ...notifications.value,
  ...olderItems.value,
]);
const next = ref(0);
const inboxLoading = ref(false);
let inboxRequest = 0;
let inboxSession = 0;
let pendingInboxChanges = 0;
let inboxChanges = Promise.resolve();
let inboxPoll: ReturnType<typeof setInterval> | undefined;

async function loadInbox(older = false) {
  if (
    !accessStore.accessToken ||
    accessStore.loginExpired ||
    pendingInboxChanges
  )
    return;
  if (older && (!next.value || inboxLoading.value)) return;
  const request = ++inboxRequest;
  inboxLoading.value = true;
  try {
    const result = await getInboxApi(older ? next.value : undefined);
    if (request !== inboxRequest) return;
    const items: NotificationItem[] = result.items.map((item) => ({
      ...item,
      avatar: preferences.logo.source || preferences.app.defaultAvatar,
      date: new Date(item.createdAt).toLocaleString('zh-CN'),
      link: item.projectId ? `/projects/${item.projectId}` : undefined,
    }));
    unread.value = result.unread;
    if (older) {
      olderItems.value.push(...items);
    } else {
      notifications.value = items;
      olderItems.value = [];
    }
    next.value = result.next;
  } catch {
    // 轮询失败后保留最近一次成功加载的数据。
  } finally {
    if (request === inboxRequest) inboxLoading.value = false;
  }
}

watch(
  () => userStore.userInfo?.userId,
  () => {
    inboxSession++;
    inboxRequest++;
    inboxLoading.value = false;
    pendingInboxChanges = 0;
    inboxChanges = Promise.resolve();
    notifications.value = [];
    olderItems.value = [];
    unread.value = 0;
    next.value = 0;
    inboxOpen.value = false;
    void loadInbox();
  },
);

onMounted(() => {
  void loadInbox();
  inboxPoll = setInterval(() => {
    if (!document.hidden && !inboxOpen.value && !inboxLoading.value)
      void loadInbox();
  }, 30_000);
});
onUnmounted(() => {
  inboxSession++;
  inboxRequest++;
  clearInterval(inboxPoll);
});

const isAdmin = computed(() => {
  const roles = userStore.userInfo?.roles ?? [];
  return roles.includes('superadmin') || roles.includes('admin');
});

const menus = computed(() => {
  // 平台级配置入口仅对超管显示（个人中心等页面未建设，不留死入口）
  const list: Array<{
    handler: () => void;
    icon: any;
    text: string;
  }> = [];
  if (isAdmin.value) {
    list.push({
      handler: () => {
        router.push({ path: '/admin' });
      },
      icon: 'lucide:wrench',
      text: '管理后台',
    });
  }
  return list;
});

const avatar = computed(() => {
  return userStore.userInfo?.avatar ?? preferences.app.defaultAvatar;
});

async function handleLogout() {
  await authStore.logout(false);
}

function changeInbox(action: InboxAction, id?: number) {
  const session = inboxSession;
  pendingInboxChanges++;
  inboxRequest++;
  inboxLoading.value = false;
  inboxChanges = inboxChanges.then(async () => {
    if (session !== inboxSession) return;
    try {
      const result = await updateInboxApi(action, id);
      if (session !== inboxSession) return;
      for (const page of [notifications, olderItems]) {
        if (action === 'clear') {
          page.value = [];
        } else if (action === 'remove') {
          page.value = page.value.filter((item) => item.id !== id);
        } else {
          for (const item of page.value) {
            if (action === 'read-all' || item.id === id) item.isRead = true;
          }
        }
      }
      unread.value = result.unread;
      if (action === 'clear') next.value = 0;
    } catch {
      // 请求拦截器显示错误。
    } finally {
      if (session === inboxSession) pendingInboxChanges--;
    }
  });
}

const handleClick = (item: NotificationItem) => {
  if (!item.isRead) void changeInbox('read', Number(item.id));
  if (item.link) {
    inboxOpen.value = false;
    navigateTo(item.link, item.query, item.state);
  }
};

function navigateTo(
  link: string,
  query?: Record<string, any>,
  state?: Record<string, any>,
) {
  if (link.startsWith('http://') || link.startsWith('https://')) {
    // 外部链接，在新标签页打开
    window.open(link, '_blank');
  } else {
    // 内部路由链接，支持 query 参数和 state
    router.push({
      path: link,
      query: query || {},
      state,
    });
  }
}

watch(
  () => ({
    enable: preferences.app.watermark,
    content: preferences.app.watermarkContent,
    isDark: isDark.value,
  }),
  async ({ enable, content, isDark: isDarkValue }) => {
    if (enable) {
      const watermarkColor = isDarkValue
        ? 'rgba(255, 255, 255, 0.12)'
        : 'rgba(0, 0, 0, 0.12)';

      await updateWatermark({
        advancedStyle: {
          colorStops: [
            {
              color: watermarkColor,
              offset: 0,
            },
            {
              color: watermarkColor,
              offset: 1,
            },
          ],
          type: 'linear',
        },
        content:
          content ||
          `${userStore.userInfo?.username} - ${userStore.userInfo?.realName}`,
      });
    } else {
      destroyWatermark();
    }
  },
  {
    immediate: true,
  },
);
</script>

<template>
  <BasicLayout
    :avatar
    :text="userStore.userInfo?.realName"
    @clear-preferences-and-logout="handleLogout"
    @logout="handleLogout"
  >
    <template #user-dropdown>
      <UserDropdown
        :avatar
        :menus
        :text="userStore.userInfo?.realName"
        @clear-preferences-and-logout="handleLogout"
        @logout="handleLogout"
      />
    </template>
    <template #notification>
      <Notification
        :dot="showDot"
        :notifications="notifications"
        @clear="changeInbox('clear')"
        @read="(item) => changeInbox('read', Number(item.id))"
        @remove="(item) => changeInbox('remove', Number(item.id))"
        @make-all="changeInbox('read-all')"
        @on-click="handleClick"
        @view-all="inboxOpen = true"
      />
    </template>
    <template #extra>
      <a-drawer v-model:open="inboxOpen" title="站内通知" width="440">
        <a-empty v-if="inboxItems.length === 0" description="暂无通知" />
        <ul v-else class="divide-y">
          <li v-for="item in inboxItems" :key="item.id" class="py-3">
            <button
              type="button"
              class="text-left font-medium"
              @click="handleClick(item)"
            >
              <span v-if="!item.isRead" class="text-primary" aria-label="未读"
                >●</span
              >
              {{ item.title }}
            </button>
            <p class="my-1 text-sm">{{ item.message }}</p>
            <p class="text-muted-foreground text-xs">{{ item.date }}</p>
          </li>
        </ul>
        <a-button v-if="next" :loading="inboxLoading" @click="loadInbox(true)">
          加载更多
        </a-button>
      </a-drawer>
      <AuthenticationLoginExpiredModal
        v-model:open="accessStore.loginExpired"
        :avatar
      >
        <LoginForm />
      </AuthenticationLoginExpiredModal>
    </template>
    <template #lock-screen>
      <LockScreen :avatar @to-login="handleLogout" />
    </template>
  </BasicLayout>
</template>
