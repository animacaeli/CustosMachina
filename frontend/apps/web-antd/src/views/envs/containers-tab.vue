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

// 部署名 = <normalizeName(project.name)>-<env>（与后端 release.DeployComposeTo 一致）
const deployPrefix = computed(() => {
  const raw = (project.value?.name ?? '')
    .toLowerCase()
    .trim()
    .replaceAll(' ', '-');
  const norm = [...raw]
    .map((ch) => (/[a-z0-9_.-]/.test(ch) ? ch : '-'))
    .join('');
  return `${norm || 'project'}-`;
});

const targetServer = computed(() => {
  const t = targets.value.find((x) => x.envType === env.value);
  return servers.value.find((s) => s.id === t?.serverId);
});

// 按服务聚合（server + worker 各服务及其实例数）
const services = computed(() => {
  const map = new Map<
    string,
    { containers: ProjectContainer[]; service: string }
  >();
  for (const ct of containers.value) {
    const svc = ct.composeService || ct.name;
    if (!map.has(svc)) {
      map.set(svc, { service: svc, containers: [] });
    }
    map.get(svc)!.containers.push(ct);
  }
  return [...map.values()];
});

async function load() {
  const sid = targetServer.value?.id;
  if (!sid) {
    containers.value = [];
    return;
  }
  loading.value = true;
  try {
    const all = await getServerContainersApi(sid);
    containers.value = all.filter((ct) => {
      const key = ct.composeProject ?? ct.name;
      return key.startsWith(deployPrefix.value);
    });
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
watch([env, () => props.projectId], () => void load());

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
const scaleForm = ref({ service: '', replicas: 1 });
const scaling = ref(false);

function openScale(service: string, current: number) {
  scaleForm.value = { service, replicas: current };
  scaleOpen.value = true;
}

async function doScale() {
  const sid = targetServer.value?.id;
  const ct = containers.value.find(
    (c) => (c.composeService || c.name) === scaleForm.value.service,
  );
  if (!sid || !ct?.composeProject || !ct?.composeFile) {
    message.warning('缺少 compose 标签信息（非 compose 部署的服务不支持伸缩）');
    return;
  }
  scaling.value = true;
  try {
    await scaleComposeApi(sid, {
      path: ct.composeFile,
      project: ct.composeProject,
      replicas: scaleForm.value.replicas,
      service: scaleForm.value.service,
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
      :data-source="services"
      :loading="loading"
      :pagination="false"
      row-key="service"
      size="middle"
    >
      <a-table-column title="服务" :width="180">
        <template #default="{ record }">
          {{ record.service }}
        </template>
      </a-table-column>
      <a-table-column title="实例" :width="90">
        <template #default="{ record }">
          {{ record.containers.length }}
        </template>
      </a-table-column>
      <a-table-column title="镜像 / 状态" key="detail">
        <template #default="{ record }">
          <div
            v-for="ct in record.containers"
            :key="ct.id"
            class="flex items-center gap-2"
          >
            <a-tag
              :color="ct.state === 'running' ? 'green' : 'red'"
              class="w-16 text-center"
            >
              {{ ct.state }}
            </a-tag>
            <span class="text-xs">{{ ct.name }}</span>
            <span class="text-muted-foreground text-xs">{{ ct.image }}</span>
          </div>
        </template>
      </a-table-column>
      <a-table-column :width="200" key="action" title="操作">
        <template #default="{ record }">
          <a-button
            :disabled="record.containers.length === 0"
            size="small"
            type="link"
            @click="openLogs(record.containers[0])"
          >
            日志
          </a-button>
          <a-button
            size="small"
            type="link"
            @click="openScale(record.service, record.containers.length)"
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
