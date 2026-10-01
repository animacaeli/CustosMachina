<script lang="ts" setup>
import { onMounted, ref } from 'vue';
import { useRoute, useRouter } from 'vue-router';

import { preferences } from '@vben/preferences';
import { useAccessStore } from '@vben/stores';

import { getAccessCodesApi } from '#/api/core';
import { useAuthStore } from '#/store';

import ImQrLogin from './components/im-qr-login.vue';

defineOptions({ name: 'QrCodeLogin' });

const route = useRoute();
const router = useRouter();
const authStore = useAuthStore();
const accessStore = useAccessStore();

const loading = ref(false);
const err = ref('');

/** 登录成功：保存双 token、拉取用户信息并进入首页 */
async function enterHome(accessToken: string, refreshToken?: string) {
  loading.value = true;
  try {
    accessStore.setAccessToken(accessToken);
    if (refreshToken) {
      accessStore.setRefreshToken(refreshToken);
    }
    const [, accessCodes] = await Promise.all([
      authStore.fetchUserInfo(),
      getAccessCodesApi(),
    ]);
    accessStore.setAccessCodes(accessCodes);
    await router.push({ path: preferences.app.defaultHomePath, replace: true });
  } catch {
    err.value = '登录信息获取失败，请重试';
    loading.value = false;
  }
}

onMounted(async () => {
  // 授权页 302 回调落地（钉钉/飞书 iframe 路径）：
  // ?token=access + #refresh=refresh（fragment 不进服务端日志/Referer）
  const token = route.query.token;
  if (typeof token === 'string' && token) {
    const m = window.location.hash.match(/refresh=([a-f0-9]+)/);
    await enterHome(token, m?.[1]);
    return;
  }
  // 回调失败重定向带回的 error（落在授权 iframe 内，顶层刷新二维码）
  const errParam = route.query.error;
  if (typeof errParam === 'string' && errParam) {
    err.value = errParam;
    if (window.self !== window.top) {
      window.top?.location.reload();
    }
  }
});
</script>

<template>
  <div class="flex w-full flex-col items-center">
    <a-spin v-if="loading" size="large" style="margin: 4rem 0" />

    <template v-else-if="err">
      <a-result
        class="p-0"
        status="warning"
        :sub-title="err"
        style="padding: 0"
      >
        <template #extra>
          <!-- 清空 err → 组件重新挂载并自动加载新二维码 -->
          <a-button type="primary" @click="err = ''">重试</a-button>
          <a-button @click="router.push({ path: '/auth/login' })">
            账号登录
          </a-button>
        </template>
      </a-result>
    </template>

    <ImQrLogin
      v-else
      @success="(p) => enterHome(p.accessToken, p.refreshToken)"
    />

    <a-button
      size="small"
      type="link"
      @click="router.push({ path: '/auth/login' })"
    >
      使用账号密码登录
    </a-button>
  </div>
</template>
