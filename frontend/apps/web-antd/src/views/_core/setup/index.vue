<script lang="ts" setup>
import type { RouteLocationNormalizedGeneric } from 'vue-router';

import { onMounted, reactive, ref } from 'vue';
import { useRoute, useRouter } from 'vue-router';

import { message } from 'ant-design-vue';

import { requestClient } from '#/api/request';

defineOptions({ name: 'SetupWizard' });

const route: RouteLocationNormalizedGeneric = useRoute();
const router = useRouter();

const loading = ref(true);
const submitting = ref(false);

const form = reactive({
  confirmPassword: '',
  displayName: '',
  password: '',
  username: '',
});

/** 首启检测：已完成初始化则直接回登录页 */
onMounted(async () => {
  try {
    const resp = await requestClient.get<{ needed: boolean }>('/setup/status');
    if (!resp.needed) {
      await router.replace({ path: '/auth/login', query: route.query });
    }
  } finally {
    loading.value = false;
  }
});

async function submit() {
  if (!form.username || form.username.length < 3) {
    message.warning('登录账号至少 3 个字符');
    return;
  }
  if (form.password.length < 8) {
    message.warning('密码至少 8 位');
    return;
  }
  if (form.password !== form.confirmPassword) {
    message.warning('两次输入的密码不一致');
    return;
  }
  submitting.value = true;
  try {
    await requestClient.post('/setup/admin', {
      displayName: form.displayName || undefined,
      password: form.password,
      username: form.username,
    });
    message.success('初始化完成，请使用本地超管登录');
    await router.replace({ path: '/auth/login' });
  } finally {
    submitting.value = false;
  }
}
</script>

<template>
  <div
    class="flex h-full items-center justify-center bg-white dark:bg-transparent"
  >
    <a-spin v-if="loading" size="large" />
    <a-card
      v-else
      class="w-[420px] shadow-md"
      title="CustosMachina · 系统初始化"
    >
      <a-alert
        class="mb-4"
        message="首次启动需创建本地超管账号（break-glass）。该账号长期保留，用于 IM 登录故障时兜底。"
        type="info"
        show-icon
      />
      <a-form layout="vertical" @submit.prevent>
        <a-form-item label="登录账号" required>
          <a-input v-model:value="form.username" placeholder="如 root" />
        </a-form-item>
        <a-form-item label="显示名（可选）">
          <a-input
            v-model:value="form.displayName"
            placeholder="如 平台管理员"
          />
        </a-form-item>
        <a-form-item label="密码（至少 8 位）" required>
          <a-input-password v-model:value="form.password" />
        </a-form-item>
        <a-form-item label="确认密码" required>
          <a-input-password v-model:value="form.confirmPassword" />
        </a-form-item>
        <a-button block :loading="submitting" type="primary" @click="submit">
          完成初始化
        </a-button>
      </a-form>
    </a-card>
  </div>
</template>
