<script lang="ts" setup>
import { onMounted, reactive, ref } from 'vue';

import { message } from 'ant-design-vue';

import { requestClient } from '#/api/request';

defineOptions({ name: 'SystemIM' });

const loading = ref(false);
const status = ref<{ configured: boolean; corpId?: string; enabled: boolean }>({
  configured: false,
  enabled: false,
});

const form = reactive({ corpId: '', agentId: '', secret: '', enabled: true });
const saving = ref(false);
const testing = ref(false);

async function load() {
  loading.value = true;
  try {
    const s = await requestClient.get<{
      configured: boolean;
      corpId?: string;
      enabled: boolean;
    }>('/im-configs/wecom');
    status.value = s;
    form.corpId = s.corpId ?? '';
    form.enabled = s.enabled;
  } finally {
    loading.value = false;
  }
}

onMounted(load);

async function verify() {
  testing.value = true;
  try {
    // 表单填了凭证就测新凭证，否则测已保存凭证
    const body = form.corpId && form.agentId && form.secret ? { ...form } : {};
    await requestClient.post('/im-configs/wecom/verify', body);
    message.success('凭证连通性测试通过');
  } catch {
    // 错误信息由全局拦截器弹出
  } finally {
    testing.value = false;
  }
}

async function save() {
  saving.value = true;
  try {
    await requestClient.put('/im-configs/wecom', { ...form });
    message.success('已保存（AES-256-GCM 加密落库）');
    await load();
  } catch {
    // 错误信息由全局拦截器弹出
  } finally {
    saving.value = false;
  }
}
</script>

<template>
  <div class="p-4">
    <a-card title="登录配置 · 企业微信扫码" :loading="loading">
      <a-alert class="mb-4" type="info" show-icon>
        <template #message>
          扫码登录需配置企业微信自建应用；回调地址需公网 HTTPS（详见部署文档）。
          当前状态：
          <a-tag :color="status.configured ? 'green' : 'orange'">
            {{ status.configured ? '已配置' : '未配置' }}
          </a-tag>
          <a-tag :color="status.enabled ? 'blue' : 'red'">
            {{ status.enabled ? '已启用' : '未启用' }}
          </a-tag>
        </template>
      </a-alert>

      <a-form layout="vertical" class="max-w-[480px]">
        <a-form-item label="企业 ID（CorpID）" required>
          <a-input v-model:value="form.corpId" placeholder="ww 开头的企业 ID" />
        </a-form-item>
        <a-form-item label="应用 AgentId" required>
          <a-input
            v-model:value="form.agentId"
            placeholder="自建应用的 AgentId"
          />
        </a-form-item>
        <a-form-item label="应用 Secret" required>
          <a-input-password
            v-model:value="form.secret"
            placeholder="已保存的 Secret 不会回显，留空表示沿用"
          />
        </a-form-item>
        <a-form-item label="启用扫码登录">
          <a-switch v-model:checked="form.enabled" />
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
