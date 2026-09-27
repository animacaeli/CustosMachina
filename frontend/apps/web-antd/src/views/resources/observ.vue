<script lang="ts" setup>
import type { ObservComponent } from '#/api/observ';

import { computed, onMounted, reactive, ref, watch } from 'vue';

import { useUserStore } from '@vben/stores';

import { message } from 'ant-design-vue';

import {
  deployObservApi,
  getObservListApi,
  getObservStatusApi,
  setO2UrlApi,
  uninstallObservApi,
} from '#/api/observ';
import { getServerListApi } from '#/api/resources/server';
import CodeEditor from '#/components/yaml-editor.vue';

defineOptions({ name: 'ResourcesObserv' });

const userStore = useUserStore();
const canWrite = computed(() => {
  const roles = userStore.userInfo?.roles ?? [];
  return roles.includes('superadmin') || roles.includes('admin') || roles.includes('ops');
});

const loading = ref(false);
const components = ref<ObservComponent[]>([]);
const o2Url = ref('');
const servers = ref<{ host: string; id: number; name: string }[]>([]);
const serverId = ref<number | undefined>();
const status = ref<Record<string, string>>({});
const acting = ref('');

const categoryMeta: Record<string, { text: string; tip: string }> = {
  metrics: {
    text: '指标采集',
    tip: 'cadvisor 看容器层指标，node-exporter 看宿主机层（CPU/内存/磁盘），可同时部署互为补充',
  },
  logs: {
    text: '日志采集',
    tip: 'vector 功能全（管线可编程：transform 路由/多源多出口），fluent-bit 更轻量省内存，二选一即可',
  },
};

async function loadBase() {
  loading.value = true;
  try {
    const [listRes, serversRes] = await Promise.all([
      getObservListApi(),
      getServerListApi(),
    ]);
    components.value = listRes.components ?? [];
    o2Url.value = listRes.o2Url ?? '';
    servers.value = (serversRes ?? []).map((s: any) => ({
      host: s.host,
      id: s.id,
      name: s.name,
    }));
    if (!serverId.value && servers.value.length > 0) {
      serverId.value = servers.value[0]!.id;
    }
  } finally {
    loading.value = false;
  }
}

async function loadStatus() {
  if (!serverId.value) return;
  try {
    status.value = await getObservStatusApi(serverId.value);
  } catch {
    status.value = {};
  }
}

onMounted(async () => {
  await loadBase();
  await loadStatus();
});

watch(serverId, loadStatus);

async function saveO2Url() {
  try {
    await setO2UrlApi(o2Url.value.trim());
    message.success('O2 地址已保存');
  } catch {
    // URL 校验等业务错误由拦截器提示
  }
}

// ---- 部署弹窗（模板可编辑：compose + 伴随配置） ----
const deployOpen = ref(false);
const deployTarget = ref<null | ObservComponent>(null);
const deployForm = reactive({
  compose: '',
  configs: {} as Record<string, string>,
});
const deploying = ref(false);
const configNames = computed(() => Object.keys(deployForm.configs));

function openDeploy(comp: ObservComponent) {
  if (comp.needsO2Url && !o2Url.value) {
    message.warning('请先配置 O2 日志地址（该组件的输出目标）');
    return;
  }
  deployTarget.value = comp;
  deployForm.compose = comp.compose;
  deployForm.configs = {};
  for (const cf of comp.configFiles ?? []) {
    deployForm.configs[cf.filename] = cf.content;
  }
  deployOpen.value = true;
}

async function doDeploy() {
  if (!serverId.value || !deployTarget.value) return;
  deploying.value = true;
  try {
    const res = await deployObservApi({
      compose: deployForm.compose,
      component: deployTarget.value.name,
      configFiles: deployForm.configs,
      serverId: serverId.value,
    });
    message.success(`${deployTarget.value.name} 部署/升级完成`);
    if (res.output) {
      console.log('[observ]', res.output);
    }
    deployOpen.value = false;
    await loadStatus();
  } catch {
    // 部署失败由拦截器提示
  } finally {
    deploying.value = false;
  }
}

async function onUninstall(comp: ObservComponent) {
  if (!serverId.value) return;
  acting.value = comp.name;
  try {
    await uninstallObservApi(serverId.value, comp.name);
    message.success(`${comp.name} 已卸载`);
    await loadStatus();
  } catch {
    // 卸载失败由拦截器提示
  } finally {
    acting.value = '';
  }
}

