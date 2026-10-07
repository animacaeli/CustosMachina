<script lang="ts" setup>
import type { Component } from 'vue';

import type { HomeSummary, ReadinessItem } from '#/api/home';

import { computed, onMounted, ref } from 'vue';
import { useRouter } from 'vue-router';

import { Activity, FileCog, Rocket, Server } from '@vben/icons';
import { useUserStore } from '@vben/stores';

import { getHomeSummaryApi, getReadinessApi } from '#/api/home';

defineOptions({ name: 'Home' });

const router = useRouter();
const userStore = useUserStore();
const displayName = computed(() => userStore.userInfo?.realName ?? '');

// 运维态势（独立审核第 2 批：登录后第一眼回答"现在是否健康、哪里需要处理"）
const summary = ref<HomeSummary | null>(null);
const summaryLoading = ref(false);

async function loadSummary() {
  summaryLoading.value = true;
  try {
    summary.value = await getHomeSummaryApi();
  } catch {
    // 拦截器已提示；态势区显示占位
  } finally {
    summaryLoading.value = false;
  }
}

interface StatCard {
  danger: boolean;
  hint: string;
  label: string;
  link?: string;
  value: () => number;
}

const statCards = computed<StatCard[]>(() => [
  {
    label: '未处理告警',
    value: () => summary.value?.alerts.open ?? 0,
    danger: (summary.value?.alerts.critical ?? 0) > 0,
    hint: `严重 ${summary.value?.alerts.critical ?? 0}`,
    link: '/resources/observ-events',
  },
  {
    label: '不可达主机(1h)',
    value: () => summary.value?.servers.unreachable1h ?? 0,
    danger: (summary.value?.servers.unreachable1h ?? 0) > 0,
    hint: '近 1 小时事件去重',
    link: '/resources/servers',
  },
  {
    label: '失败发布(7d)',
    value: () => summary.value?.releases.failed7d ?? 0,
    danger: (summary.value?.releases.failed7d ?? 0) > 0,
    hint: '含超时',
    link: '/projects',
  },
  {
    label: '失败任务(7d)',
    value: () => summary.value?.jobs.failed7d ?? 0,
    danger: (summary.value?.jobs.failed7d ?? 0) > 0,
    hint: '定时任务含超时',
    link: '/cron/jobs',
  },
  {
    label: '证书 14d 到期',
    value: () => summary.value?.certs.expiring14d ?? 0,
    danger: (summary.value?.certs.expiring14d ?? 0) > 0,
    hint: '即将到期数',
    link: '/system/certs',
  },
]);

// 系统就绪度（独立审核第 2 批：替代 localStorage 一次性引导——
// 从真实配置状态计算，缺失项常驻提示、可折叠、点击直达设置）
const readiness = ref<ReadinessItem[]>([]);
const readinessOpen = ref(false);

const readinessLabel: Record<string, string> = {
  ai: 'AI 中转层',
  backup: '备份任务',
  ci: 'CI 全局配置',
  im: 'IM 扫码登录',
  notify: '通知群',
  redis: 'Redis 会话',
  target: '主机 / 集群',
};
const readinessLink: Record<string, string> = {
  ai: '/admin?section=ai&tab=ai',
  backup: '/system/backup',
  ci: '/admin?section=delivery&tab=ci',
  im: '/admin?section=identity&tab=im',
  notify: '/admin?section=notify&tab=notify',
  redis: '/admin?section=identity&tab=session',
  target: '/resources/servers',
};

const missingRequired = computed(() =>
  readiness.value.filter((r) => r.status === 'missing' && !r.optional),
);
const missingOptional = computed(() =>
  readiness.value.filter((r) => r.status === 'missing' && r.optional),
);

