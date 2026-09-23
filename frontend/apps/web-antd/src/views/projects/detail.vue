<script lang="ts" setup>
import type { EnvTarget, NotifyGroup, Project } from '#/api/projects';
import type { ManagedServer } from '#/api/resources/server';

import { computed, onMounted, reactive, ref, watch } from 'vue';
import { useRoute } from 'vue-router';

import { useUserStore } from '@vben/stores';

import { message } from 'ant-design-vue';

import {
  getNotifyGroupsApi,
  getProjectApi,
  saveProjectTargetsApi,
  updateProjectApi,
} from '#/api/projects';
import { getServerListApi } from '#/api/resources/server';

defineOptions({ name: 'ProjectDetail' });

const route = useRoute();
const projectId = Number(route.params.id);

const project = ref<null | Project>(null);
const targets = ref<EnvTarget[]>([]);
const groups = ref<NotifyGroup[]>([]);
const servers = ref<ManagedServer[]>([]);
const loading = ref(false);

async function load() {
  loading.value = true;
  try {
    const [detail, gs, ss] = await Promise.all([
      getProjectApi(projectId),
      getNotifyGroupsApi(),
      getServerListApi(),
    ]);
    project.value = detail.project;
    targets.value = detail.targets;
    groups.value = gs;
    servers.value = ss;
  } finally {
    loading.value = false;
  }
}

onMounted(load);

// ---- 配置表单 ----
const config = reactive({
  composePath: '',
  defaultBranch: 'main',
  ciToken: '',
  notifyCanaryGroupId: undefined as number | undefined,
  notifyOnSuccess: false,
  notifyProdGroupId: undefined as number | undefined,
  notifyTestGroupId: undefined as number | undefined,
  slotGraceDays: 3,
  testSlotCount: 3,
  trafficCap: 50,
});

const prodGroups = computed(() =>
  groups.value.filter((g) => g.scope === 'prod'),
);
const devGroups = computed(() => groups.value.filter((g) => g.scope === 'dev'));

function fillConfig(p: Project) {
  config.composePath = p.composePath;
  config.defaultBranch = p.defaultBranch;
  config.ciToken = '';
  config.notifyProdGroupId = p.notifyProdGroupId ?? undefined;
  config.notifyCanaryGroupId = p.notifyCanaryGroupId ?? undefined;
  config.notifyTestGroupId = p.notifyTestGroupId ?? undefined;
  config.notifyOnSuccess = p.notifyOnSuccess;
  config.testSlotCount = p.testSlotCount;
  config.trafficCap = p.trafficCap;
  config.slotGraceDays = p.slotGraceDays;
}

const userStore = useUserStore();
const isAdmin = computed(() => {
  const roles = userStore.userInfo?.roles ?? [];
  return roles.includes('superadmin') || roles.includes('admin');
});

const configSaving = ref(false);
async function saveConfig() {
  if (!project.value) return;
  configSaving.value = true;
  try {
    const updated = await updateProjectApi(projectId, {
      ciToken: config.ciToken || undefined,
      composePath: config.composePath,
      defaultBranch: config.defaultBranch,
      name: project.value.name,
      notifyCanaryGroupId: config.notifyCanaryGroupId ?? null,
      notifyOnSuccess: config.notifyOnSuccess,
      notifyProdGroupId: config.notifyProdGroupId ?? null,
      notifyTestGroupId: config.notifyTestGroupId ?? null,
      repoPath: project.value.repoPath,
      repoUrl: project.value.repoUrl,
      slotGraceDays: config.slotGraceDays,
      testSlotCount: config.testSlotCount,
      trafficCap: config.trafficCap,
    });
    project.value = updated;
    message.success('配置已保存');
  } finally {
    configSaving.value = false;
  }
}

// ---- 部署目标 ----
const envLabels: Record<string, string> = {
  canary: '灰度',
  prod: '正式',
  test: '测试',
};
const targetRows = reactive(
  (['prod', 'canary', 'test'] as const).map((envType) => ({
    envType,
    serverId: undefined as number | undefined,
    runtime: 'compose' as 'compose' | 'k3s',
  })),
);

function fillTargets(ts: EnvTarget[]) {
  for (const row of targetRows) {
    const hit = ts.find((t) => t.envType === row.envType);
    row.serverId = hit?.serverId;
    row.runtime = hit?.runtime ?? 'compose';
  }
}

const targetsSaving = ref(false);
async function saveTargets() {
  const rows = targetRows
    .filter((r) => r.serverId)
    .map((r) => ({
      envType: r.envType,
      runtime: r.runtime,
      serverId: r.serverId!,
    }));
  if (rows.length === 0) {
    message.warning('至少配置一个环境的部署目标');
    return;
  }
  targetsSaving.value = true;
  try {
    await saveProjectTargetsApi(projectId, { targets: rows });
    message.success('部署目标已保存');
    await load();
  } finally {
    targetsSaving.value = false;
  }
}

watch(project, (p) => p && (fillConfig(p), fillTargets(targets.value)));
</script>

