<script lang="ts" setup>
import { onMounted, reactive, ref } from 'vue';

import { message } from 'ant-design-vue';

import {
  createMcpTokenApi,
  enableMcpTokenApi,
  listMcpTokensApi,
  revokeMcpTokenApi,
} from '#/api/mcp';

defineOptions({ name: 'AdminMcpTokens' });

interface McpToken {
  calls7d: number;
  enabled: boolean;
  id: number;
  lastUsedAt: null | string;
  name: string;
  role: 'admin' | 'dev';
}

const tokens = ref<McpToken[]>([]);
const loading = ref(false);
const createOpen = ref(false);
// 接入说明（端点地址按当前部署展示）
const endpointHint = `${window.location.origin}/api/mcp`;

const form = reactive({ name: '', role: 'dev' as 'admin' | 'dev' });
// 签发结果（明文只显示一次）
const issuedOpen = ref(false);
const issued = ref<null | { plaintext: string; role: string }>(null);
const saving = ref(false);

async function load() {
  loading.value = true;
  try {
    tokens.value = await listMcpTokensApi();
  } finally {
    loading.value = false;
  }
}

async function submit() {
  if (!form.name.trim()) {
    message.warning('请填写凭证名称');
    return;
  }
  saving.value = true;
  try {
    const out = await createMcpTokenApi({ ...form });
    issued.value = { plaintext: out.plaintext, role: out.role };
    issuedOpen.value = true;
    createOpen.value = false;
    form.name = '';
    await load();
  } finally {
    saving.value = false;
  }
}

async function onRevoke(t: McpToken) {
  await revokeMcpTokenApi(t.id);
  message.success(`已吊销 ${t.name}（即时生效）`);
  await load();
}

async function onEnable(t: McpToken) {
  await enableMcpTokenApi(t.id);
  message.success(`已恢复 ${t.name}`);
  await load();
}

function copy(text: string) {
  navigator.clipboard?.writeText(text);
  message.success('已复制');
}

onMounted(load);
</script>

<template>
  <div>
    <a-alert class="mb-4" type="info" show-icon>
      <template #message>MCP 接入（P6-M2）</template>
      <template #description>
        <div>
          平台能力已暴露为标准 MCP
          tools（主机/项目/构建/发布/cron/容器/上下文包，只读）。 Claude Desktop
          / IDE / 任意 MCP 客户端按以下配置接入：
        </div>
        <div class="mt-2 rounded bg-muted p-2 font-mono text-xs">
          URL: {{ endpointHint }}<br />
          Header: Authorization: Bearer &lt;凭证&gt;
        </div>
        <div class="mt-1 text-xs">
          凭证绑定角色：admin 视角含敏感上下文块；dev
          视角自动过滤（与平台用户同套语义）
        </div>
      </template>
    </a-alert>

    <div class="mb-4">
      <a-button type="primary" @click="createOpen = true">签发凭证</a-button>
    </div>

    <a-modal
      v-model:open="createOpen"
      title="签发 MCP 凭证"
      :confirm-loading="saving"
      @ok="submit"
    >
      <a-form layout="vertical" style="padding-top: 0.5rem">
        <a-form-item label="名称" required extra="用途备注，如 claude-desktop">
          <a-input v-model:value="form.name" placeholder="claude-desktop" />
        </a-form-item>
        <a-form-item
          label="角色"
          extra="admin：含敏感上下文块；dev：自动过滤（配置元信息/主机事件不可见）"
        >
          <a-radio-group
            v-model:value="form.role"
            :options="[
              { label: 'dev（受限视角）', value: 'dev' },
              { label: 'admin（完整视角）', value: 'admin' },
            ]"
          />
        </a-form-item>
      </a-form>
    </a-modal>

    <a-modal v-model:open="issuedOpen" title="凭证已签发" :footer="null">
      <a-alert
        class="mb-3"
        message="明文只显示这一次，请立即复制保存"
        type="warning"
        show-icon
      />
      <div class="flex items-center gap-2">
        <code class="flex-1 overflow-x-auto rounded bg-muted p-2 text-xs">
          {{ issued?.plaintext }}
        </code>
        <a-button size="small" @click="copy(issued?.plaintext ?? '')">
          复制
        </a-button>
      </div>
    </a-modal>

    <a-table
      :data-source="tokens"
      :loading="loading"
      :pagination="false"
      row-key="id"
      size="middle"
    >
      <a-table-column :width="60" data-index="id" title="ID" />
      <a-table-column data-index="name" title="名称" />
      <a-table-column :width="90" title="角色">
        <template #default="{ record }">
          <a-tag :color="record.role === 'admin' ? 'red' : 'blue'">
            {{ record.role }}
          </a-tag>
        </template>
      </a-table-column>
      <a-table-column :width="90" title="状态">
        <template #default="{ record }">
          <a-badge
            :status="record.enabled ? 'success' : 'error'"
            :text="record.enabled ? '启用' : '已吊销'"
          />
        </template>
      </a-table-column>
      <a-table-column :width="110" title="近 7 天调用">
        <template #default="{ record }">{{ record.calls7d }} 次</template>
      </a-table-column>
      <a-table-column :width="170" title="最近使用">
        <template #default="{ record }">
          {{
            record.lastUsedAt
              ? record.lastUsedAt.replace('T', ' ').slice(0, 16)
              : '未使用'
          }}
        </template>
      </a-table-column>
      <a-table-column :width="140" key="action" title="操作">
        <template #default="{ record }">
          <a-popconfirm
            v-if="record.enabled"
            title="确认吊销？（下一次调用立即 401）"
            @confirm="onRevoke(record)"
          >
            <a-button danger size="small" type="link">吊销</a-button>
          </a-popconfirm>
          <a-button v-else size="small" type="link" @click="onEnable(record)">
            恢复
          </a-button>
        </template>
      </a-table-column>
    </a-table>
  </div>
</template>
