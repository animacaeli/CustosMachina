<script lang="ts" setup>
import { computed, ref } from 'vue';
import { useRoute, useRouter } from 'vue-router';

import AiConfig from './tabs/ai-config.vue';
import AiSkills from './tabs/ai-skills.vue';
import AlertTemplates from './tabs/alert-templates.vue';
import CiConfig from './tabs/ci-config.vue';
import ImConfig from './tabs/im-config.vue';
import K3sClusters from './tabs/k3s-clusters.vue';
import McpTokens from './tabs/mcp-tokens.vue';
import NotifyGroups from './tabs/notify-groups.vue';
import NotifyRoutes from './tabs/notify-routes.vue';
import PullTokens from './tabs/pull-tokens.vue';
import Session from './tabs/session.vue';

defineOptions({ name: 'AdminConsole' });

// 四域分组（独立审核 U1）：11 个平铺 tab 收敛为二级导航，URL 带 section 可深链
const SECTIONS = [
  {
    key: 'identity',
    label: '身份与安全',
    tabs: [
      { key: 'im', label: '登录配置' },
      { key: 'session', label: '会话设置' },
    ],
  },
  {
    key: 'notify',
    label: '通知与告警',
    tabs: [
      { key: 'notify', label: '通知群聊' },
      { key: 'notify-routes', label: '通知路由' },
      { key: 'alert-templates', label: '告警模板' },
    ],
  },
  {
    key: 'delivery',
    label: '交付与基础设施',
    tabs: [
      { key: 'ci', label: 'CI / 镜像仓库' },
      { key: 'k3s', label: 'k3s 集群' },
      { key: 'pull-tokens', label: '配置拉取' },
    ],
  },
  {
    key: 'ai',
    label: 'AI',
    tabs: [
      { key: 'ai', label: '中转层' },
      { key: 'skills', label: '技能' },
      { key: 'mcp', label: 'MCP 接入' },
    ],
  },
] as const;

const allTabs = computed(() => SECTIONS.flatMap((sec) => sec.tabs));
const activeTab = ref<string>(initTab());

const route = useRoute();
const router = useRouter();

function initTab(): string {
  const q = route.query.tab;
  if (typeof q === 'string' && allTabs.value.some((t) => t.key === q)) {
    return q;
  }
  return 'im';
}

// 切 tab 同步 URL（深链/刷新保持位置）
function onTabChange(key: number | string) {
  activeTab.value = String(key);
  router.replace({ query: { ...route.query, tab: String(key) } });
}
</script>

<template>
  <div class="p-4">
    <a-card>
      <template #title>
        <span class="text-base">管理后台</span>
        <span class="text-muted-foreground ml-2 text-xs font-normal">
          平台级配置（登录 / 通知 / AI / CI / 集群与各类接入凭证）
        </span>
      </template>
      <a-tabs v-model:active-key="activeTab" @change="onTabChange">
        <a-tab-pane key="im" tab="登录配置">
          <ImConfig />
        </a-tab-pane>
        <a-tab-pane key="session" tab="会话设置">
          <Session />
        </a-tab-pane>
        <a-tab-pane key="notify" tab="通知群聊">
          <NotifyGroups />
        </a-tab-pane>
        <a-tab-pane key="notify-routes" tab="通知路由">
          <NotifyRoutes />
        </a-tab-pane>
        <a-tab-pane key="alert-templates" tab="告警模板">
          <AlertTemplates />
        </a-tab-pane>
        <a-tab-pane key="ai" tab="AI 中转层">
          <AiConfig />
        </a-tab-pane>
        <a-tab-pane key="mcp" tab="MCP 接入">
          <McpTokens />
        </a-tab-pane>
        <a-tab-pane key="pull-tokens" tab="配置拉取">
          <PullTokens />
        </a-tab-pane>
        <a-tab-pane key="skills" tab="AI 技能">
          <AiSkills />
        </a-tab-pane>
        <a-tab-pane key="k3s" tab="k3s 集群">
          <K3sClusters />
        </a-tab-pane>
        <a-tab-pane key="ci" tab="CI / 镜像仓库">
          <CiConfig />
        </a-tab-pane>
      </a-tabs>
      <div
        class="border-t border-border mt-2 pt-2 flex flex-wrap gap-2 text-xs"
      >
        <span class="text-muted-foreground">分组：</span>
        <a
          v-for="sec in SECTIONS"
          :key="sec.key"
          class="cursor-pointer"
          @click="onTabChange(sec.tabs[0].key)"
        >
          {{ sec.label }}（{{ sec.tabs.length }}）
        </a>
      </div>
    </a-card>
  </div>
</template>
