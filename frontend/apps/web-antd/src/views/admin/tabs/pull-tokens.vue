<script lang="ts" setup>
import type { AgileDiff } from '#/api/configs/kv';
import type { PullToken } from '#/api/configs/pull';

import { onMounted, reactive, ref } from 'vue';

import { message } from 'ant-design-vue';

import {
  getAgileSettingsApi,
  reconcileAgileApi,
  saveAgileSettingsApi,
  syncAgileApi,
} from '#/api/configs/kv';
import {
  createPullTokenApi,
  listPullTokensApi,
  setPullTokenEnabledApi,
} from '#/api/configs/pull';
import { getProjectsApi } from '#/api/projects';

defineOptions({ name: 'AdminPullTokens' });

const tokens = ref<PullToken[]>([]);
const loading = ref(false);
const createOpen = ref(false);
// 应用接入说明（服务启动 GET 一次；凭证带 Authorization 或 X-Config-Token）
const endpointHint = (t: PullToken) =>
  `curl ${window.location.origin}/api/config/${t.app}/${t.envs.split(',')[0]} -H "X-Config-Token: <凭证>"`;
// 签发结果（明文只出现一次）
const issued = ref<null | { app: string; envs: string; plaintext: string }>(null);

const form = reactive({ app: '', envs: 'prod,test', name: '' });

// ---- AgileConfig（配置中心热更通道）----
const agile = reactive({ configured: false, endpoint: '' });
const agileForm = reactive({ endpoint: '', user: '', password: '' });
const projects = ref<{ id: number; name: string }[]>([]);
const agileTarget = reactive({ projectId: undefined as number | undefined, env: 'prod' });
const syncing = ref(false);
const reconciling = ref(false);
const diff = ref<AgileDiff | null>(null);
const diffOpen = ref(false);

async function loadAgile() {
  const s = await getAgileSettingsApi();
  agile.configured = s.configured;
  agile.endpoint = s.endpoint;
  agileForm.endpoint = s.endpoint;
}

async function saveAgile() {
  await saveAgileSettingsApi({
    ...(agileForm.endpoint ? { endpoint: agileForm.endpoint } : {}),
    ...(agileForm.user ? { user: agileForm.user } : {}),
    ...(agileForm.password ? { password: agileForm.password } : {}),
  });
  await loadAgile();
  message.success('已保存（凭据 AES 落库）');
}

async function manualSync() {
  if (!agileTarget.projectId) return;
  syncing.value = true;
  try {
    const n = await syncAgileApi(agileTarget.projectId, agileTarget.env);
    message.success(`已同步 ${n} 项到 AgileConfig（并上线）`);
  } catch (e: any) {
    message.error(e?.response?.data?.message ?? '同步失败');
  } finally {
    syncing.value = false;
  }
}

async function manualReconcile() {
  if (!agileTarget.projectId) return;
  reconciling.value = true;
  try {
    diff.value = await reconcileAgileApi(agileTarget.projectId, agileTarget.env);
    diffOpen.value = true;
  } catch (e: any) {
    message.error(e?.response?.data?.message ?? '对账失败');
  } finally {
    reconciling.value = false;
  }
}

async function load() {
  loading.value = true;
  try {
    tokens.value = await listPullTokensApi();
  } finally {
    loading.value = false;
  }
}

async function create() {
  const out = await createPullTokenApi({
    app: form.app.trim(),
    envs: form.envs.trim(),
    name: form.name.trim() || form.app.trim(),
  });
  issued.value = {
    app: out.app,
    envs: out.envs,
    plaintext: out.plaintext,
  };
  createOpen.value = false;
  await load();
}

async function toggle(t: PullToken, enabled: boolean) {
  await setPullTokenEnabledApi(t.id, enabled);
  t.enabled = enabled;
  if (!enabled) message.warning('已停用（即时生效）');
}

onMounted(async () => {
  await Promise.all([load(), loadAgile()]);
  const list = await getProjectsApi();
  projects.value = list.map((p) => ({ id: p.id, name: p.name }));
  if (!agileTarget.projectId && projects.value.length > 0) {
    agileTarget.projectId = projects.value[0]!.id;
  }
});
</script>

