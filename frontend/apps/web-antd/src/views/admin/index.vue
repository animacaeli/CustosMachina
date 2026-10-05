<script lang="ts" setup>
import { ref } from 'vue';

import AiConfig from './tabs/ai-config.vue';
import AiSkills from './tabs/ai-skills.vue';
import AlertTemplates from './tabs/alert-templates.vue';
import CiConfig from './tabs/ci-config.vue';
import ImConfig from './tabs/im-config.vue';
import McpTokens from './tabs/mcp-tokens.vue';
import NotifyGroups from './tabs/notify-groups.vue';
import NotifyRoutes from './tabs/notify-routes.vue';
import PullTokens from './tabs/pull-tokens.vue';
import Session from './tabs/session.vue';

defineOptions({ name: 'AdminConsole' });

const activeTab = ref('im');
</script>

<template>
  <div class="p-4">
    <a-card>
      <template #title>
        <span class="text-base">管理后台</span>
        <span class="text-muted-foreground ml-2 text-xs font-normal">
          平台级配置中心（组件纳管 / AI 配置等随后续版本加入）
        </span>
      </template>
      <a-tabs v-model:active-key="activeTab">
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
        <a-tab-pane key="pull-tokens" tab="配置中心">
          <PullTokens />
        </a-tab-pane>
        <a-tab-pane key="skills" tab="AI 技能">
          <AiSkills />
        </a-tab-pane>
        <a-tab-pane key="ci" tab="CI / 镜像仓库">
          <CiConfig />
        </a-tab-pane>
      </a-tabs>
    </a-card>
  </div>
</template>
