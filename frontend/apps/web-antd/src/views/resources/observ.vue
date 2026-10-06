<script lang="ts" setup>
import type { ObservComponent } from '#/api/observ';

import { computed, onMounted, reactive, ref, watch } from 'vue';

import { useUserStore } from '@vben/stores';

import { message, Modal } from 'ant-design-vue';

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
  return (
    roles.includes('superadmin') ||
    roles.includes('admin') ||
    roles.includes('ops')
  );
});

const loading = ref(false);
const components = ref<ObservComponent[]>([]);
const o2Url = ref('');
const servers = ref<{ host: string; id: number; name: string }[]>([]);
/** 多主机：部署/卸载对全部选中主机执行（一键铺开，改配置不用一台台点） */
const serverIds = ref<number[]>([]);
/** 组件名 → 各主机状态聚合（"运行中 2/3"） */
const status = ref<Record<string, string>>({});
/** 组件名 → 分主机明细（悬停看每台状态） */
const statusDetail = ref<Record<string, Record<number, string>>>({});
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
    if (serverIds.value.length === 0 && servers.value.length > 0) {
      serverIds.value = servers.value.map((srv) => srv.id); // 默认全选
    }
  } finally {
    loading.value = false;
  }
}

async function loadStatus() {
  if (serverIds.value.length === 0) return;
  const detail: Record<string, Record<number, string>> = {};
  const agg: Record<string, string> = {};
  const names = components.value.map((c) => c.name);
  await Promise.all(
    serverIds.value.map(async (sid) => {
      try {
        const st = await getObservStatusApi(sid);
        for (const n of names) {
          detail[n] = detail[n] ?? {};
          detail[n][sid] = st[n] ?? 'absent';
        }
      } catch {
        for (const n of names) {
          detail[n] = detail[n] ?? {};
          detail[n][sid] = 'unknown';
        }
      }
    }),
  );
  // 聚合：全部 running 才 running；全部 absent 才 absent；否则 分数
  for (const n of names) {
    const vals = Object.values(detail[n] ?? {});
    const running = vals.filter((v) => v === 'running').length;
    if (vals.length > 0 && running === vals.length) agg[n] = 'running';
    else if (running === 0 && vals.every((v) => v === 'absent'))
      agg[n] = 'absent';
    else agg[n] = `partial:${running}/${vals.length}`;
  }
  statusDetail.value = detail;
  status.value = agg;
}

onMounted(async () => {
  await loadBase();
  await loadStatus();
});

watch(serverIds, loadStatus);

// 空串=清空（P8-M2：后端已允许）；确认弹窗防误触（清空会断掉全量采集器输出）
function saveO2Url() {
  const url = o2Url.value.trim();
  Modal.confirm({
    title: url ? '保存 O2 地址？' : '清空 O2 地址？',
    content: url
      ? `采集器输出将指向 ${url}`
      : '清空后所有主机的 vector/fluent-bit 输出将失去目标（可重新保存恢复）。',
    okButtonProps: url ? {} : { danger: true },
    okText: url ? '保存' : '清空',
    onOk: async () => {
      try {
        await setO2UrlApi(url);
        message.success('O2 地址已保存');
      } catch {
        // URL 校验等业务错误由拦截器提示
      }
    },
  });
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
  if (serverIds.value.length === 0 || !deployTarget.value) return;
  deploying.value = true;
  const failed: number[] = [];
  try {
    // 逐台部署（顺序执行避免镜像拉取并发打爆带宽；失败继续下一台）
    for (const sid of serverIds.value) {
      const host = servers.value.find((srv) => srv.id === sid);
      try {
        await deployObservApi({
          compose: deployForm.compose,
          component: deployTarget.value?.name ?? '',
          configFiles: deployForm.configs,
          serverId: sid,
        });
        message.success(
          `${deployTarget.value.name} → ${host?.name ?? sid} 完成`,
        );
      } catch {
        failed.push(sid); // 失败详情由拦截器 toast
      }
    }
    if (failed.length === 0) {
      deployOpen.value = false;
    } else {
      message.warning(
        `${failed.length} 台主机部署失败（可重试，成功的不受影响）`,
      );
    }
    await loadStatus();
  } finally {
    deploying.value = false;
  }
}

async function onUninstall(comp: ObservComponent) {
  if (serverIds.value.length === 0) return;
  acting.value = comp.name;
  try {
    let failed = 0;
    const reasons: string[] = [];
    for (const sid of serverIds.value) {
      try {
        await uninstallObservApi(sid, comp.name);
      } catch (error: any) {
        failed++;
        reasons.push(
          `#${sid}: ${error?.response?.data?.message ?? error?.message ?? '失败'}`,
        );
      }
    }
    if (failed === 0) {
      message.success(`${comp.name} 已在 ${serverIds.value.length} 台主机卸载`);
    } else if (failed === serverIds.value.length) {
      message.error(`${comp.name} 卸载全部失败：\n${reasons.join('\n')}`);
    } else {
      message.warning(
        `${comp.name} 部分卸载失败（${failed}/${serverIds.value.length}）：\n${reasons.join('\n')}`,
      );
    }
    await loadStatus();
  } finally {
    acting.value = '';
  }
}

const statusMeta: Record<string, { color: string; text: string }> = {
  absent: { color: 'default', text: '未部署' },
  running: { color: 'green', text: '运行中' },
  stopped: { color: 'red', text: '已停止' },
  unknown: { color: 'purple', text: '探测失败' },
};

function statusTagMeta(s: string) {
  if (s.startsWith('partial:')) {
    return { color: 'orange', text: `部分运行 ${s.slice(8)}` };
  }
  return statusMeta[s] ?? { color: 'default', text: s };
}

function detailText(comp: ObservComponent) {
  const d = statusDetail.value[comp.name] ?? {};
  return servers.value
    .filter((srv) => srv.id in d)
    .map((srv) => {
      const v = d[srv.id] ?? 'absent';
      const label = statusMeta[v]?.text ?? v;
      return `${srv.name}：${label}`;
    })
    .join('\n');
}

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
          v-model:value="serverIds"
          :options="
            servers.map((s) => ({
              label: `${s.name}（${s.host}）`,
              value: s.id,
            }))
          "
          mode="multiple"
          placeholder="选择主机（部署对全部选中主机执行）"
          style="min-width: 280px"
        />
        <span class="ml-4">O2 日志地址：</span>
        <a-input
          v-model:value="o2Url"
          :disabled="!canWrite"
          placeholder="http://<o2>:5080/api/<org>/<stream>/_json（可内嵌 user:pass）"
          style="width: 380px"
        />
        <a-button v-if="canWrite" @click="saveO2Url"> 保存 </a-button>
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
              <a-tag class="ml-2">
                {{ categoryMeta[comp.category]?.text }}
              </a-tag>
            </template>
            <template #extra>
              <a-tooltip :title="detailText(comp)">
                <a-tag
                  :color="statusTagMeta(status[comp.name] ?? 'absent').color"
                >
                  {{ statusTagMeta(status[comp.name] ?? 'absent').text }}
                </a-tag>
              </a-tooltip>
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
                :disabled="comp.needsO2Url && !o2Url"
                :loading="acting === comp.name || (deploying && deployOpen)"
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
