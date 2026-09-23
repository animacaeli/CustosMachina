<script lang="ts" setup>
import type { Project } from '#/api/projects';

import { computed, onMounted, ref } from 'vue';

import { getProjectsApi } from '#/api/projects';

import BuildDrawer from './build-drawer.vue';
import PolicyDrawer from './policy-drawer.vue';
import ReleaseDrawer from './release-drawer.vue';
import SlotDrawer from './slot-drawer.vue';

defineOptions({ name: 'EnvsIndex' });

const projects = ref<Project[]>([]);
const projectId = ref<number | undefined>();
const activeEnv = ref<'canary' | 'prod' | 'test'>('prod');

const envTabs = [
  { key: 'prod', tab: '正式环境' },
  { key: 'canary', tab: '灰度环境' },
  { key: 'test', tab: '测试环境' },
] as const;

// M3 起：正式/灰度发布开放（正式发布仅管理员，后端强制）；策略/槽位 M4/M5
const releaseEnabled: Record<string, boolean> = {
  canary: true,
  prod: true,
  test: false,
};

const buildOpen = ref(false);
const releaseOpen = ref(false);
const policyOpen = ref(false);
const slotOpen = ref(false);

const currentProject = computed(() =>
  projects.value.find((p) => p.id === projectId.value),
);

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
              <a-button
                :disabled="!releaseEnabled[t.key]"
                @click="releaseOpen = true"
              >
                发布
              </a-button>
              <a-button v-if="t.key === 'canary'" @click="policyOpen = true">
                策略
              </a-button>
              <a-button v-if="t.key === 'test'" @click="slotOpen = true">
                槽位
              </a-button>
            </div>
            <a-empty
              description="测试环境为全自动 CI/CD：占用槽位后 push 即自动重建"
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
    <ReleaseDrawer
      :env="activeEnv"
      :open="releaseOpen"
      :project-id="projectId"
      @close="releaseOpen = false"
    />
    <PolicyDrawer
      :open="policyOpen"
      :project="currentProject"
      @close="policyOpen = false"
    />
    <SlotDrawer
      :open="slotOpen"
      :project="currentProject"
      @close="slotOpen = false"
    />
  </div>
</template>
