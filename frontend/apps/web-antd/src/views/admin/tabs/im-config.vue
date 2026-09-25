<script lang="ts" setup>
import { computed, onMounted, reactive, ref } from 'vue';

import { message } from 'ant-design-vue';

import { requestClient } from '#/api/request';

defineOptions({ name: 'SystemIM' });

const PROVIDERS = [
  { key: 'wecom', label: '企业微信' },
  { key: 'dingtalk', label: '钉钉' },
  { key: 'feishu', label: '飞书' },
] as const;

const FIELDS: Record<
  string,
  { key: string; label: string; secret?: boolean }[]
> = {
  dingtalk: [
    { key: 'clientKey', label: 'AppKey / ClientId' },
    { key: 'clientSecret', label: 'AppSecret', secret: true },
  ],
  feishu: [
    { key: 'appId', label: 'App ID' },
    { key: 'appSecret', label: 'App Secret', secret: true },
  ],
  wecom: [
    { key: 'corpId', label: '企业 ID（CorpID）' },
    { key: 'agentId', label: '应用 AgentId' },
    { key: 'secret', label: '应用 Secret', secret: true },
  ],
};

interface ProviderStatus {
  configured: boolean;
  enabled: boolean;
  provider: string;
}

const loading = ref(false);
const statuses = ref<ProviderStatus[]>([]);
const provider = ref('wecom');
const form = reactive<Record<string, string>>({});
const enabled = ref(true);
const saving = ref(false);
const testing = ref(false);

const fields = computed(() => FIELDS[provider.value] ?? []);
const currentStatus = computed(() =>
  statuses.value.find((s) => s.provider === provider.value),
);

async function load() {
  loading.value = true;
  try {
    statuses.value = await requestClient.get<ProviderStatus[]>('/im-configs');
    const active = statuses.value.find((s) => s.enabled);
    if (active) {
      provider.value = active.provider;
      enabled.value = true;
    }
  } catch {
    // 错误信息由全局拦截器弹出
  } finally {
    loading.value = false;
  }
}

onMounted(load);

function switchProvider(key: string) {
  provider.value = key;
  for (const f of FIELDS[key] ?? []) {
    form[f.key] = '';
  }
}

const payload = () => ({ config: { ...form }, enabled: enabled.value });

async function verify() {
  testing.value = true;
  try {
    const filled = Object.values(form).some(Boolean);
    await requestClient.post(
      `/im-configs/${provider.value}/verify`,
      filled ? payload() : {},
    );
    message.success('凭证连通性测试通过');
  } catch {
    // 错误信息由全局拦截器弹出
  } finally {
    testing.value = false;
  }
}

async function save() {
  for (const f of fields.value) {
    if (!form[f.key]) {
      message.warning(`请填写 ${f.label}`);
      return;
    }
  }
  saving.value = true;
  try {
    await requestClient.put(`/im-configs/${provider.value}`, payload());
    message.success('已保存（AES-256-GCM 加密落库，启用后其他家自动禁用）');
    await load();
  } catch {
    // 错误信息由全局拦截器弹出
  } finally {
    saving.value = false;
  }
}
</script>

<template>
  <div>
    <a-card :loading="loading" title="IM 扫码登录">
      <a-alert show-icon type="info" style="margin-bottom: 1rem">
        <template #message>
          同时只启用一家；扫码回调地址需公网 HTTPS（详见部署文档）。当前：
          <a-tag v-if="currentStatus?.enabled" color="green">已启用</a-tag>
          <a-tag v-else color="orange">未启用</a-tag>
        </template>
      </a-alert>

      <a-radio-group
        :value="provider"
        @change="(e: any) => switchProvider(e.target.value)"
      >
        <a-radio-button v-for="p in PROVIDERS" :key="p.key" :value="p.key">
          {{ p.label }}
        </a-radio-button>
      </a-radio-group>

      <a-form style="max-width: 480px" layout="vertical">
        <a-form-item v-for="f in fields" :key="f.key" :label="f.label" required>
          <a-input-password v-if="f.secret" v-model:value="form[f.key]" />
          <a-input v-else v-model:value="form[f.key]" />
        </a-form-item>
        <a-form-item label="启用扫码登录">
          <a-switch v-model:checked="enabled" />
        </a-form-item>
        <a-space>
          <a-button :loading="testing" @click="verify">连通性测试</a-button>
          <a-button :loading="saving" type="primary" @click="save">
            保存
          </a-button>
        </a-space>
      </a-form>
    </a-card>
  </div>
</template>
