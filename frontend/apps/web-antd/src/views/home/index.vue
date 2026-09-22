<script lang="ts" setup>
import { computed, onMounted, ref } from 'vue';
import { useRouter } from 'vue-router';

import { useUserStore } from '@vben/stores';

defineOptions({ name: 'Home' });

const router = useRouter();
const userStore = useUserStore();
const displayName = computed(() => userStore.userInfo?.realName ?? '');
const isAdmin = computed(() =>
  (userStore.userInfo?.roles ?? []).includes('admin'),
);

const FEATURES = [
  {
    desc: 'Gitea / OpenObserve / AgileConfig 等高频操作统一入口',
    icon: '🖥️',
    title: '统一控制台',
  },
  {
    desc: '告警自动汇聚五源上下文，LLM 输出诊断与建议',
    icon: '🧠',
    title: '告警 AI 诊断',
  },
  {
    desc: '占用制测试环境，推送分支自动部署',
    icon: '🧪',
    title: '测试环境槽位',
  },
  {
    desc: 'compose / swarm / k3s 多形态运行时适配',
    icon: '🚀',
    title: '运行时纳管',
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
  if (isAdmin.value && !localStorage.getItem(ONBOARD_KEY)) {
    showOnboard.value = true;
    localStorage.setItem(ONBOARD_KEY, '1');
  }
});

function goAdmin() {
  showOnboard.value = false;
  router.push({ path: '/admin' });
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
      <a-card v-for="f in FEATURES" :key="f.title">
        <div class="text-2xl">{{ f.icon }}</div>
        <div class="mt-2 font-medium">{{ f.title }}</div>
        <p class="text-muted-foreground mt-1 text-xs">{{ f.desc }}</p>
      </a-card>
    </div>

    <a-card class="mt-4" title="快捷入口">
      <a-space wrap>
        <template v-if="isAdmin">
          <a-button v-for="t in TODO" :key="t.key" @click="router.push(t.key)">
            {{ t.title }}
          </a-button>
          <a-button type="primary" @click="router.push('/admin')">
            管理后台
          </a-button>
        </template>
        <span v-else class="text-muted-foreground text-sm">
          联系管理员开通更多权限
        </span>
      </a-space>
      <p class="text-muted-foreground mt-3 text-xs">
        服务管理 / CI / 日志 / 配置 / 告警中心等功能随第二阶段开发逐步开放。
      </p>
    </a-card>

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
