<script lang="ts" setup>
import { computed } from 'vue';
import { useRouter } from 'vue-router';

import { useUserStore } from '@vben/stores';

defineOptions({ name: 'Dashboard' });

const router = useRouter();
const userStore = useUserStore();
const displayName = computed(() => userStore.userInfo?.realName ?? '');

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
  { key: 'user', title: '用户管理' },
  { key: 'role', title: '角色权限' },
  { key: 'im', title: '登录配置' },
  { key: 'setting', title: '系统设置' },
];

function go(key: string) {
  router.push({ path: `/system/${key}` });
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
        <a-button v-for="t in TODO" :key="t.key" @click="go(t.key)">
          {{ t.title }}
        </a-button>
      </a-space>
      <p class="text-muted-foreground mt-3 text-xs">
        服务管理 / CI / 日志 / 配置 / 告警中心等功能随第二阶段开发逐步开放。
      </p>
    </a-card>
  </div>
</template>
