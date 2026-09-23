<script lang="ts" setup>
import type { DockerContainer } from '#/api/resources/containers';

import { computed, onBeforeUnmount, ref, watch } from 'vue';

import { useUserStore } from '@vben/stores';

import { message } from 'ant-design-vue';

import {
  containerActionApi,
  containerLogsApi,
  getComposeFileApi,
  issueTicketApi,
  listContainersApi,
  recreateComposeApi,
  saveComposeFileApi,
  statsAllApi,
} from '#/api/resources/containers';
import YamlEditor from '#/components/yaml-editor.vue';

import ComposeModal from './compose-modal.vue';

const props = defineProps<{ serverId: null | number }>();
const open = defineModel<boolean>('open', { default: false });

const userStore = useUserStore();
const canManage = computed(() => {
  const roles = userStore.userInfo?.roles ?? [];
  return (
    roles.includes('superadmin') ||
    roles.includes('admin') ||
    roles.includes('ops')
  );
});

const loading = ref(false);
const list = ref<DockerContainer[]>([]);
const statsMap = ref<Record<string, ContainerStat>>({});
const actingId = ref('');
const view = ref<'compose' | 'containers'>('containers');

interface ContainerStat {
  cpuPerc: string;
  memPerc: string;
  memUsage: string;
}

const STATE_COLOR: Record<string, string> = {
  created: 'default',
  exited: 'red',
  paused: 'orange',
  restarting: 'orange',
  running: 'green',
};

// compose 项目视图：按容器标签聚合（含停掉的容器，只要没删）
interface ComposeProject {
  containers: DockerContainer[];
  file: string;
  name: string;
}

const projects = computed<ComposeProject[]>(() => {
  const map = new Map<string, ComposeProject>();
  for (const ct of list.value) {
    if (!ct.composeProject) continue;
    let p = map.get(ct.composeProject);
    if (!p) {
      p = {
        name: ct.composeProject,
        file: ct.composeFile ?? '',
        containers: [],
      };
      map.set(ct.composeProject, p);
    }
    p.containers.push(ct);
  }
  return [...map.values()].sort((a, b) => a.name.localeCompare(b.name));
});

let statsTimer = 0;

async function load() {
  if (!props.serverId) return;
  loading.value = true;
  try {
    list.value = await listContainersApi(props.serverId);
    loadStats(); // 不阻塞列表渲染
  } finally {
    loading.value = false;
  }
}

// 批量拉取全部容器占用（后端一条 SSH 命令），抽屉打开期间 10s 轮询
async function loadStats() {
  if (!props.serverId) return;
  try {
    const stats = await statsAllApi(props.serverId);
    const map: Record<string, ContainerStat> = {};
    for (const s of stats ?? []) map[s.id] = s;
    statsMap.value = map;
  } catch {
    // 轮询失败静默
  }
}

watch(open, (v) => {
  statsMap.value = {};
  if (v) {
    load();
    statsTimer = window.setInterval(loadStats, 10_000);
  } else {
    stopFollow();
    window.clearInterval(statsTimer);
  }
});

onBeforeUnmount(() => {
  window.clearInterval(statsTimer);
  stopFollow();
});

async function act(cid: string, action: 'start' | 'stop', name: string) {
  if (!props.serverId) return;
  actingId.value = cid;
  try {
    await containerActionApi(props.serverId, cid, action);
    message.success(`${name} 已${action === 'start' ? '启动' : '停止'}`);
    await load();
  } finally {
    actingId.value = '';
  }
}

// --- 部署文件查看/编辑（Monaco） ---
const fileOpen = ref(false);
const filePath = ref('');
const fileProject = ref('');
const fileContent = ref('');
const fileEditing = ref(false);
const fileLoading = ref(false);
const fileSaving = ref(false);
const recreating = ref(false);
const recreateOutput = ref('');

async function openFile(p: ComposeProject) {
  if (!props.serverId || !p.file) {
    message.warning('该项目没有部署文件信息（可能容器由其他方式创建）');
    return;
  }
  filePath.value = p.file;
  fileProject.value = p.name;
  fileEditing.value = false;
  recreateOutput.value = '';
  fileLoading.value = true;
  fileOpen.value = true;
  try {
    const res = await getComposeFileApi(props.serverId, p.file);
    fileContent.value = res.content;
  } catch {
    fileContent.value = '';
  } finally {
    fileLoading.value = false;
  }
}

async function saveFile() {
  if (!props.serverId) return;
  fileSaving.value = true;
  try {
    const res = await saveComposeFileApi(
      props.serverId,
      filePath.value,
      fileContent.value,
    );
    message.success(res.message);
    fileEditing.value = false;
  } catch {
    // 校验/写入失败由拦截器提示
  } finally {
    fileSaving.value = false;
  }
}

