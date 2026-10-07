<script lang="ts" setup>
import type { EnvStatus, Project } from '#/api/projects';

import { computed, onMounted, ref } from 'vue';
import { useRouter } from 'vue-router';

import { getProjectsApi } from '#/api/projects';

defineOptions({ name: 'ProjectsOverview' });

const router = useRouter();

const loading = ref(false);
const projects = ref<Project[]>([]);
const keyword = ref('');

const filtered = computed(() => {
  const kw = keyword.value.trim().toLowerCase();
  if (!kw) return projects.value;
  return projects.value.filter(
    (p) =>
      p.name.toLowerCase().includes(kw) ||
      (p.repoPath ?? '').toLowerCase().includes(kw),
  );
});

async function load() {
  loading.value = true;
  try {
    projects.value = await getProjectsApi();
  } catch {
    // 拦截器已提示
  } finally {
    loading.value = false;
  }
}

onMounted(load);

function stat(p: Project, env: 'canary' | 'prod' | 'test'): EnvStatus | null {
  return p.envs?.find((e) => e.envType === env) ?? null;
}

const statusText: Record<string, string> = {
  failed: '失败',
  running: '进行中',
  success: '正常',
  timeout: '超时',
};
const statusBadge: Record<string, string> = {
  failed: 'error',
  running: 'processing',
  success: 'success',
  timeout: 'warning',
};

function goEnv(env: 'canary' | 'prod' | 'test') {
  router.push(`/envs/${env}`);
}
</script>

<template>
  <div class="p-4">
    <a-card>
      <template #title>
        <span class="text-base">项目总览</span>
        <span class="text-muted-foreground ml-2 text-xs font-normal">
          以项目为中心横看三环境；进入各环境页执行构建 / 发布 / 灰度
        </span>
      </template>
      <template #extra>
        <a-input-search
          v-model:value="keyword"
          placeholder="搜索项目 / 仓库"
          style="width: 240px"
        />
      </template>
      <a-table
        :columns="[
          { title: '项目', key: 'name' },
          { title: '正式', key: 'prod', width: 200 },
          { title: '灰度', key: 'canary', width: 180 },
          { title: '测试', key: 'test', width: 180 },
          { title: '操作', key: 'actions', width: 150 },
        ]"
        :data-source="filtered"
        :loading="loading"
        :pagination="false"
        row-key="id"
        size="middle"
      >
        <template #bodyCell="{ column, record }">
          <template v-if="column.key === 'name'">
            <div>
              <router-link :to="`/projects/${record.id}`" class="font-medium">
                {{ record.name }}
              </router-link>
            </div>
            <div class="text-muted-foreground text-xs">
              {{ record.repoPath }}
            </div>
          </template>
          <template v-else-if="column.key === 'prod'">
            <template v-if="stat(record, 'prod')">
              <div class="flex items-center gap-1">
                <a-badge
                  :status="
                    statusBadge[stat(record, 'prod')!.lastStatus] ?? 'default'
                  "
                  :text="statusText[stat(record, 'prod')!.lastStatus] ?? '—'"
                />
                <a-tag
                  v-if="stat(record, 'prod')!.activeColor"
                  :color="
                    stat(record, 'prod')!.activeColor === 'blue'
                      ? 'blue'
                      : 'green'
                  "
                >
                  {{ stat(record, 'prod')!.activeColor }}
                </a-tag>
              </div>
              <div class="text-muted-foreground text-xs">
                {{ stat(record, 'prod')!.lastTag }} ·
                {{
                  stat(record, 'prod')!.lastReleaseAt
                    ? new Date(
                        stat(record, 'prod')!.lastReleaseAt!,
                      ).toLocaleString()
                    : ''
                }}
              </div>
            </template>
            <span v-else class="text-muted-foreground text-xs">未发布</span>
          </template>
          <template v-else-if="column.key === 'canary'">
            <template v-if="stat(record, 'canary')">
              <a-badge
                :status="
                  statusBadge[stat(record, 'canary')!.lastStatus] ?? 'default'
                "
                :text="stat(record, 'canary')!.lastTag || '—'"
              />
            </template>
            <span v-else class="text-muted-foreground text-xs">未发布</span>
          </template>
          <template v-else-if="column.key === 'test'">
            <template v-if="stat(record, 'test')">
              <a-badge
                :status="
                  statusBadge[stat(record, 'test')!.lastStatus] ?? 'default'
                "
                :text="stat(record, 'test')!.lastTag || '—'"
              />
            </template>
            <span v-else class="text-muted-foreground text-xs">未发布</span>
          </template>
          <template v-else-if="column.key === 'actions'">
            <a-button size="small" type="link" @click="goEnv('prod')">
              正式环境
            </a-button>
            <a-button size="small" type="link" @click="goEnv('test')">
              测试环境
            </a-button>
          </template>
        </template>
      </a-table>
    </a-card>
  </div>
</template>