async function loadReadiness() {
  try {
    const res = await getReadinessApi();
    readiness.value = res.items ?? [];
    const isSuper = (userStore.userInfo?.roles ?? []).includes('superadmin');
    readinessOpen.value =
      isSuper &&
      (missingRequired.value.length > 0 || missingOptional.value.length > 0);
  } catch {
    // 拦截器已提示
  }
}

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
const relStatusColor: Record<string, string> = {
  failed: 'error',
  running: 'processing',
  success: 'success',
  timeout: 'warning',
};
const isAdmin = computed(() => {
  const roles = userStore.userInfo?.roles ?? [];
  return roles.includes('superadmin') || roles.includes('admin');
});

// 功能卡（router-link 直达 + Lucide SVG 图标——独立审核 N10：div@click
// 键盘/读屏不可达，emoji 跨平台外观不一致）
const FEATURES: Array<{
  desc: string;
  icon: Component;
  path: string;
  title: string;
}> = [
  {
    desc: '纳管主机、容器、compose 与 k3s 集群，终端与文件管理',
    icon: Server,
    path: '/resources/servers',
    title: '资源管理',
  },
  {
    desc: '项目三环境发布：蓝绿切流 / 灰度策略 / 测试槽位',
    icon: Rocket,
    path: '/envs/prod',
    title: '环境与发布',
  },
  {
    desc: 'O2 告警闭环 + 五源上下文 AI 诊断 + 统一通知路由',
    icon: Activity,
    path: '/resources/observ-alerts',
    title: '观测与告警',
  },
  {
    desc: '配置文件版本化下发 / 拉取 API / 定时任务沙箱执行',
    icon: FileCog,
    path: '/configs/files',
    title: '配置与任务',
  },
];

const TODO = [
  { key: '/system/user', title: '用户管理' },
  { key: '/system/role', title: '角色权限' },
];

onMounted(() => {
  loadSummary();
  loadReadiness();
});
</script>

