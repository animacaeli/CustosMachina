<script lang="ts" setup>
import { computed, onMounted, ref } from 'vue';
import { useRouter } from 'vue-router';

import { useUserStore } from '@vben/stores';

defineOptions({ name: 'Home' });

const router = useRouter();
const userStore = useUserStore();
const displayName = computed(() => userStore.userInfo?.realName ?? '');
const isAdmin = computed(() => {
  const roles = userStore.userInfo?.roles ?? [];
  return roles.includes('superadmin') || roles.includes('admin');
});

// 功能卡（可点击直达；能力均已在 v0.12.0 交付）
const FEATURES = [
  {
    desc: '纳管主机、容器、compose 与 k3s 集群，终端与文件管理',
    icon: '🖥️',
    path: '/resources/servers',
    title: '资源管理',
  },
  {
    desc: '项目三环境发布：蓝绿切流 / 灰度策略 / 测试槽位',
    icon: '🚀',
    path: '/envs/prod',
    title: '环境与发布',
  },
  {
    desc: 'O2 告警闭环 + 五源上下文 AI 诊断 + 统一通知路由',
    icon: '🧠',
    path: '/resources/observ-alerts',
    title: '观测与告警',
  },
  {
    desc: '配置文件版本化下发 / 拉取 API / 定时任务沙箱执行',
    icon: '⚙️',
    path: '/configs/files',
    title: '配置与任务',
  },
];

const TODO = [
  { key: '/system/user', title: '用户管理' },
  { key: '/system/role', title: '角色权限' },
];

// --- 超管首登引导 ---
const ONBOARD_KEY = 'custos-admin-onboarded';
const showOnboard = ref(false);

const STEPS = [
  {
    description:
      '头像下拉 → 管理后台 → 登录配置：选择企业微信/钉钉/飞书并填入凭证，团队成员即可扫码登录（JIT 注册为访客）。',
    title: '配置 IM 扫码登录',
  },
  {
    description:
      '管理后台 → 会话设置：指定 Redis（登出/踢下线与重启不丢会话）；单实例可跳过（内存模式）。',
    title: '设置 Redis（可选）',
  },
  {
    description:
      '管理后台 → 会话设置：按安全需求调整 access / refresh 有效期（秒）。',
    title: '调整 token 有效期',
  },
  {
    description:
      '系统管理 → 用户管理：调整扫码进来的成员角色（默认访客）；角色权限页可细化各角色的 API 访问矩阵。',
    title: '管理用户与角色',
  },
];

onMounted(() => {
  const isSuper = (userStore.userInfo?.roles ?? []).includes('superadmin');
  if (isSuper && !localStorage.getItem(ONBOARD_KEY)) {
    showOnboard.value = true;
    localStorage.setItem(ONBOARD_KEY, '1');
  }
});

function goAdmin() {
  showOnboard.value = false;
  router.push({ path: '/admin' });
}

function goFeature(path: string) {
  router.push(path);
}
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

    <div class="mt-4 grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-4">
      <!-- hoverable + 点击直达：功能已全部交付，卡片不再是纯展示 -->
      <a-card
        v-for="f in FEATURES"
        :key="f.title"
        hoverable
        @click="goFeature(f.path)"
      >
        <div class="text-2xl">{{ f.icon }}</div>
        <div class="mt-2 font-medium">{{ f.title }}</div>
        <p class="text-muted-foreground mt-1 text-xs">{{ f.desc }}</p>
      </a-card>
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

    <!-- 超管首登引导 -->
    <a-modal
      v-model:open="showOnboard"
      :footer="null"
      title="欢迎使用 CustosMachina 🎉"
      width="560px"
    >
      <a-steps
        :current="-1"
        :items="
          STEPS.map((s, i) => ({
            description: s.description,
            title: `${i + 1}. ${s.title}`,
          }))
        "
        direction="vertical"
        size="small"
      />
      <div class="flex justify-end gap-2 pt-2">
        <a-button @click="showOnboard = false">稍后配置</a-button>
        <a-button type="primary" @click="goAdmin">进入管理后台</a-button>
      </div>
    </a-modal>
  </div>
</template>
