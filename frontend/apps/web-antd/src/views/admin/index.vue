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
const SECTIONS_RAW = [
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
];

interface AdminTab {
  key: string;
  label: string;
}
interface AdminSection {
  key: string;
  label: string;
  tabs: AdminTab[];
}

const SECTIONS: AdminSection[] = SECTIONS_RAW;

const route = useRoute();
const router = useRouter();

const activeSection = ref<string>(initSection());
const activeTab = ref<string>(initTab());

function initSection(): string {
  const q = route.query.section;
  const hit = SECTIONS.find((sec) => sec.key === q);
  return hit ? hit.key : SECTIONS[0]!.key;
}

function initTab(): string {
  const sec =
    SECTIONS.find((s) => s.key === activeSection.value) ?? SECTIONS[0]!;
  const q = route.query.tab;
  if (typeof q === 'string' && sec.tabs.some((t) => t.key === q)) {
    return q;
  }
  return sec.tabs[0]!.key;
}

const sectionTabs = computed<AdminTab[]>(
  () =>
    (SECTIONS.find((s) => s.key === activeSection.value) ?? SECTIONS[0]!).tabs,
);

function onSectionChange(key: string) {
  activeSection.value = key;
  const first = sectionTabs.value[0]!.key;
  activeTab.value = first;
  router.replace({ query: { ...route.query, section: key, tab: first } });
}

function onTabChange(key: number | string) {
  activeTab.value = String(key);
  router.replace({
    query: { ...route.query, section: activeSection.value, tab: String(key) },
  });
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
      <div class="flex flex-col gap-3 lg:flex-row">
        <a-menu
          :selected-keys="[activeSection]"
          class="lg:w-44!"
          mode="inline"
          @click="
            (info: { key: number | string }) =>
              onSectionChange(String(info.key))
          "
        >
          <a-menu-item v-for="sec in SECTIONS" :key="sec.key">
            {{ sec.label }}
            <span class="text-muted-foreground ml-1 text-xs">
              {{ sec.tabs.length }}
            </span>
          </a-menu-item>
        </a-menu>
        <a-tabs
          :active-key="activeTab"
          class="min-w-0 flex-1"
          @change="onTabChange"
        >
          <a-tab-pane
            key="im"
            v-if="activeSection === 'identity'"
            tab="登录配置"
          >
            <ImConfig />
          </a-tab-pane>
          <a-tab-pane
            key="session"
            v-if="activeSection === 'identity'"
            tab="会话设置"
          >
            <Session />
          </a-tab-pane>
          <a-tab-pane
            key="notify"
            v-if="activeSection === 'notify'"
            tab="通知群聊"
          >
            <NotifyGroups />
          </a-tab-pane>
          <a-tab-pane
            key="notify-routes"
            v-if="activeSection === 'notify'"
            tab="通知路由"
          >
            <NotifyRoutes />
          </a-tab-pane>
          <a-tab-pane
            key="alert-templates"
            v-if="activeSection === 'notify'"
            tab="告警模板"
          >
            <AlertTemplates />
          </a-tab-pane>
          <a-tab-pane key="ai" v-if="activeSection === 'ai'" tab="AI 中转层">
            <AiConfig />
          </a-tab-pane>
          <a-tab-pane key="mcp" v-if="activeSection === 'ai'" tab="MCP 接入">
            <McpTokens />
          </a-tab-pane>
          <a-tab-pane
            key="pull-tokens"
            v-if="activeSection === 'delivery'"
            tab="配置拉取"
          >
            <PullTokens />
          </a-tab-pane>
          <a-tab-pane key="skills" v-if="activeSection === 'ai'" tab="AI 技能">
            <AiSkills />
          </a-tab-pane>
          <a-tab-pane
            key="k3s"
            v-if="activeSection === 'delivery'"
            tab="k3s 集群"
          >
            <K3sClusters />
          </a-tab-pane>
          <a-tab-pane
            key="ci"
            v-if="activeSection === 'delivery'"
            tab="CI / 镜像仓库"
          >
            <CiConfig />
          </a-tab-pane>
        </a-tabs>
      </div>
    </a-card>
  </div>
</template>
