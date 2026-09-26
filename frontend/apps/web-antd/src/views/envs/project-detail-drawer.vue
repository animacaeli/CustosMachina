<script lang="ts" setup>
import type { EnvTarget, NotifyGroup, Project } from '#/api/projects';
import type { ManagedServer } from '#/api/resources/server';

import { computed, reactive, ref, watch } from 'vue';

import { useUserStore } from '@vben/stores';

import { message } from 'ant-design-vue';

import {
  getNotifyGroupsApi,
  getProjectApi,
  saveProjectTargetsApi,
  updateProjectApi,
} from '#/api/projects';
import { getServerListApi } from '#/api/resources/server';

import ContainersTab from './containers-tab.vue';

defineOptions({ name: 'ProjectDetailDrawer' });

const props = withDefaults(
  defineProps<{
    env?: 'canary' | 'prod' | 'test';
    open: boolean;
    projectId: number | undefined;
  }>(),
  { env: 'prod' },
);

const emit = defineEmits<{ 'update:open': [boolean]; changed: [] }>();

const project = ref<null | Project>(null);
const targets = ref<EnvTarget[]>([]);
const groups = ref<NotifyGroup[]>([]);
const servers = ref<ManagedServer[]>([]);
const loading = ref(false);
const activeTab = ref('config');

async function load() {
  if (!props.projectId) return;
  loading.value = true;
  try {
    const [detail, gs, ss] = await Promise.all([
      getProjectApi(props.projectId),
      getNotifyGroupsApi(),
      getServerListApi(),
    ]);
    project.value = detail.project;
    targets.value = detail.targets;
    groups.value = gs;
    servers.value = ss;
    if (project.value) {
      fillConfig(project.value);
      fillTargets(targets.value);
    }
  } finally {
    loading.value = false;
  }
}

watch(
  () => [props.open, props.projectId],
  ([open]) => open && load(),
);

// ---- 配置表单 ----
const config = reactive({
  ciToken: '',
  composePath: '',
  defaultBranch: 'main',
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
  config.notifyProdGroupId =
    p.notifyProdGroupId ?? p.notifyCanaryGroupId ?? undefined;
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
  if (!project.value || !props.projectId) return;
  configSaving.value = true;
  try {
    project.value = await updateProjectApi(props.projectId, {
      ciToken: config.ciToken || undefined,
      composePath: config.composePath,
      defaultBranch: config.defaultBranch,
      name: project.value.name,
      notifyOnSuccess: config.notifyOnSuccess,
      notifyProdGroupId: config.notifyProdGroupId ?? null,
      notifyTestGroupId: config.notifyTestGroupId ?? null,
      repoPath: project.value.repoPath,
      repoUrl: project.value.repoUrl,
      slotGraceDays: config.slotGraceDays,
      testSlotCount: config.testSlotCount,
      trafficCap: config.trafficCap,
    });
    if (deployServerId.value) {
      await saveProjectTargetsApi(props.projectId, {
        targets: (['prod', 'canary', 'test'] as const).map((envType) => ({
          envType,
          runtime: 'compose' as const,
          serverId: deployServerId.value as number,
        })),
      });
    }
    message.success('配置已保存');
    emit('changed');
  } finally {
    configSaving.value = false;
  }
}

// ---- 部署主机 ----
// 部署的编排细节在流水线/compose 文件里；平台只需要知道部署到哪台主机（SSH 凭据）。
// 三个环境通常同机（compose 期隔离域靠项目名前缀），UI 简化为一个下拉，保存时三环境同值。
const deployServerId = ref<number | undefined>();

function fillTargets(ts: EnvTarget[]) {
  deployServerId.value = ts[0]?.serverId;
}

function close() {
  emit('update:open', false);
}
</script>

<template>
  <a-drawer
    :open="open"
    :title="`项目：${project?.name ?? '…'}`"
    :width="980"
    @close="close"
  >
    <a-spin :spinning="loading">
      <div class="text-muted-foreground mb-3 text-xs">
        {{ project?.repoUrl }}
      </div>
      <a-tabs v-model:active-key="activeTab">
        <a-tab-pane key="config" tab="配置">
          <a-form layout="vertical" style="max-width: 42rem">
            <a-divider orientation="left" plain>CI / 部署</a-divider>
            <a-form-item
              label="部署主机"
              extra="正式 / 灰度 / 测试共用（隔离域靠项目名前缀区分）；编排细节在仓库的 compose 文件里"
            >
              <a-select
                v-model:value="deployServerId"
                :options="
                  servers.map((s) => ({
                    label: `${s.name}（${s.host}）`,
                    value: s.id,
                  }))
                "
                allow-clear
                placeholder="未配置（发布/槽位不可用）"
                show-search
                style="width: 360px"
              />
            </a-form-item>
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
            <a-form-item
              label="生产环境通知群"
              extra="正式与灰度共用同一通知群（本质是同一个生产环境）"
            >
              <a-select
                v-model:value="config.notifyProdGroupId"
                :options="
                  prodGroups.map((g) => ({ label: g.name, value: g.id }))
                "
                allow-clear
                placeholder="不通知"
                style="width: 320px"
              />
            </a-form-item>
            <a-form-item label="测试环境通知群（测试类）">
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
                  :max="64"
                  :min="0"
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

        <a-tab-pane key="containers" tab="容器 / Pod">
          <ContainersTab v-if="projectId" :env="env" :project-id="projectId" />
        </a-tab-pane>
      </a-tabs>
    </a-spin>
  </a-drawer>
</template>