async function recreate() {
  if (!props.serverId) return;
  recreating.value = true;
  recreateOutput.value = '';
  try {
    const res = await recreateComposeApi(
      props.serverId,
      filePath.value,
      fileProject.value,
    );
    recreateOutput.value = res.output;
    message.success('重建完成');
    await load();
  } catch {
    // 失败详情由拦截器提示
  } finally {
    recreating.value = false;
  }
}

// --- 新建部署（原列表页"部署"按钮收进来） ---
const composeOpen = ref(false);

// --- 日志子抽屉（打开即跟随 -f，自动滚到最新） ---
const logsOpen = ref(false);
const logsCid = ref('');
const logsName = ref('');
const logsText = ref('');
const logsLoading = ref(false);
const follow = ref(false);
const autoscroll = ref(true);
const logsPreRef = ref<HTMLPreElement>();
let es: EventSource | null = null;

function stopFollow() {
  es?.close();
  es = null;
  follow.value = false;
}

async function openLogs(c: DockerContainer) {
  logsCid.value = c.id;
  logsName.value = c.name;
  logsOpen.value = true;
  logsLoading.value = true;
  logsText.value = '';
  try {
    const lines = await containerLogsApi(props.serverId!, c.id, 200);
    logsText.value = (lines ?? []).join('\n');
  } catch {
    // 拉取失败也继续尝试跟随
  } finally {
    logsLoading.value = false;
  }
  scrollLogsBottom();
  startFollow(); // 打开即 -f
}

async function startFollow() {
  if (!props.serverId || !logsCid.value || es) return;
  // SSE 跟随：一次性 ticket 走 query（浏览器 EventSource 无法带 header）
  try {
    const { ticket } = await issueTicketApi();
    const url = `/api/server-containers/${props.serverId}/${logsCid.value}/logs?tail=50&follow=1&ticket=${encodeURIComponent(ticket)}`;
    es = new EventSource(url);
    follow.value = true;
    es.addEventListener('log', (ev) => {
      logsText.value += (ev as MessageEvent).data;
      // 封顶防内存膨胀
      if (logsText.value.length > 1_000_000) {
        logsText.value = logsText.value.slice(-500_000);
      }
      if (autoscroll.value) scrollLogsBottom();
    });
    es.onerror = () => {
      stopFollow();
      message.info('日志流已结束');
    };
  } catch {
    // 领 ticket 失败静默
  }
}

function scrollLogsBottom() {
  requestAnimationFrame(() => {
    const el = logsPreRef.value;
    if (el) el.scrollTop = el.scrollHeight;
  });
}

// 用户往上翻阅时暂停自动滚动，翻回底部附近自动恢复
function onLogsScroll() {
  const el = logsPreRef.value;
  if (el) {
    autoscroll.value = el.scrollHeight - el.scrollTop - el.clientHeight < 40;
  }
}

function stopFollowOnClose() {
  stopFollow();
  logsOpen.value = false;
}
</script>

