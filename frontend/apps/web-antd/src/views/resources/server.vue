<script lang="ts" setup>
import type {
  ManagedServer,
  MetricPoint,
  ServerGroup,
  ServerPayload,
} from '#/api/resources/server';
import type { HostInfo } from '#/api/resources/server';

import { computed, onBeforeUnmount, onMounted, reactive, ref } from 'vue';

import { useUserStore } from '@vben/stores';

import { message } from 'ant-design-vue';

import { probeEnvApi } from '#/api/resources/containers';
import {
  createServerApi,
  deleteServerApi,
  getGroupListApi,
  getLatestMetricsApi,
  getServerListApi,
  testConnectionApi,
  testServerApi,
  updateServerApi,
} from '#/api/resources/server';

import ContainersDrawer from './containers-drawer.vue';
import MetricsDrawer from './metrics-drawer.vue';
import Sparkline from './sparkline.vue';
import TerminalModal from './terminal-modal.vue';

defineOptions({ name: 'ResourcesServer' });

function parseHost(raw: string | undefined): HostInfo | null {
  if (!raw) return null;
  try {
    return JSON.parse(raw) as HostInfo;
  } catch {
    return null;
  }
}

function gb(bytes: number): string {
  return (bytes / 1024 ** 3).toFixed(0);
}

function hostSummary(raw: string): string {
  const h = parseHost(raw);
  if (!h) return '未探测';
  const parts = [`${h.cpuCores}C`, `${gb(h.memBytes)}G`];
  if (h.diskBytes > 0) {
    parts.push(
      `${gb(h.diskBytes)}G(${Math.round((h.diskUsed / h.diskBytes) * 100)}%)`,
    );
  }
  if (h.netMbps > 0) {
    parts.push(`${h.netMbps}Mbps`);
  }
  return parts.join(' · ');
}

function hostTooltip(raw: string): string {
  const h = parseHost(raw);
  if (!h) return '';
  return [
    `CPU：${h.cpuModel || '未知'}（${h.cpuCores} 核）`,
    `内存：${gb(h.memBytes)} GB`,
    `硬盘：${gb(h.diskUsed)} / ${gb(h.diskBytes)} GB（${Math.round((h.diskUsed / Math.max(h.diskBytes, 1)) * 100)}%）`,
    `网卡协商速率：${h.netMbps > 0 ? `${h.netMbps} Mbps（公网带宽为云厂商属性，机器内测不到）` : '未知'}`,
    `探测时间：${h.probedAt}`,
  ].join('\n');
}

const userStore = useUserStore();
// 终端仅 admin 以上（后端 casbin 兜底，这里只控制按钮可见性）
const canTerminal = computed(() => {
  const roles = userStore.userInfo?.roles ?? [];
  return roles.includes('superadmin') || roles.includes('admin');
});

const loading = ref(false);
const list = ref<ManagedServer[]>([]);
const groups = ref<ServerGroup[]>([]);
const groupFilter = ref<number | undefined>();

const filtered = computed(() =>
  groupFilter.value
    ? list.value.filter((s) => s.groupId === groupFilter.value)
    : list.value,
);

async function load() {
  loading.value = true;
  try {
    [list.value, groups.value] = await Promise.all([
      getServerListApi(),
      getGroupListApi(),
    ]);
  } finally {
    loading.value = false;
  }
}

onMounted(() => {
  load();
  loadMetrics();
  metricsTimer = window.setInterval(loadMetrics, 15_000);
});

onBeforeUnmount(() => window.clearInterval(metricsTimer));

// --- 指标 sparkline（内存缓冲，30s 轮询） ---
let metricsTimer = 0;
const latestMetrics = ref<Record<string, MetricPoint[]>>({});

async function loadMetrics() {
  try {
    latestMetrics.value = await getLatestMetricsApi();
  } catch {
    // 轮询失败静默，下轮重试
  }
}

function sparkSeries(id: number, kind: 'cpu' | 'mem') {
  const pts = latestMetrics.value[String(id)] ?? [];
  return pts.slice(-40).map((p) => {
    if (kind === 'cpu') return p.cpuPct;
    return p.memTotal > 0 ? (p.memUsed / p.memTotal) * 100 : 0;
  });
}

// --- 曲线抽屉 ---
const drawerOpen = ref(false);
const drawerServer = ref<ManagedServer | null>(null);

function openMetrics(s: (typeof list.value)[number]) {
  drawerServer.value = s;
  drawerOpen.value = true;
}

