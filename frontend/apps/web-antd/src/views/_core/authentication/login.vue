<script lang="ts" setup>
import { onMounted, reactive, ref } from 'vue';
import { useRouter } from 'vue-router';

import { preferences } from '@vben/preferences';
import { useAccessStore } from '@vben/stores';

import { useQRCode } from '@vueuse/integrations/useQRCode';
import { message } from 'ant-design-vue';

import { loginApi } from '#/api/core';
import { requestClient } from '#/api/request';
import { useAuthStore } from '#/store';

defineOptions({ name: 'Login' });

const router = useRouter();
const authStore = useAuthStore();
const accessStore = useAccessStore();

// --- 扫码登录（主视图） ---
const qrText = ref('');
const qrSrc = useQRCode(qrText, { margin: 1, width: 200 });
const qrLoading = ref(true);
const qrError = ref('');
const isMock = ref(false);

async function loadQR() {
  qrLoading.value = true;
  qrError.value = '';
  try {
    const result = await requestClient.get<{ url: string }>(
      '/auth/qrlogin/url',
    );
    qrText.value = result.url;
    isMock.value = result.url.includes('provider=mock');
  } catch {
    qrError.value = '二维码获取失败，请检查 IM 配置';
  } finally {
    qrLoading.value = false;
  }
}

onMounted(async () => {
  // 首启检测：未初始化跳 setup 向导
  try {
    const resp = await requestClient.get<{ needed: boolean }>('/setup/status');
    if (resp.needed) {
      await router.replace({ path: '/auth/setup' });
      return;
    }
  } catch {
    // 检测失败不阻塞登录
  }
  await loadQR();
});

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
</script>

<template>
  <div class="flex w-full max-w-[380px] flex-col items-center">
    <!-- 欢迎语 -->
    <h2 class="mb-1 text-2xl font-semibold">欢迎回来 👋🏻</h2>
    <p class="text-muted-foreground mb-6 text-sm">
      请使用企业 IM 扫一扫登录 CustosMachina
    </p>

    <!-- 扫码视图 -->
    <template v-if="!showAdmin">
      <a-spin v-if="qrLoading" class="my-14" size="large" />
      <template v-else-if="qrError">
        <a-result class="p-0" status="warning" :sub-title="qrError">
          <template #extra>
            <a-button type="primary" @click="loadQR">重试</a-button>
            <a-button @click="showAdmin = true">超管登录</a-button>
          </template>
        </a-result>
      </template>
      <template v-else>
        <img
          v-if="qrSrc"
          :src="qrSrc"
          alt="登录二维码"
          class="rounded border p-2"
        />
        <a-alert v-if="isMock" class="mt-3 w-full" show-icon type="info">
          <template #message>
            本地联调（mock 提供商）：
            <a :href="qrText" class="text-xs">点此模拟扫码确认</a>
          </template>
        </a-alert>
        <a-button class="mt-3" size="small" type="link" @click="loadQR">
          刷新二维码
        </a-button>
      </template>

      <a-button class="mt-6" @click="showAdmin = true">超管登录</a-button>
    </template>

    <!-- 超管账密视图 -->
    <template v-else>
      <a-form class="mt-4 w-full" layout="vertical" @submit.prevent>
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
      <a-button
        class="mt-4"
        size="small"
        type="link"
        @click="showAdmin = false"
      >
        ← 返回扫码登录
      </a-button>
    </template>
  </div>
</template>