<template>
  <div class="space-y-3">
    <div class="flex items-center gap-2">
      <a-button type="primary" @click="createOpen = true">签发凭证</a-button>
      <span class="text-xs text-muted-foreground">
        应用级只读凭证：服务启动时 GET /api/config/{app}/{env} 拉取合并配置（应用名=项目名）
      </span>
      <div class="flex-1"></div>
      <a-button :loading="loading" @click="load">刷新</a-button>
    </div>

    <!-- 签发结果：明文只出现一次 -->
    <a-alert v-if="issued" type="success" show-icon closable @close="issued = null">
      <template #message>凭证已签发（明文仅此一次，请立即保存）</template>
      <template #description>
        <div class="break-all font-mono text-xs">{{ issued.plaintext }}</div>
        <div class="mt-1 text-xs">
          接入示例：<span class="font-mono">curl /api/config/{{ issued.app }}/{{ issued.envs.split(',')[0] }} -H "X-Config-Token: 上述凭证"</span>
        </div>
      </template>
    </a-alert>

    <a-table
      :data-source="tokens"
      :loading="loading"
      :pagination="false"
      row-key="id"
      size="small"
    >
      <a-table-column title="名称" data-index="name" :width="140" />
      <a-table-column title="应用" data-index="app" :width="120" />
      <a-table-column title="环境范围" data-index="envs" :width="120" />
      <a-table-column title="最近使用" data-index="lastUsedAt" :width="160">
        <template #default="{ record }">
          {{ record.lastUsedAt ? String(record.lastUsedAt).slice(0, 19).replace('T', ' ') : '—' }}
        </template>
      </a-table-column>
      <a-table-column title="状态" :width="90">
        <template #default="{ record }">
          <a-tag :color="record.enabled ? 'green' : 'red'">
            {{ record.enabled ? '启用' : '停用' }}
          </a-tag>
        </template>
      </a-table-column>
      <a-table-column title="接入示例" :width="220">
        <template #default="{ record }">
          <span class="cursor-pointer font-mono text-xs text-muted-foreground" :title="endpointHint(record)">
            {{ `/api/config/${record.app}/…` }}
          </span>
        </template>
      </a-table-column>
      <a-table-column title="操作" :width="90">
        <template #default="{ record }">
          <a-popconfirm
            :title="record.enabled ? '停用后服务拉取立即失效，确认？' : '确认重新启用？'"
            @confirm="toggle(record, !record.enabled)"
          >
            <a-button danger size="small" type="link">
              {{ record.enabled ? '停用' : '启用' }}
            </a-button>
          </a-popconfirm>
        </template>
      </a-table-column>
    </a-table>

    <!-- AgileConfig 配置中心（P6-M7）：文件下发自动同步的通道设置 + 手动同步/对账 -->
    <div class="mt-2 rounded-lg border border-border p-3">
      <div class="mb-2 flex items-center gap-2">
        <span class="text-sm font-medium">配置中心（AgileConfig 热更通道）</span>
        <a-tag :color="agile.configured ? 'green' : 'default'">
          {{ agile.configured ? '已连接' : '未配置' }}
        </a-tag>
        <span class="text-xs text-muted-foreground">
          env/ini 配置文件「下发」时自动同步；应用嵌 AgileConfig SDK 获得键值热更。文件是唯一编辑入口
        </span>
      </div>
      <div class="flex flex-wrap items-center gap-2">
        <a-input v-model:value="agileForm.endpoint" placeholder="http://agileconfig:5000" size="small" style="width: 220px" />
        <a-input v-model:value="agileForm.user" placeholder="admin 用户名（默认 admin）" size="small" style="width: 180px" />
        <a-input-password v-model:value="agileForm.password" placeholder="admin 密码（留空保留）" size="small" style="width: 180px" />
        <a-button size="small" @click="saveAgile">保存连接</a-button>
        <div class="flex-1"></div>
        <a-select
          v-model:value="agileTarget.projectId"
          :options="projects.map((p) => ({ value: p.id, label: p.name }))"
          size="small"
          style="width: 130px"
        />
        <a-select
          v-model:value="agileTarget.env"
          :options="[
            { value: 'prod', label: 'prod' },
            { value: 'canary', label: 'canary' },
            { value: 'test', label: 'test' },
          ]"
          size="small"
          style="width: 100px"
        />
        <a-button :loading="reconciling" size="small" @click="manualReconcile">对账</a-button>
        <a-button :loading="syncing" size="small" type="primary" @click="manualSync">手动同步</a-button>
      </div>
    </div>

    <!-- 对账结果 -->
    <a-modal v-model:open="diffOpen" title="对账结果（AgileConfig 漂移检测）" footer-only-close>
      <template v-if="diff">
        <a-alert
          v-if="diff.drifted.length + diff.extra.length + diff.missing.length === 0"
          message="无漂移：远端与平台文件派生键值一致"
          type="success"
          show-icon
        />
        <div v-else class="space-y-3 text-sm">
          <a-alert message="检测到漂移（平台文件为真相源：下发覆盖改值项；控制台手加项不自动删除）" type="warning" show-icon />
          <div v-if="diff.missing.length">
            <div class="mb-1 font-medium">平台有、远端缺（{{ diff.missing.length }}）</div>
            <div class="font-mono text-xs text-muted-foreground">{{ diff.missing.join('、') }}</div>
          </div>
          <div v-if="diff.drifted.length">
            <div class="mb-1 font-medium">值不同（{{ diff.drifted.length }}）</div>
            <div class="font-mono text-xs text-muted-foreground">{{ diff.drifted.join('、') }}</div>
          </div>
          <div v-if="diff.extra.length">
            <div class="mb-1 font-medium">远端多出（控制台手加，{{ diff.extra.length }}）</div>
            <div class="font-mono text-xs text-muted-foreground">{{ diff.extra.join('、') }}</div>
          </div>
        </div>
      </template>
    </a-modal>

    <a-modal v-model:open="createOpen" title="签发拉取凭证" @ok="create">
      <a-form layout="vertical" class="pt-2">
        <a-form-item label="应用（平台项目名）" required>
          <a-input v-model:value="form.app" placeholder="如 demo" />
        </a-form-item>
        <a-form-item label="环境范围（逗号分隔，* = 全环境）" required>
          <a-input v-model:value="form.envs" placeholder="prod,test" />
        </a-form-item>
        <a-form-item label="备注名（可选）">
          <a-input v-model:value="form.name" placeholder="默认取应用名" />
        </a-form-item>
      </a-form>
    </a-modal>
  </div>
</template>