// --- Web 终端 ---
// --- 主机配置探测（顺带刷新 docker/compose 环境） ---
const probingId = ref<null | number>(null);
async function probeHost(s: ManagedServer) {
  probingId.value = s.id;
  try {
    await probeEnvApi(s.id);
    await load();
    message.success('探测完成，配置已更新');
  } catch {
    // 拦截器提示
  } finally {
    probingId.value = null;
  }
}

const terminalOpen = ref(false);
const terminalServer = ref<ManagedServer | null>(null);

function openTerminal(s: (typeof list.value)[number]) {
  terminalServer.value = s;
  terminalOpen.value = true;
}

// --- 容器管理（M4；compose 部署/编辑收在容器抽屉内） ---
const containersOpen = ref(false);
const containersServerId = ref<null | number>(null);

function openContainers(s: (typeof list.value)[number]) {
  containersServerId.value = s.id;
  containersOpen.value = true;
}

function groupName(id: null | number) {
  if (!id) return '—';
  return groups.value.find((g) => g.id === id)?.name ?? `#${id}`;
}

const STATUS_TAG: Record<string, { color: string; text: string }> = {
  reachable: { color: 'green', text: '可达' },
  unreachable: { color: 'red', text: '不可达' },
  unknown: { color: 'default', text: '未知' },
};

function fmtTime(t: null | string) {
  return t ? new Date(t).toLocaleString() : '—';
}

// --- 连通性测试 ---
const testingId = ref(0);

async function testConnectivity(id: number) {
  testingId.value = id;
  try {
    const res = await testServerApi(id);
    message.success(res.message || '连接成功');
    await load();
  } catch {
    // 拦截器已弹出错误消息；这里只需刷新状态
    await load();
  } finally {
    testingId.value = 0;
  }
}

// --- 新建 / 编辑 ---
const formOpen = ref(false);
const editingId = ref<null | number>(null); // null = 新建
const form = reactive<ServerPayload & { groupIdVal: number | undefined }>({
  name: '',
  host: '',
  port: 22,
  authType: 'password',
  username: 'root',
  password: '',
  privateKey: '',
  passphrase: '',
  metricSecs: 30,
  remark: '',
  groupIdVal: undefined,
});

function openCreate() {
  editingId.value = null;
  Object.assign(form, {
    name: '',
    host: '',
    port: 22,
    authType: 'password',
    username: 'root',
    password: '',
    privateKey: '',
    passphrase: '',
    metricSecs: 30,
    remark: '',
    groupIdVal: undefined,
  });
  formOpen.value = true;
}

function openEdit(s: (typeof list.value)[number]) {
  editingId.value = s.id;
  Object.assign(form, {
    name: s.name,
    host: s.host,
    port: s.port,
    authType: s.authType,
    username: '', // 原用户名不回传，留空表示保留
    // 编辑时凭据留空 = 保留原凭据
    password: '',
    privateKey: '',
    passphrase: '',
    metricSecs: s.metricSecs,
    remark: s.remark,
    groupIdVal: s.groupId ?? undefined,
  });
  formOpen.value = true;
}

async function onKeyFile(e: Event) {
  const file = (e.target as HTMLInputElement).files?.[0];
  if (!file) return;
  form.privateKey = await file.text();
  message.success(`已导入私钥文件：${file.name}`);
}

function buildPayload(): ServerPayload {
  const payload: ServerPayload = {
    name: form.name,
    host: form.host,
    port: form.port,
    authType: form.authType,
    username: form.username,
    metricSecs: form.metricSecs,
    remark: form.remark,
    groupId: form.groupIdVal ?? null,
  };
  if (form.authType === 'password' && form.password) {
    payload.password = form.password;
  }
  if (form.authType === 'key' && form.privateKey) {
    payload.privateKey = form.privateKey;
    if (form.passphrase) payload.passphrase = form.passphrase;
  }
  return payload;
}

// 表单内"测试连通"：用当前填写的凭据即席连接（新建模式用表单值，
// 编辑模式若未改凭据则落库的旧凭据由保存后的自动测试兜底）
const testingConn = ref(false);

async function testConn() {
  if (!form.host) {
    message.warning('请先填写主机地址');
    return;
  }
  testingConn.value = true;
  try {
    const payload = buildPayload();
    if (editingId.value && !form.username) delete payload.username;
    const res = await testConnectionApi(payload);
    message.success(res.message || '连接成功');
  } catch {
    // 失败详情由拦截器弹出
  } finally {
    testingConn.value = false;
  }
}

