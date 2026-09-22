<script lang="ts" setup>
import { ref } from 'vue';

import ImConfig from './tabs/im-config.vue';
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
      </a-tabs>
    </a-card>
  </div>
</template>
