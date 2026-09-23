<script lang="ts" setup>
import type { Project } from '#/api/projects';

import { onMounted, ref } from 'vue';

import { getProjectsApi } from '#/api/projects';

import BuildDrawer from './build-drawer.vue';

defineOptions({ name: 'EnvsIndex' });

const projects = ref<Project[]>([]);
const projectId = ref<number | undefined>();
const activeEnv = ref<'canary' | 'prod' | 'test'>('prod');

const envTabs = [
  { key: 'prod', tab: '正式环境' },
  { key: 'canary', tab: '灰度环境' },
  { key: 'test', tab: '测试环境' },
] as const;

const buildOpen = ref(false);

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
          <template v-else>
            <div class="mb-4 flex gap-2">
              <a-button type="primary" @click="buildOpen = true">构建</a-button>
              <a-button disabled>发布</a-button>
              <a-button v-if="t.key !== 'prod'" disabled>
                {{ t.key === 'canary' ? '策略' : '槽位' }}
              </a-button>
            </div>
            <a-empty
              description="发布 / 策略 / 槽位随后续里程碑（M3~M5）交付；构建记录见「构建」抽屉"
            />
          </template>
        </a-tab-pane>
      </a-tabs>
    </a-card>

    <BuildDrawer
      :env="activeEnv"
      :open="buildOpen"
      :project-id="projectId"
      @close="buildOpen = false"
    />
  </div>
</template>