async function submitForm() {
  if (!form.name || !form.host || (!form.username && !editingId.value)) {
    message.warning('请填写名称、主机与用户名');
    return;
  }
  const payload = buildPayload();
  if (editingId.value) {
    // 编辑时用户名/凭据留空即不下发，后端保留原值
    if (!form.username) delete payload.username;
    await updateServerApi(editingId.value, payload);
    message.success('已更新');
    formOpen.value = false;
    await load();
    // 保存后自动测试连通，把结果和最新状态刷出来
    autoTest(editingId.value);
  } else {
    const created = await createServerApi(payload);
    message.success('创建成功');
    formOpen.value = false;
    await load();
    autoTest(created.id);
  }
}

// 保存后自动连通性测试：结果只提示失败（成功状态列自己会变绿）
async function autoTest(id: number) {
  try {
    await testServerApi(id);
  } catch {
    message.warning('保存成功，但连通性测试失败，请检查凭据');
    await load();
  }
}

// --- 删除 ---
async function onDelete(id: number, name: string) {
  await deleteServerApi(id);
  message.success(`已删除 ${name}`);
  await load();
}
</script>

<template>
  <div class="p-4">
    <a-card title="主机管理">
      <template #extra>
        <div class="flex items-center gap-2">
          <a-select
            v-model:value="groupFilter"
            allow-clear
            placeholder="按分组筛选"
            style="width: 160px"
            :options="groups.map((g) => ({ label: g.name, value: g.id }))"
          />
          <a-button type="primary" @click="openCreate">新增服务器</a-button>
        </div>
      </template>
      <a-table
        :data-source="filtered"
        :loading="loading"
        :pagination="false"
        row-key="id"
      >
        <a-table-column title="名称" data-index="name" />
        <a-table-column title="地址">
          <template #default="{ record }">
            {{ record.host }}:{{ record.port }}
          </template>
        </a-table-column>
        <a-table-column title="分组" :width="110">
          <template #default="{ record }">
            {{ groupName(record.groupId) }}
          </template>
        </a-table-column>
        <a-table-column title="认证" data-index="authType" :width="90">
          <template #default="{ text }">
            <a-tag :color="text === 'key' ? 'geekblue' : 'cyan'">
              {{ text === 'key' ? '密钥' : '密码' }}
            </a-tag>
          </template>
        </a-table-column>
        <a-table-column title="CPU" :width="170">
          <template #default="{ record }">
            <div
              v-if="sparkSeries(record.id, 'cpu').length >= 2"
              class="cursor-pointer"
              title="点击查看详情"
              @click="openMetrics(record)"
            >
              <Sparkline :points="sparkSeries(record.id, 'cpu')" />
            </div>
            <span v-else class="text-xs text-gray-400">采集中…</span>
          </template>
        </a-table-column>
        <a-table-column title="内存" :width="170">
          <template #default="{ record }">
            <div
              v-if="sparkSeries(record.id, 'mem').length >= 2"
              class="cursor-pointer"
              title="点击查看详情"
              @click="openMetrics(record)"
            >
              <Sparkline
                :points="sparkSeries(record.id, 'mem')"
                color="#52c41a"
              />
            </div>
            <span v-else class="text-xs text-gray-400">采集中…</span>
          </template>
        </a-table-column>
        <a-table-column title="配置" :width="200">
          <template #default="{ record }">
            <a-tooltip
              v-if="parseHost(record.hostInfo)"
              :title="hostTooltip(record.hostInfo)"
            >
              <span class="text-xs">{{ hostSummary(record.hostInfo) }}</span>
            </a-tooltip>
            <span v-else class="text-muted-foreground text-xs">未探测</span>
          </template>
        </a-table-column>
        <a-table-column title="状态" :width="90">
          <template #default="{ record }">
            <a-tag :color="STATUS_TAG[record.status]?.color ?? 'default'">
              {{ STATUS_TAG[record.status]?.text ?? record.status }}
            </a-tag>
          </template>
        </a-table-column>
        <a-table-column title="采集间隔" :width="90">
          <template #default="{ record }"> {{ record.metricSecs }}s </template>
        </a-table-column>
        <a-table-column title="最近在线" :width="170">
          <template #default="{ record }">
            {{ fmtTime(record.lastSeen) }}
          </template>
        </a-table-column>
        <a-table-column title="操作" :width="340">
          <template #default="{ record }">
            <a-button size="small" type="link" @click="openContainers(record)">
              容器
            </a-button>
            <a-button
              size="small"
              type="link"
              :loading="probingId === record.id"
              @click="probeHost(record)"
            >
              探测
            </a-button>
            <a-button
              v-if="canTerminal"
              size="small"
              type="link"
              @click="openTerminal(record)"
            >
              终端
            </a-button>
            <a-button
              size="small"
              type="link"
              :loading="testingId === record.id"
              @click="testConnectivity(record.id)"
            >
              测试连通
            </a-button>
            <a-button size="small" type="link" @click="openEdit(record)">
              编辑
            </a-button>
            <a-popconfirm
              :title="`确认删除 ${record.name}？`"
              @confirm="onDelete(record.id, record.name)"
            >
              <a-button size="small" type="link" danger>删除</a-button>
            </a-popconfirm>
          </template>
        </a-table-column>
      </a-table>
    </a-card>

    <a-modal
      v-model:open="formOpen"
      :title="editingId ? `编辑服务器：${form.name}` : '新增服务器'"
      :width="620"
      :ok-text="editingId ? '保存' : '保存（自动测试连通）'"
      @ok="submitForm"
    >
      <template #footer>
        <a-button @click="formOpen = false">取消</a-button>
        <a-button :loading="testingConn" @click="testConn">测试连通</a-button>
        <a-button type="primary" @click="submitForm">保存</a-button>
      </template>
      <a-form layout="vertical">
        <div class="grid grid-cols-2 gap-x-4">
          <a-form-item label="名称" required>
            <a-input v-model:value="form.name" placeholder="如：web-1" />
          </a-form-item>
          <a-form-item label="分组">
            <a-select
              v-model:value="form.groupIdVal"
              allow-clear
              placeholder="不分组"
              :options="groups.map((g) => ({ label: g.name, value: g.id }))"
            />
          </a-form-item>
          <a-form-item label="主机" required>
            <a-input v-model:value="form.host" placeholder="IP 或域名" />
          </a-form-item>
          <a-form-item label="SSH 端口" required>
            <a-input-number
              v-model:value="form.port"
              :min="1"
              :max="65_535"
              class="w-full"
            />
          </a-form-item>
        </div>

        <a-form-item label="认证方式">
          <a-radio-group v-model:value="form.authType">
            <a-radio value="password">密码</a-radio>
            <a-radio value="key">密钥</a-radio>
          </a-radio-group>
        </a-form-item>
        <a-form-item
          :label="editingId ? '用户名（留空保留原值）' : '用户名'"
          :required="!editingId"
        >
          <a-input
            v-model:value="form.username"
            placeholder="如：root"
            style="width: 240px"
          />
        </a-form-item>

        <template v-if="form.authType === 'password'">
          <a-form-item
            :label="editingId ? '密码（留空保留原密码）' : '密码'"
            :required="!editingId"
          >
            <a-input-password v-model:value="form.password" />
          </a-form-item>
        </template>
        <template v-else>
          <a-form-item
            :label="
              editingId ? '私钥（留空保留原私钥）' : '私钥（OpenSSH PEM）'
            "
            :required="!editingId"
          >
            <div class="flex w-full flex-col gap-2">
              <a-textarea
                v-model:value="form.privateKey"
                :rows="5"
                placeholder="-----BEGIN OPENSSH PRIVATE KEY-----&#10;...&#10;-----END OPENSSH PRIVATE KEY-----"
              />
              <input type="file" accept=".pem,.key,id_*" @change="onKeyFile" />
            </div>
          </a-form-item>
          <a-form-item label="私钥口令（无口令可留空）">
            <a-input-password v-model:value="form.passphrase" />
          </a-form-item>
        </template>

        <div class="grid grid-cols-2 gap-x-4">
          <a-form-item label="采集间隔">
            <a-select
              v-model:value="form.metricSecs"
              :options="
                [15, 30, 60].map((v) => ({
                  label: `${v} 秒`,
                  value: v,
                }))
              "
            />
          </a-form-item>
          <a-form-item label="备注">
            <a-input v-model:value="form.remark" />
          </a-form-item>
        </div>
      </a-form>
    </a-modal>

    <MetricsDrawer v-model:open="drawerOpen" :server="drawerServer" />
    <TerminalModal v-model:open="terminalOpen" :server="terminalServer" />
    <ContainersDrawer
      v-model:open="containersOpen"
      :server-id="containersServerId"
    />
  </div>
</template>
