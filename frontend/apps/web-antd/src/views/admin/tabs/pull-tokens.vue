<script lang="ts" setup>
import type { PullToken } from '#/api/configs/pull';

import { onMounted, reactive, ref } from 'vue';

import { message } from 'ant-design-vue';

import {
  createPullTokenApi,
  listPullTokensApi,
  setPullTokenEnabledApi,
} from '#/api/configs/pull';

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

onMounted(load);
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