const statusMeta: Record<string, { color: string; text: string }> = {
  absent: { color: 'default', text: '未部署' },
  running: { color: 'green', text: '运行中' },
  stopped: { color: 'red', text: '已停止' },
};

function configLanguage(filename: string) {
  if (filename.endsWith('.yaml') || filename.endsWith('.yml')) return 'yaml';
  if (filename.endsWith('.json')) return 'json';
  return 'ini'; // fluent-bit.conf 等
}
</script>

<template>
  <div class="p-4">
    <a-card title="观测组件">
      <div class="mb-4 flex flex-wrap items-center gap-2">
        <span>目标主机：</span>
        <a-select
          v-model:value="serverId"
          :options="
            servers.map((s) => ({ label: `${s.name}（${s.host}）`, value: s.id }))
          "
          style="width: 280px"
        />
        <span class="ml-4">O2 日志地址：</span>
        <a-input
          v-model:value="o2Url"
          :disabled="!canWrite"
          placeholder="http://<o2>:5080/api/<org>/<stream>/_json（可内嵌 user:pass）"
          style="width: 380px"
        />
        <a-button v-if="canWrite" :disabled="!o2Url" @click="saveO2Url">
          保存
        </a-button>
      </div>
      <a-alert
        class="mb-4"
        type="info"
        show-icon
        message="部署前可编辑 compose 与采集器配置（Monaco 编辑器，含语法校验）；升级 = 改完模板重新部署。k3s 期模板将翻译为 DaemonSet。"
      />
      <a-row :gutter="[16, 16]">
        <a-col v-for="comp in components" :key="comp.name" :span="12">
          <a-card size="small" :loading="loading">
            <template #title>
              {{ comp.name }}
              <a-tag class="ml-2">{{ categoryMeta[comp.category]?.text }}</a-tag>
            </template>
            <template #extra>
              <a-tag :color="statusMeta[status[comp.name] ?? 'absent']?.color">
                {{ statusMeta[status[comp.name] ?? 'absent']?.text ?? '未知' }}
              </a-tag>
            </template>
            <p class="mb-2 text-sm whitespace-pre-line text-gray-500">
              {{ comp.remark }}
            </p>
            <p class="mb-4">
              <code class="text-xs">{{ comp.image }}</code>
            </p>
            <a-space v-if="canWrite">
              <a-button
                type="primary"
                size="small"
                :loading="acting === comp.name"
                :disabled="comp.needsO2Url && !o2Url"
                @click="openDeploy(comp)"
              >
                {{ status[comp.name] === 'running' ? '编辑并升级' : '部署' }}
              </a-button>
              <a-popconfirm
                :title="`确认卸载 ${comp.name}？`"
                @confirm="onUninstall(comp)"
              >
                <a-button
                  size="small"
                  danger
                  :disabled="status[comp.name] === 'absent'"
                >
                  卸载
                </a-button>
              </a-popconfirm>
            </a-space>
            <div
              v-if="comp.needsO2Url && !o2Url"
              class="mt-2 text-xs text-orange-500"
            >
              需先配置 O2 日志地址
            </div>
          </a-card>
        </a-col>
      </a-row>
    </a-card>

    <a-modal
      v-model:open="deployOpen"
      :title="`部署 / 升级：${deployTarget?.name ?? ''}`"
      :width="820"
      :confirm-loading="deploying"
      ok-text="部署"
      @ok="doDeploy"
    >
      <a-alert
        class="mb-3"
        type="info"
        show-icon
        :message="categoryMeta[deployTarget?.category ?? 'metrics']?.tip"
      />
      <div class="mb-1 text-xs text-gray-500">
        docker-compose.yml（容器定义，可加挂载/环境变量/资源限制等）
      </div>
      <CodeEditor
        v-model="deployForm.compose"
        height="240px"
        language="yaml"
        schema="compose"
      />
      <template v-for="fname in configNames" :key="fname">
        <div class="mt-3 mb-1 text-xs text-gray-500">
          {{ fname }}（{{ deployTarget?.name }} 配置，可定制管线/过滤规则）
        </div>
        <CodeEditor
          v-model="deployForm.configs[fname]!"
          :height="configLanguage(fname) === 'yaml' ? '260px' : '200px'"
          :language="configLanguage(fname)"
        />
      </template>
    </a-modal>
  </div>
</template>
