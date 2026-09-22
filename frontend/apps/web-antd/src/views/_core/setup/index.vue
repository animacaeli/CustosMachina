<script lang="ts" setup>
import { onMounted, reactive, ref } from 'vue';
import { useRoute, useRouter } from 'vue-router';

import { message } from 'ant-design-vue';

import { requestClient } from '#/api/request';

defineOptions({ name: 'SetupWizard' });

const route = useRoute();
const router = useRouter();

const loading = ref(true);
const submitting = ref(false);
const admin = reactive({
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
  if (!admin.username || admin.username.length < 3) {
    message.warning('登录账号至少 3 个字符');
    return;
  }
  if (admin.password.length < 8) {
    message.warning('密码至少 8 位');
    return;
  }
  if (admin.password !== admin.confirmPassword) {
    message.warning('两次输入的密码不一致');
    return;
  }
  submitting.value = true;
  try {
    await requestClient.post('/setup/admin', {
      displayName: admin.displayName || undefined,
      password: admin.password,
      username: admin.username,
    });
    message.success('初始化完成，请使用超管账号登录');
    await router.replace({ path: '/auth/login' });
  } catch {
    // 错误信息由全局拦截器弹出
  } finally {
    submitting.value = false;
  }
}
</script>

<template>
  <div class="flex h-full items-center justify-center">
    <a-spin v-if="loading" size="large" />
    <a-card
      v-else
      class="w-[420px] shadow-md"
      title="CustosMachina · 系统初始化"
    >
      <a-alert
        class="mb-4"
        message="首次启动需创建本地超管（break-glass）。仅账密登录，用于 IM 登录故障时兜底；其余配置登录后进入「管理后台」设置。"
        show-icon
        type="info"
      />
      <a-form layout="vertical">
        <a-form-item label="登录账号" required>
          <a-input v-model:value="admin.username" placeholder="如 root" />
        </a-form-item>
        <a-form-item label="显示名（可选）">
          <a-input
            v-model:value="admin.displayName"
            placeholder="如 平台管理员"
          />
        </a-form-item>
        <a-form-item label="密码（至少 8 位）" required>
          <a-input-password v-model:value="admin.password" />
        </a-form-item>
        <a-form-item label="确认密码" required>
          <a-input-password v-model:value="admin.confirmPassword" />
        </a-form-item>
        <a-button block :loading="submitting" type="primary" @click="submit">
          完成初始化
        </a-button>
      </a-form>
    </a-card>
  </div>
</template>
