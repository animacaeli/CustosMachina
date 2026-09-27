<script lang="ts" setup>
import type { ObservComponent } from '#/api/observ';

import { computed, onMounted, ref, watch } from 'vue';

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

async function onDeploy(comp: ObservComponent) {
  if (!serverId.value) return;
  acting.value = comp.name;
  try {
    const res = await deployObservApi(serverId.value, comp.name);
    message.success(`${comp.name} 部署/升级完成`);
    if (res.output) {
      console.log('[observ]', res.output);
    }
    await loadStatus();
  } catch {
    // 部署失败由拦截器提示
  } finally {
    acting.value = '';
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
          placeholder="http://<o2>:5080/api/<org>/<stream>/_json"
          style="width: 360px"
        />
        <a-button v-if="canWrite" :disabled="!o2Url" @click="saveO2Url">
          保存
        </a-button>
      </div>
      <a-alert
        class="mb-4"
        type="info"
        show-icon
        message="cadvisor 半弃维护（版本固定），k3s 期由 kubelet 内置 cAdvisor 淘汰；vector 采集容器日志输出到上方 O2 地址。升级 = 换模板重新部署。"
      />
      <a-row :gutter="16">
        <a-col v-for="comp in components" :key="comp.name" :span="12">
          <a-card size="small" :title="comp.name" :loading="loading">
            <template #extra>
              <a-tag :color="statusMeta[status[comp.name] ?? 'absent']?.color">
                {{ statusMeta[status[comp.name] ?? 'absent']?.text ?? '未知' }}
              </a-tag>
            </template>
            <p class="mb-2 text-sm text-gray-500">{{ comp.remark }}</p>
            <p class="mb-4">
              <code class="text-xs">{{ comp.image }}</code>
            </p>
            <a-space v-if="canWrite">
              <a-button
                type="primary"
                size="small"
                :loading="acting === comp.name"
                :disabled="comp.needsO2Url && !o2Url"
                @click="onDeploy(comp)"
              >
                {{ status[comp.name] === 'running' ? '升级' : '部署' }}
              </a-button>
              <a-popconfirm
                :title="`确认卸载 ${comp.name}？`"
                @confirm="onUninstall(comp)"
              >
                <a-button size="small" danger :disabled="status[comp.name] === 'absent'">
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
  </div>
</template>
