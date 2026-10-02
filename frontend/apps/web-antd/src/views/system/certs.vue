<script lang="ts" setup>
import type { Cert } from '#/api/certs';

import { computed, onMounted, reactive, ref } from 'vue';

import { message } from 'ant-design-vue';

import {
  createCertApi,
  deleteCertApi,
  getCertsApi,
  renewCertApi,
  updateCertApi,
} from '#/api/certs';
import { getServerListApi } from '#/api/resources/server';

defineOptions({ name: 'SystemCerts' });

const STAGING = 'https://acme-staging-v02.api.letsencrypt.org/directory';
const PROD = 'https://acme-v02.api.letsencrypt.org/directory';

const PROVIDER_ENVS: Record<string, Array<{ key: string; label: string }>> = {
  alidns: [
    { key: 'ALICLOUD_ACCESS_KEY', label: 'AccessKey ID' },
    { key: 'ALICLOUD_SECRET_KEY', label: 'AccessKey Secret' },
  ],
  cloudflare: [{ key: 'CF_DNS_API_TOKEN', label: 'API Token' }],
  dnspod: [{ key: 'DNSPOD_API_KEY', label: 'API Key（id,token）' }],
  huaweicloud: [
    { key: 'HUAWEICLOUD_ACCESS_KEY_ID', label: 'AK' },
    { key: 'HUAWEICLOUD_SECRET_ACCESS_KEY', label: 'SK' },
  ],
  gandi: [{ key: 'GANDI_API_KEY', label: 'API Key' }],
  godaddy: [
    { key: 'GODADDY_API_KEY', label: 'Key' },
    { key: 'GODADDY_API_SECRET', label: 'Secret' },
  ],
};

const loading = ref(false);
const list = ref<Cert[]>([]);
const servers = ref<Array<{ id: number; name: string }>>([]);
const renewing = ref(0);

async function load() {
  loading.value = true;
  try {
    const [cs, ss] = await Promise.all([getCertsApi(), getServerListApi()]);
    list.value = cs;
    servers.value = ss;
  } finally {
    loading.value = false;
  }
}

onMounted(load);

const serverName = (id: number) =>
  servers.value.find((s) => s.id === id)?.name ?? `#${id}`;

function daysLeft(c: Cert) {
  if (!c.expiresAt) return null;
  return Math.floor(
    (new Date(c.expiresAt).getTime() - Date.now()) / 86_400_000,
  );
}

const columns = [
  { title: 'ID', dataIndex: 'id', width: 56 },
  { title: '名称', dataIndex: 'name' },
  { title: '域名', dataIndex: 'domains' },
  { title: 'DNS', dataIndex: 'dnsProvider', width: 100 },
  { title: '目标', key: 'target', width: 140 },
  { title: '状态', key: 'status', width: 100 },
  { title: '到期', key: 'expire', width: 100 },
  { title: '操作', key: 'action', width: 200 },
];

const formOpen = ref(false);
const saving = ref(false);
const editingId = ref<null | number>(null);
const form = reactive({
  name: '',
  domains: '',
  email: '',
  staging: false,
  dnsProvider: 'alidns',
  creds: {} as Record<string, string>,
  serverId: undefined as number | undefined,
  certPath: '',
  keyPath: '',
  enabled: true,
});

const envFields = computed(() => PROVIDER_ENVS[form.dnsProvider] ?? []);

function openCreate() {
  editingId.value = null;
  Object.assign(form, {
    name: '',
    domains: '',
    email: '',
    staging: false,
    dnsProvider: 'alidns',
    creds: {},
    serverId: undefined,
    certPath: '',
    keyPath: '',
    enabled: true,
  });
  formOpen.value = true;
}

function openEdit(c: Cert) {
  editingId.value = c.id;
  Object.assign(form, {
    name: c.name,
    domains: c.domains,
    email: c.email,
    staging: c.caDirUrl.includes('staging'),
    dnsProvider: c.dnsProvider,
    creds: {},
    serverId: c.serverId,
    certPath: c.certPath,
    keyPath: c.keyPath,
    enabled: c.enabled,
  });
  formOpen.value = true;
}

async function save() {
  if (!form.name || !form.domains || !form.email || !form.serverId) {
    message.warning('请填写完整（名称/域名/邮箱/目标主机）');
    return;
  }
  if (!editingId.value && Object.keys(form.creds).length === 0) {
    message.warning('请填写 DNS 凭证');
    return;
  }
  saving.value = true;
  try {
    const payload = {
      name: form.name,
      domains: form.domains,
      email: form.email,
      caDirUrl: form.staging ? STAGING : PROD,
      dnsProvider: form.dnsProvider,
      credentials: Object.keys(form.creds).length > 0 ? form.creds : undefined,
      serverId: form.serverId ?? 0,
      certPath: form.certPath,
      keyPath: form.keyPath,
      enabled: form.enabled,
    };
    if (editingId.value) {
      await updateCertApi(editingId.value, payload);
      message.success('已保存');
    } else {
      await createCertApi(payload);
      message.success('已创建（将在到期扫描时自动签发，也可立即签发）');
    }
    formOpen.value = false;
    await load();
  } catch (error: any) {
    message.error(error?.response?.data?.message || '保存失败');
  } finally {
    saving.value = false;
  }
}

