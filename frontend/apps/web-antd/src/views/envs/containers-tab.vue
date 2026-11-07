<script lang="ts" setup>
import type { EnvTarget, Project } from '#/api/projects';
import type { ProjectContainer } from '#/api/release';
import type { ManagedServer } from '#/api/resources/server';

import { computed, onMounted, ref, watch } from 'vue';

import { message } from 'ant-design-vue';

import { getProjectApi } from '#/api/projects';
import {
  getContainerLogsApi,
  getServerContainersApi,
  scaleComposeApi,
} from '#/api/release';
import { getServerListApi } from '#/api/resources/server';

defineOptions({ name: 'ProjectContainersTab' });

const props = defineProps<{
  env: 'canary' | 'prod' | 'test';
  projectId: number;
}>();

const project = ref<null | Project>(null);
const targets = ref<EnvTarget[]>([]);
const servers = ref<ManagedServer[]>([]);
// 环境由打开方决定（在哪个环境页进入详情就只看哪个环境），不提供切换
const env = computed(() => props.env);
const loading = ref(false);
const containers = ref<ProjectContainer[]>([]);

// 部署前缀由后端下发（project.deployPrefix=<norm项目名>-）——命名规则只在
// 后端实现一处，防前后端漂移。容器匹配：prod/canary 带 <proj>-<env>- 段；
// test 域 2026-11-08 命名统一后为 <proj>-<slot>（无环境段），匹配项目前缀后
// 排除 prod-/canary- 段（否则三个隔离域混在一起）
const deployPrefix = computed(() => project.value?.deployPrefix ?? 'project-');

function matchContainer(name: string) {
  const prefix = deployPrefix.value;
  if (!name.startsWith(prefix)) return false;
  if (env.value === 'test') {
    const rest = name.slice(prefix.length);
    return !rest.startsWith('prod-') && !rest.startsWith('canary-');
  }
  return name.startsWith(`${prefix}${env.value}-`);
}

const targetServer = computed(() => {
  const t = targets.value.find((x) => x.envType === env.value);
  return servers.value.find((s) => s.id === t?.serverId);
});

// 一行一容器（2026-11-08 命名统一：容器名/状态/镜像分列）
// 镜像列只显示 repo:tag（registry 前缀与 namespace 去掉，减少噪音）
function fmtImage(image: string) {
  const parts = image.split('/');
  return parts[parts.length - 1] || image;
}

async function load() {
  const sid = targetServer.value?.id;
  if (!sid) {
    containers.value = [];
    return;
  }
  loading.value = true;
  try {
    const all = await getServerContainersApi(sid);
    containers.value = all.filter((ct) => matchContainer(ct.name));
  } catch {
    containers.value = [];
  } finally {
    loading.value = false;
  }
}

async function init() {
  const [detail, ss] = await Promise.all([
    getProjectApi(props.projectId),
    getServerListApi(),
  ]);
  project.value = detail.project;
  targets.value = detail.targets;
  servers.value = ss;
  await load();
}

onMounted(init);
// 换项目必须重拉项目详情（部署前缀/目标主机随之变化），否则会按旧项目过滤
watch([env, () => props.projectId], async () => {
  await init().catch((error) => console.warn('[init]', error));
});

// ---- 日志查看（tail 模式，刷新按钮重拉；SSE 跟随后置） ----
const logOpen = ref(false);
const logLines = ref<string[]>([]);
const logTitle = ref('');
const logLoading = ref(false);

async function openLogs(ct: ProjectContainer) {
  const sid = targetServer.value?.id;
  if (!sid) return;
  logTitle.value = `${ct.name}（${ct.composeService ?? '单容器'}）`;
  logOpen.value = true;
  logLoading.value = true;
  try {
    logLines.value = await getContainerLogsApi(sid, ct.id);
  } finally {
    logLoading.value = false;
  }
}

// ---- 实例伸缩 ----
const scaleOpen = ref(false);
// 记录当前行容器的 compose 标签（多槽位/多服务时定位准确，不能取列表首个）
const scaleForm = ref({
  composeFile: '',
  composeProject: '',
  replicas: 1,
  service: '',
});
const scaling = ref(false);

