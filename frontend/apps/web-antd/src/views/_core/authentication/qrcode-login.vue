<script lang="ts" setup>
import { onMounted, ref } from 'vue';
import { useRoute, useRouter } from 'vue-router';

import { preferences } from '@vben/preferences';
import { useAccessStore } from '@vben/stores';

import { useQRCode } from '@vueuse/integrations/useQRCode';

import { getAccessCodesApi } from '#/api/core';
import { requestClient } from '#/api/request';
import { useAuthStore } from '#/store';

defineOptions({ name: 'QrCodeLogin' });

const route = useRoute();
const router = useRouter();
const authStore = useAuthStore();
const accessStore = useAccessStore();

const qrText = ref('');
const qrSrc = useQRCode(qrText, { margin: 1, width: 220 });
const loading = ref(true);
const error = ref('');
const isMock = ref(false);

/** 回调落地：?token=access + #refresh=refresh → 保存会话并进入首页 */
async function handleCallbackToken(token: string) {
  loading.value = true;
  try {
    accessStore.setAccessToken(token);
    // refresh token 走 URL fragment（不进服务端日志/Referer）
    const m = window.location.hash.match(/refresh=([a-f0-9]+)/);
    if (m?.[1]) {
      accessStore.setRefreshToken(m[1]);
      history.replaceState(
        null,
        '',
        window.location.pathname + window.location.search,
      );
    }
    const [, accessCodes] = await Promise.all([
      authStore.fetchUserInfo(),
      getAccessCodesApi(),
    ]);
    accessStore.setAccessCodes(accessCodes);
    await router.push({ path: preferences.app.defaultHomePath, replace: true });
  } catch {
    error.value = '登录信息获取失败，请重试';
    loading.value = false;
  }
}

async function loadQR() {
  loading.value = true;
  error.value = '';
  try {
    const result = await requestClient.get<{ url: string }>(
      '/auth/qrlogin/url',
    );
    qrText.value = result.url;
    // mock 提供商：授权地址即回调地址，提供"模拟扫码"入口
    isMock.value = result.url.includes('provider=mock');
  } catch {
    error.value = '获取二维码失败，请稍后重试';
  } finally {
    loading.value = false;
  }
}

onMounted(async () => {
  const token = route.query.token;
  if (typeof token === 'string' && token) {
    await handleCallbackToken(token);
    return;
  }
  await loadQR();
});
</script>

<template>
  <div class="flex w-full flex-col items-center">
    <a-spin v-if="loading" size="large" style="margin: 4rem 0" />

    <template v-else-if="error">
      <a-result
        class="p-0"
        status="warning"
        :sub-title="error"
        style="padding: 0"
      >
        <template #extra>
          <a-button type="primary" @click="loadQR">重试</a-button>
          <a-button @click="router.push({ path: '/auth/login' })">
            账号登录
          </a-button>
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
      <div class="text-muted-foreground mt-3 text-sm">
        请使用企业微信扫一扫登录
      </div>
      <a-alert
        v-if="isMock"
        message="本地联调（mock 提供商）"
        type="info"
        show-icon
      >
        <template #description>
          <a :href="qrText" class="break-all text-xs">点此模拟扫码确认登录</a>
        </template>
      </a-alert>
      <a-button size="small" type="link" @click="loadQR"> 刷新二维码 </a-button>
    </template>

    <a-button
      size="small"
      type="link"
      @click="router.push({ path: '/auth/login' })"
    >
      使用账号密码登录
    </a-button>
  </div>
</template>
