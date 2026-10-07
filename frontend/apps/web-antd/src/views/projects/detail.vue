<script lang="ts" setup>
import type { EnvStatus, EnvTarget, Project } from '#/api/projects';

import { computed, onMounted, ref } from 'vue';
import { useRoute, useRouter } from 'vue-router';

import { getProjectApi } from '#/api/projects';

defineOptions({ name: 'ProjectDetail' });

const route = useRoute();
const router = useRouter();
const projectId = computed(() => Number(route.params.id));

const project = ref<null | Project>(null);
const targets = ref<EnvTarget[]>([]);
const loading = ref(false);

const envs: Array<{ key: 'canary' | 'prod' | 'test'; label: string }> = [
  { key: 'prod', label: '正式' },
  { key: 'canary', label: '灰度' },
  { key: 'test', label: '测试' },
];

const statusText: Record<string, string> = {
  failed: '失败',
  running: '进行中',
  success: '正常',
  timeout: '超时',
};

async function load() {
  loading.value = true;
  try {
    const res = await getProjectApi(projectId.value);
    project.value = res.project;
    targets.value = res.targets ?? [];
  } catch {
    // 拦截器已提示
  } finally {
    loading.value = false;
  }
}

function stat(env: 'canary' | 'prod' | 'test'): EnvStatus | null {
  return project.value?.envs?.find((e) => e.envType === env) ?? null;
}

function targetOf(env: string): EnvTarget | undefined {
  return targets.value.find((t) => t.envType === env);
}

const runtimeText: Record<string, string> = {
  compose: 'compose 主机',
  k3s: 'k3s 集群',
};

onMounted(load);
</script>

<template>
  <div class="p-4">
    <a-card :loading="loading">
      <template #title>
        <a class="text-base" @click="router.push('/projects')"> ← 项目 </a>
        <span class="ml-2">{{ project?.name ?? '…' }}</span>
        <span class="text-muted-foreground ml-2 text-xs font-normal">
          {{ project?.repoPath }}（{{ project?.provider || 'gitea' }}）
        </span>
      </template>
      <template #extra>
        <a-button
          size="small"
          @click="router.push(`/envs/prod?project=${project?.id ?? projectId}`)"
        >
          环境操作
        </a-button>
      </template>

      <!-- 三环境聚合：状态 + 部署目标（独立审核第 2 批：项目是主对象，
           环境是子视角——不再分别进三个环境页拼凑全景） -->
      <div class="grid grid-cols-1 gap-4 md:grid-cols-3">
        <a-card v-for="env in envs" :key="env.key" size="small">
          <template #title>
            {{ env.label }}
            <a-tag
              v-if="env.key === 'prod' && stat(env.key)?.activeColor"
              :color="stat(env.key)?.activeColor === 'blue' ? 'blue' : 'green'"
              class="ml-1"
            >
              {{ stat(env.key)?.activeColor }}
            </a-tag>
          </template>
          <div v-if="stat(env.key)">
            <a-badge
              :status="
                stat(env.key)?.lastStatus === 'success'
                  ? 'success'
                  : stat(env.key)?.lastStatus === 'running'
                    ? 'processing'
                    : 'error'
              "
              :text="statusText[stat(env.key)!.lastStatus ?? ''] ?? '—'"
            />
            <div class="text-muted-foreground mt-1 text-xs">
              {{ stat(env.key)?.lastTag || '未发布' }} ·
              {{
                stat(env.key)?.lastReleaseAt
                  ? new Date(stat(env.key)!.lastReleaseAt!).toLocaleString()
                  : '—'
              }}
            </div>
          </div>
          <div v-else class="text-muted-foreground text-xs">未发布</div>
          <a-divider class="my-2" />
          <div class="text-xs">
            部署目标：
            <span v-if="targetOf(env.key)">
              {{ runtimeText[targetOf(env.key)!.runtime || 'compose'] }}
            </span>
            <span v-else class="text-muted-foreground">未绑定</span>
          </div>
          <a-button
            class="mt-2"
            size="small"
            type="link"
            @click="router.push(`/envs/${env.key}?project=${projectId}`)"
          >
            进入{{ env.label }}环境 →
          </a-button>
        </a-card>
      </div>
    </a-card>
  </div>
</template>
