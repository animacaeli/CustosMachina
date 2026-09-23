<script lang="ts" setup>
import type { Project } from '#/api/projects';

import { onMounted, ref } from 'vue';

import { getProjectsApi } from '#/api/projects';

defineOptions({ name: 'EnvsIndex' });

const projects = ref<Project[]>([]);
const projectId = ref<number | undefined>();
const activeEnv = ref('prod');

const envTabs = [
  { key: 'prod', tab: '正式环境' },
  { key: 'canary', tab: '灰度环境' },
  { key: 'test', tab: '测试环境' },
];

onMounted(async () => {
  projects.value = await getProjectsApi();
  projectId.value = projects.value[0]?.id;
});
</script>

<template>
  <div class="p-4">
    <a-card>
      <template #title>
        <span class="text-base">环境管理</span>
        <span class="text-muted-foreground ml-2 text-xs font-normal">
          正式 / 灰度 / 测试三环境共用项目上下文
        </span>
      </template>
      <div class="mb-4 flex items-center gap-3">
        <span class="text-sm">当前项目：</span>
        <a-select
          v-model:value="projectId"
          :options="projects.map((p) => ({ label: p.name, value: p.id }))"
          placeholder="暂无项目（先到项目管理创建）"
          show-search
          style="width: 260px"
        />
      </div>
      <a-tabs v-model:active-key="activeEnv">
        <a-tab-pane v-for="t in envTabs" :key="t.key" :tab="t.tab">
          <a-empty v-if="!projectId" description="请选择项目" />
          <a-empty
            v-else
            description="构建 / 发布 / 策略 / 槽位功能随后续里程碑（M2~M5）交付"
          />
        </a-tab-pane>
      </a-tabs>
    </a-card>
  </div>
</template>
