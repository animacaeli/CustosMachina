<script lang="ts" setup>
import { onMounted, reactive, ref } from 'vue';
import { useRouter } from 'vue-router';

import { preferences } from '@vben/preferences';
import { useAccessStore } from '@vben/stores';

import { message } from 'ant-design-vue';

import { loginApi } from '#/api/core';
import { requestClient } from '#/api/request';
import { useAuthStore } from '#/store';

import ImQrLogin from './components/im-qr-login.vue';

defineOptions({ name: 'Login' });

const router = useRouter();
const authStore = useAuthStore();
const accessStore = useAccessStore();

// 扫码成功：保存双 token 并进入首页
async function onQrSuccess(payload: {
  accessToken: string;
  refreshToken: string;
}) {
  accessStore.setAccessToken(payload.accessToken);
  accessStore.setRefreshToken(payload.refreshToken);
  await authStore.fetchUserInfo();
  await router.push({ path: preferences.app.defaultHomePath, replace: true });
}

// --- 超管登录（账密） ---
const showAdmin = ref(false);
const adminLoading = ref(false);
const form = reactive({ password: '', username: '' });

async function adminLogin() {
  if (!form.username || !form.password) {
    message.warning('请输入账号与密码');
    return;
  }
  adminLoading.value = true;
  try {
    const result = await loginApi({
      password: form.password,
      username: form.username,
    });
    if (result.accessToken) {
      accessStore.setAccessToken(result.accessToken);
      if (result.refreshToken) {
        accessStore.setRefreshToken(result.refreshToken);
      }
      await authStore.fetchUserInfo();
      await router.push({
        path: preferences.app.defaultHomePath,
        replace: true,
      });
    }
  } catch {
    // 错误信息由全局拦截器弹出
  } finally {
    adminLoading.value = false;
  }
}

onMounted(async () => {
  // 首启检测：未初始化跳 setup 向导
  try {
    const resp = await requestClient.get<{ needed: boolean }>('/setup/status');
    if (resp.needed) {
      await router.replace({ path: '/auth/setup' });
    }
  } catch {
    // 检测失败不阻塞登录
  }
});
</script>

<template>
  <div class="flex w-full max-w-[380px] flex-col items-center">
    <!-- 扫码视图（说明文案在 ImQrLogin 内按提供商显示） -->
    <template v-if="!showAdmin">
      <h2 class="mb-4 self-start text-2xl font-semibold">欢迎回来 👋🏻</h2>
      <ImQrLogin @success="onQrSuccess" />
      <a-button class="mt-2" @click="showAdmin = true">超管登录</a-button>
    </template>

    <!-- 超管账密视图 -->
    <template v-else>
      <h2 class="mb-1 text-2xl font-semibold">管理员登录</h2>
      <p class="text-muted-foreground mb-6 text-sm">仅平台管理员使用</p>
      <a-form class="w-full" layout="vertical" @submit.prevent>
        <a-form-item label="登录账号" required>
          <a-input
            v-model:value="form.username"
            placeholder="本地超管账号"
            @press-enter="adminLogin"
          />
        </a-form-item>
        <a-form-item label="密码" required>
          <a-input-password
            v-model:value="form.password"
            @press-enter="adminLogin"
          />
        </a-form-item>
        <a-button
          block
          :loading="adminLoading"
          type="primary"
          @click="adminLogin"
        >
          登 录
        </a-button>
      </a-form>
      <a-button size="small" type="link" @click="showAdmin = false">
        ← 返回扫码登录
      </a-button>
    </template>
  </div>
</template>