function openScale(ct: ProjectContainer) {
  const count = containers.value.filter(
    (c) => (c.composeService || c.name) === (ct.composeService || ct.name),
  ).length;
  scaleForm.value = {
    composeFile: ct.composeFile ?? '',
    composeProject: ct.composeProject ?? '',
    replicas: Math.max(count, 1),
    service: ct.composeService || ct.name,
  };
  scaleOpen.value = true;
}

async function doScale() {
  const sid = targetServer.value?.id;
  const f = scaleForm.value;
  if (!sid || !f.composeProject || !f.composeFile) {
    message.warning('缺少 compose 标签信息（非 compose 部署的服务不支持伸缩）');
    return;
  }
  scaling.value = true;
  try {
    await scaleComposeApi(sid, {
      path: f.composeFile,
      project: f.composeProject,
      replicas: f.replicas,
      service: f.service,
    });
    message.success('伸缩完成');
    scaleOpen.value = false;
    await load();
  } catch {
    // 拦截器已提示（含 compose 输出）
  } finally {
    scaling.value = false;
  }
}
</script>

<template>
  <div>
    <div class="mb-4 flex flex-wrap items-center gap-3">
      <span v-if="targetServer" class="text-muted-foreground text-xs">
        部署目标：{{ targetServer.name }}（{{ targetServer.host }}）· 前缀
        {{ deployPrefix }}
      </span>
      <span v-else class="text-xs text-orange-500">该环境未配置部署目标</span>
      <a-button :disabled="!targetServer" size="small" @click="load">
        刷新
      </a-button>
    </div>

    <a-table
      :data-source="containers"
      :loading="loading"
      :pagination="false"
      row-key="id"
      size="middle"
    >
      <a-table-column key="name" title="容器名" :width="280">
        <template #default="{ record }">
          <span class="text-xs">{{ record.name }}</span>
        </template>
      </a-table-column>
      <a-table-column key="state" title="状态" :width="100">
        <template #default="{ record }">
          <a-tag
            :color="
              record.state === 'running' ? 'green' : record.state === 'restarting' ? 'orange' : 'red'
            "
            class="w-20 text-center"
          >
            {{ record.state }}
          </a-tag>
        </template>
      </a-table-column>
      <a-table-column key="image" title="镜像" :width="220">
        <template #default="{ record }">
          <span class="text-muted-foreground text-xs">
            {{ fmtImage(record.image) }}
          </span>
        </template>
      </a-table-column>
      <a-table-column key="service" title="服务" :width="120">
        <template #default="{ record }">
          <span class="text-muted-foreground text-xs">
            {{ record.composeService ?? '-' }}
          </span>
        </template>
      </a-table-column>
      <a-table-column :width="200" key="action" title="操作">
        <template #default="{ record }">
          <a-button size="small" type="link" @click="openLogs(record)">
            日志
          </a-button>
          <a-button
            size="small"
            type="link"
            @click="openScale(record)"
          >
            实例数
          </a-button>
        </template>
      </a-table-column>
    </a-table>

    <a-drawer
      :open="logOpen"
      :title="logTitle"
      :width="900"
      @close="logOpen = false"
    >
      <a-spin :spinning="logLoading">
        <pre
          class="bg-muted max-h-[70vh] overflow-auto rounded p-3 text-xs leading-5"
          >{{ logLines.join('\n') || '（无日志）' }}</pre>
      </a-spin>
    </a-drawer>

    <a-modal
      v-model:open="scaleOpen"
      :title="`调整 ${scaleForm.service} 实例数`"
      @ok="doScale"
    >
      <a-form layout="vertical" style="padding-top: 0.5rem">
        <a-form-item
          label="实例数（最少 1）"
          extra="compose 服务须无固定容器名 / 端口绑定"
        >
          <a-input-number
            v-model:value="scaleForm.replicas"
            :max="32"
            :min="1"
          />
        </a-form-item>
      </a-form>
    </a-modal>
  </div>
</template>