<template>
  <div class="p-4">
    <a-spin :spinning="loading">
      <a-card>
        <template #title>
          项目：{{ project?.name ?? '…' }}
          <span class="text-muted-foreground ml-2 text-xs font-normal">
            {{ project?.repoPath }}
          </span>
        </template>
        <a-tabs default-active-key="config">
          <a-tab-pane key="config" tab="配置">
            <a-form layout="vertical" class="max-w-3xl">
              <a-divider orientation="left" plain>CI / 部署</a-divider>
              <a-form-item
                label="部署描述文件路径"
                extra="compose 文件在仓库中的路径（发布时按标签 checkout 该文件）"
              >
                <a-input
                  v-model:value="config.composePath"
                  placeholder="deploy/docker-compose.yml"
                />
              </a-form-item>
              <a-form-item label="默认分支">
                <a-input
                  v-model:value="config.defaultBranch"
                  style="width: 240px"
                />
              </a-form-item>
              <a-form-item
                label="项目级 gitea token（可选）"
                extra="留空使用平台全局凭据；填写后覆盖（仅保存时提交）"
              >
                <a-input-password
                  v-model:value="config.ciToken"
                  :placeholder="
                    project?.hasCiToken ? '已配置，留空保留' : '未配置'
                  "
                />
              </a-form-item>

              <a-divider orientation="left" plain>通知</a-divider>
              <a-form-item label="正式环境通知群（【P】前缀）">
                <a-select
                  v-model:value="config.notifyProdGroupId"
                  :options="
                    prodGroups.map((g) => ({ label: g.name, value: g.id }))
                  "
                  allow-clear
                  placeholder="不通知"
                />
              </a-form-item>
              <a-form-item label="灰度环境通知群（【P】前缀）">
                <a-select
                  v-model:value="config.notifyCanaryGroupId"
                  :options="
                    prodGroups.map((g) => ({ label: g.name, value: g.id }))
                  "
                  allow-clear
                  placeholder="不通知"
                />
              </a-form-item>
              <a-form-item label="测试环境通知群（【dev】前缀）">
                <a-select
                  v-model:value="config.notifyTestGroupId"
                  :options="
                    devGroups.map((g) => ({ label: g.name, value: g.id }))
                  "
                  allow-clear
                  placeholder="不通知"
                />
              </a-form-item>
              <a-form-item>
                <a-checkbox v-model:checked="config.notifyOnSuccess">
                  构建成功也通知（默认只报失败）
                </a-checkbox>
              </a-form-item>

              <a-divider orientation="left" plain>环境参数</a-divider>
              <div class="flex flex-wrap gap-8">
                <a-form-item label="测试槽位个数" extra="仅管理员可改">
                  <a-input-number
                    v-model:value="config.testSlotCount"
                    :disabled="!isAdmin"
                    :min="0"
                    :max="64"
                  />
                </a-form-item>
                <a-form-item label="槽位过期宽限（天）">
                  <a-input-number
                    v-model:value="config.slotGraceDays"
                    :max="30"
                    :min="1"
                  />
                </a-form-item>
                <a-form-item label="灰度流量总上限（%）">
                  <a-input-number
                    v-model:value="config.trafficCap"
                    :max="100"
                    :min="1"
                  />
                </a-form-item>
              </div>
              <a-button
                :loading="configSaving"
                type="primary"
                @click="saveConfig"
              >
                保存配置
              </a-button>
            </a-form>
          </a-tab-pane>

          <a-tab-pane key="targets" tab="部署目标">
            <a-table
              :data-source="targetRows"
              :pagination="false"
              row-key="envType"
              size="middle"
            >
              <a-table-column title="环境" :width="100">
                <template #default="{ record }">
                  {{ envLabels[record.envType] }}
                </template>
              </a-table-column>
              <a-table-column title="目标服务器">
                <template #default="{ record }">
                  <a-select
                    v-model:value="record.serverId"
                    :options="
                      servers.map((s) => ({
                        label: `${s.name}（${s.host}）`,
                        value: s.id,
                      }))
                    "
                    allow-clear
                    placeholder="未配置"
                    show-search
                    style="width: 100%"
                  />
                </template>
              </a-table-column>
              <a-table-column :width="160" title="运行时">
                <template #default="{ record }">
                  <a-select
                    v-model:value="record.runtime"
                    :options="[
                      { label: 'docker-compose', value: 'compose' },
                      { label: 'k3s（未开放）', value: 'k3s', disabled: true },
                    ]"
                    style="width: 100%"
                  />
                </template>
              </a-table-column>
            </a-table>
            <div class="mt-4">
              <a-button
                :loading="targetsSaving"
                type="primary"
                @click="saveTargets"
              >
                保存部署目标
              </a-button>
            </div>
          </a-tab-pane>

          <a-tab-pane key="containers" tab="容器 / Pod">
            <a-empty
              description="第三阶段 M3 提供项目实例视图（列表 / 日志 / 伸缩）"
            />
          </a-tab-pane>
        </a-tabs>
      </a-card>
    </a-spin>
  </div>
</template>