<template>
  <div class="p-4">
    <a-card>
      <div class="flex items-center justify-between">
        <div>
          <h2 class="text-xl font-semibold">
            {{ displayName ? `${displayName}，欢迎回来` : '欢迎回来' }}
          </h2>
          <p class="text-muted-foreground mt-1 text-sm">
            轻量级 AI DevOps 运维平台 · 统一控制台与告警诊断
          </p>
        </div>
        <img alt="logo" class="h-14 w-14" src="/logo.svg" />
      </div>
    </a-card>

    <!-- 系统就绪度：常驻可折叠（替代一次性 localStorage 引导） -->
    <div v-if="readiness.length > 0" class="mt-4">
      <a-card size="small">
        <template #title>
          <span class="text-sm">系统就绪度</span>
          <a-tag v-if="missingRequired.length === 0" class="ml-2" color="green">
            就绪
          </a-tag>
          <a-tag v-else class="ml-2" color="red">
            缺 {{ missingRequired.length }} 项必配
          </a-tag>
          <span
            v-if="missingOptional.length > 0"
            class="text-muted-foreground ml-2 text-xs"
          >
            另有 {{ missingOptional.length }} 项可选未配置
          </span>
        </template>
        <template #extra>
          <a-button
            size="small"
            type="link"
            @click="readinessOpen = !readinessOpen"
          >
            {{ readinessOpen ? '收起' : '展开' }}
          </a-button>
        </template>
        <div v-if="readinessOpen" class="grid grid-cols-2 gap-2 md:grid-cols-4">
          <router-link
            v-for="item in readiness"
            :key="item.key"
            :to="readinessLink[item.key] ?? '/admin'"
            class="block rounded border border-border p-2 text-xs hover:border-primary"
          >
            <span v-if="item.status === 'ok'" class="text-green-500">✓</span>
            <span v-else-if="!item.optional" class="text-red-500">✗</span>
            <span v-else class="text-muted-foreground">—</span>
            {{ readinessLabel[item.key] ?? item.key }}
            <span v-if="item.optional" class="text-muted-foreground">
              （可选）
            </span>
          </router-link>
        </div>
      </a-card>
    </div>

    <!-- 运维态势：五个数字 + 待处理事项 + 最近变更 -->
    <div class="mt-4">
      <a-card :loading="summaryLoading" title="运维态势">
        <template #extra>
          <a-button size="small" @click="loadSummary">刷新</a-button>
        </template>
        <div class="grid grid-cols-2 gap-3 md:grid-cols-3 xl:grid-cols-5">
          <router-link
            v-for="card in statCards"
            :key="card.label"
            :to="card.link ?? '/home'"
            class="block rounded border border-border p-3 transition-colors hover:border-primary"
          >
            <div class="text-muted-foreground text-xs">{{ card.label }}</div>
            <div
              class="mt-1 text-2xl font-semibold"
              :class="card.danger ? 'text-red-500' : ''"
            >
              {{ card.value() }}
            </div>
            <div class="text-muted-foreground mt-1 text-xs">
              {{ card.hint }}
            </div>
          </router-link>
        </div>
        <div class="mt-4 grid grid-cols-1 gap-4 lg:grid-cols-2">
          <div>
            <div class="mb-2 text-sm font-medium">待处理告警</div>
            <a-list
              :data-source="summary?.alerts.items ?? []"
              size="small"
              :locale="{ emptyText: '无未处理告警' }"
            >
              <template #renderItem="{ item }">
                <a-list-item>
                  <a-tag :color="levelColor[item.level] ?? 'default'">
                    {{ levelText[item.level] ?? item.level }}
                  </a-tag>
                  <span class="mr-2">{{ item.title }}</span>
                  <span class="text-muted-foreground ml-auto text-xs">
                    {{ new Date(item.createdAt).toLocaleString() }}
                  </span>
                </a-list-item>
              </template>
            </a-list>
          </div>
          <div>
            <div class="mb-2 text-sm font-medium">最近发布</div>
            <a-list
              :data-source="summary?.releases.recent ?? []"
              size="small"
              :locale="{ emptyText: '暂无发布记录' }"
            >
              <template #renderItem="{ item }">
                <a-list-item>
                  <a-badge
                    :status="relStatusColor[item.status] ?? 'default'"
                    :text="`${item.project ?? '—'} · ${item.envType} · ${item.tag}`"
                  />
                  <span class="text-muted-foreground ml-auto text-xs">
                    {{ new Date(item.createdAt).toLocaleString() }}
                  </span>
                </a-list-item>
              </template>
            </a-list>
          </div>
        </div>
      </a-card>
    </div>

    <div class="mt-4 grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-4">
      <!-- router-link 语义化导航（独立审核 N10）；hoverable 视觉保留 -->
      <router-link v-for="f in FEATURES" :key="f.title" :to="f.path">
        <a-card hoverable>
          <component :is="f.icon" class="text-primary size-7" />
          <div class="mt-2 font-medium">{{ f.title }}</div>
          <p class="text-muted-foreground mt-1 text-xs">{{ f.desc }}</p>
        </a-card>
      </router-link>
    </div>

    <!-- 间距放外层 div：a-card 上的工具类会被 antd 样式层覆盖 -->
    <div class="mt-6">
      <a-card title="快捷入口">
        <a-space wrap>
          <a-button @click="router.push('/cron/jobs')">定时任务</a-button>
          <a-button @click="router.push('/resources/observ-alerts')">
            告警中心
          </a-button>
          <template v-if="isAdmin">
            <a-button
              v-for="t in TODO"
              :key="t.key"
              @click="router.push(t.key)"
            >
              {{ t.title }}
            </a-button>
            <a-button type="primary" @click="router.push('/admin')">
              管理后台
            </a-button>
          </template>
        </a-space>
        <p v-if="!isAdmin" class="text-muted-foreground mt-3 text-xs">
          联系管理员开通更多权限
        </p>
        <p class="text-muted-foreground mt-3 text-xs">
          右下角悬浮助手可对话查询平台数据（只读），AI 建议绝不自动执行。
        </p>
      </a-card>
    </div>
  </div>
</template>