async function renew(c: Cert) {
  renewing.value = c.id;
  try {
    await renewCertApi(c.id);
    message.success('签发/续期成功');
    await load();
  } catch (error: any) {
    message.error(error?.response?.data?.message || '签发失败');
    await load();
  } finally {
    renewing.value = 0;
  }
}

async function remove(c: Cert) {
  await deleteCertApi(c.id);
  message.success('已删除');
  await load();
}

const statusColor: Record<string, string> = {
  issued: 'green',
  failed: 'red',
  pending: 'orange',
};
const statusText: Record<string, string> = {
  issued: '已签发',
  failed: '失败',
  pending: '待签发',
};
</script>

<template>
  <div class="p-4">
    <div class="mb-3 flex items-center justify-between">
      <span class="text-muted-foreground text-xs">
        ACME DNS challenge 签发/续期（到期前 30 天自动，失败退避 24h）； 部署经
        nginx -t 守门；到期 14/7 天走通知路由（warn/critical）
      </span>
      <a-button type="primary" @click="openCreate">新建证书</a-button>
    </div>

    <a-table
      :columns="columns"
      :data-source="list"
      :loading="loading"
      :pagination="false"
      row-key="id"
      size="small"
    >
      <template #bodyCell="{ column, record }">
        <template v-if="column.key === 'target'">
          {{ serverName(record.serverId) }}
        </template>
        <template v-else-if="column.key === 'status'">
          <a-tooltip :title="record.lastError">
            <a-tag :color="statusColor[record.status]">
              {{ statusText[record.status] ?? record.status
              }}{{ record.lastError ? ' ⚠' : '' }}
            </a-tag>
          </a-tooltip>
        </template>
        <template v-else-if="column.key === 'expire'">
          <template v-if="daysLeft(record) !== null">
            <a-tag :color="(daysLeft(record) ?? 0) < 14 ? 'red' : 'green'">
              {{ daysLeft(record) }} 天
            </a-tag>
          </template>
          <template v-else>—</template>
        </template>
        <template v-else-if="column.key === 'action'">
          <a-space>
            <a-button
              size="small"
              type="primary"
              :loading="renewing === record.id"
              @click="renew(record)"
            >
              立即签发
            </a-button>
            <a-button size="small" @click="openEdit(record)">编辑</a-button>
            <a-popconfirm title="确认删除？" @confirm="remove(record)">
              <a-button danger size="small">删除</a-button>
            </a-popconfirm>
          </a-space>
        </template>
      </template>
    </a-table>

    <a-modal
      v-model:open="formOpen"
      :confirm-loading="saving"
      :title="editingId ? '编辑证书' : '新建证书'"
      width="620px"
      @ok="save"
    >
      <a-form layout="vertical">
        <a-form-item label="名称" required>
          <a-input v-model:value="form.name" placeholder="如 animacaeli 主域" />
        </a-form-item>
        <a-form-item label="域名（逗号分隔，SAN 多域名）" required>
          <a-input
            v-model:value="form.domains"
            placeholder="a.example.com,b.example.com"
          />
        </a-form-item>
        <a-form-item label="ACME 账号邮箱" required>
          <a-input v-model:value="form.email" placeholder="ops@example.com" />
        </a-form-item>
        <a-form-item label="CA">
          <a-radio-group v-model:value="form.staging">
            <a-radio :value="false">Let's Encrypt 生产</a-radio>
            <a-radio :value="true">Staging（测试，无限额）</a-radio>
          </a-radio-group>
        </a-form-item>
        <a-form-item label="DNS Provider（域名托管商）" required>
          <a-select
            v-model:value="form.dnsProvider"
            :options="
              Object.keys(PROVIDER_ENVS).map((p) => ({ label: p, value: p }))
            "
          />
        </a-form-item>
        <a-form-item
          :label="`DNS 凭证${editingId ? '（留空保留原值）' : ''}`"
          :required="!editingId"
        >
          <div class="flex flex-col gap-2">
            <div
              v-for="f in envFields"
              :key="f.key"
              class="flex items-center gap-2"
            >
              <span class="w-32 shrink-0 text-xs">{{ f.label }}</span>
              <a-input-password
                v-model:value="form.creds[f.key]"
                class="flex-1"
                :placeholder="f.key"
              />
            </div>
          </div>
        </a-form-item>
        <a-form-item label="部署目标主机" required>
          <a-select
            v-model:value="form.serverId"
            :options="servers.map((s) => ({ label: s.name, value: s.id }))"
          />
        </a-form-item>
        <a-form-item label="证书落盘路径（fullchain）" required>
          <a-input
            v-model:value="form.certPath"
            placeholder="/etc/nginx/certs/a.pem"
          />
        </a-form-item>
        <a-form-item label="私钥落盘路径" required>
          <a-input
            v-model:value="form.keyPath"
            placeholder="/etc/nginx/certs/a.key"
          />
        </a-form-item>
        <a-form-item label="启用（到期前 30 天自动续期）">
          <a-switch v-model:checked="form.enabled" />
        </a-form-item>
      </a-form>
    </a-modal>
  </div>
</template>