<template>
  <a-drawer v-model:open="open" :width="1100" title="容器管理">
    <div class="flex items-center justify-between pb-3">
      <a-segmented
        v-model:value="view"
        :options="[
          { label: `容器 (${list.length})`, value: 'containers' },
          { label: `Compose 项目 (${projects.length})`, value: 'compose' },
        ]"
      />
      <a-button
        v-if="canManage && view === 'compose'"
        type="primary"
        @click="composeOpen = true"
      >
        新建部署
      </a-button>
    </div>

    <!-- 容器视图 -->
    <a-table
      v-if="view === 'containers'"
      :data-source="list"
      :loading="loading"
      :pagination="false"
      row-key="id"
      size="small"
    >
      <a-table-column title="容器" data-index="name" />
      <a-table-column title="镜像" data-index="image" :ellipsis="true" />
      <a-table-column title="状态" :width="130">
        <template #default="{ record }">
          <a-tag :color="STATE_COLOR[record.state] ?? 'default'">
            {{ record.state }}
          </a-tag>
          <div class="text-xs text-gray-400">{{ record.status }}</div>
        </template>
      </a-table-column>
      <a-table-column title="CPU" :width="100">
        <template #default="{ record }">
          <span v-if="statsMap[record.id]" class="tabular-nums">
            {{ statsMap[record.id]!.cpuPerc }}
          </span>
          <span v-else class="text-xs text-gray-400">—</span>
        </template>
      </a-table-column>
      <a-table-column title="内存" :width="180">
        <template #default="{ record }">
          <span v-if="statsMap[record.id]" class="tabular-nums">
            {{ statsMap[record.id]!.memUsage }}
            <span class="ml-1 text-xs text-gray-400">
              ({{ statsMap[record.id]!.memPerc }})
            </span>
          </span>
          <span v-else class="text-xs text-gray-400">—</span>
        </template>
      </a-table-column>
      <a-table-column title="操作" :width="200">
        <template #default="{ record }">
          <a-button
            size="small"
            type="link"
            :disabled="record.state === 'running'"
            :loading="actingId === record.id && record.state !== 'running'"
            @click="act(record.id, 'start', record.name)"
          >
            启动
          </a-button>
          <a-button
            size="small"
            type="link"
            danger
            :disabled="record.state !== 'running'"
            :loading="actingId === record.id && record.state === 'running'"
            @click="act(record.id, 'stop', record.name)"
          >
            停止
          </a-button>
          <a-button size="small" type="link" @click="openLogs(record)">
            日志
          </a-button>
        </template>
      </a-table-column>
    </a-table>

    <!-- Compose 项目视图 -->
    <div v-else class="flex flex-col gap-3">
      <a-spin :spinning="loading">
        <a-card v-for="p in projects" :key="p.name" size="small">
          <template #title>
            <span class="font-medium">{{ p.name }}</span>
            <span class="ml-2 text-xs text-gray-400">{{ p.file }}</span>
          </template>
          <template #extra>
            <a-button
              size="small"
              type="primary"
              ghost
              @click="openFile(p)"
            >
              {{ canManage ? '部署文件' : '查看文件' }}
            </a-button>
          </template>
          <div class="flex flex-wrap gap-2">
            <a-tag
              v-for="ct in p.containers"
              :key="ct.id"
              :color="ct.state === 'running' ? 'green' : 'red'"
            >
              {{ ct.name }}（{{ ct.state }}）
            </a-tag>
          </div>
        </a-card>
        <a-empty
          v-if="projects.length === 0"
          description="未发现 compose 部署的容器"
        />
      </a-spin>
    </div>

    <!-- 部署文件查看/编辑/重建 -->
    <a-modal
      v-model:open="fileOpen"
      :footer="null"
      :title="`部署文件：${fileProject}`"
      :width="860"
      destroy-on-close
    >
      <a-spin :spinning="fileLoading">
        <div class="pb-2 text-xs text-gray-400">{{ filePath }}</div>
        <YamlEditor
          v-model="fileContent"
          :height="fileEditing ? '440px' : '480px'"
          :read-only="!fileEditing"
          schema="compose"
        />
        <div v-if="canManage" class="flex items-center justify-between pt-3">
          <div class="flex gap-2">
            <a-button
              v-if="!fileEditing"
              type="primary"
              ghost
              @click="fileEditing = true"
            >
              编辑
            </a-button>
            <template v-else>
              <a-button @click="fileEditing = false">取消</a-button>
              <a-button
                type="primary"
                :loading="fileSaving"
                @click="saveFile"
              >
                保存（远端自动备份）
              </a-button>
            </template>
          </div>
          <a-button
            danger
            ghost
            type="primary"
            :loading="recreating"
            :disabled="fileEditing"
            @click="recreate"
          >
            重建容器
          </a-button>
        </div>
        <pre
          v-if="recreateOutput"
          class="mt-3 max-h-60 overflow-auto rounded bg-[#1e1e1e] p-2 text-xs text-gray-200"
        >{{ recreateOutput }}</pre>
      </a-spin>
    </a-modal>

    <!-- 新建部署 -->
    <ComposeModal v-model:open="composeOpen" :server-id="serverId" />

    <!-- 日志子抽屉 -->
    <a-drawer
      v-model:open="logsOpen"
      :title="`日志：${logsName}`"
      :width="900"
      @close="stopFollowOnClose"
    >
      <div class="flex items-center gap-3 pb-2">
        <a-tag :color="follow ? 'green' : 'default'">
          {{ follow ? '跟随中 (-f)' : '已停止跟随' }}
        </a-tag>
        <a-button v-if="!follow" size="small" @click="startFollow">
          继续跟随
        </a-button>
        <a-button v-else size="small" @click="stopFollow">暂停跟随</a-button>
        <a-checkbox v-model:checked="autoscroll">自动滚动</a-checkbox>
        <span class="text-xs text-gray-400">打开即最近 200 行 + 实时跟随</span>
      </div>
      <a-spin :spinning="logsLoading">
        <pre
          ref="logsPreRef"
          class="max-h-[72vh] overflow-auto rounded bg-[#1e1e1e] p-2 text-xs leading-5 text-gray-200"
          @scroll="onLogsScroll"
        >{{ logsText || '（暂无日志）' }}</pre>
      </a-spin>
    </a-drawer>
  </a-drawer>
</template>
