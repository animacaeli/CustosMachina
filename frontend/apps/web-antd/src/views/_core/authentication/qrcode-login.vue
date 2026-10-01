<script lang="ts" setup>
import { onBeforeUnmount, onMounted, ref, watchEffect } from 'vue';
import { useRoute, useRouter } from 'vue-router';

import { preferences } from '@vben/preferences';
import { useAccessStore } from '@vben/stores';

import { useQRCode } from '@vueuse/integrations/useQRCode';
import * as ww from '@wecom/jssdk';

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
const err = ref('');
const isMock = ref(false);
// 企微走官方 JSSDK 内嵌登录面板（createWWLoginPanel，login_type=code）：
// 扫码直达确认页，避免整页授权页"扫码后先落网页再识别二维码"的二次扫码。
// 其他提供商：授权页 URL 用 iframe 加载或自绘二维码。
const panelParams = ref<null | {
  appid: string;
  agentid?: string;
  redirectUri: string;
  state: string;
  wwLoginType: string;
}>(null);
const panelEl = ref<HTMLDivElement>();
let panelDestroy: (() => void) | undefined;

/** 登录成功：保存双 token 并进入首页 */
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
    const home = preferences.app.defaultHomePath;
    await router.push({ path: home, replace: true });
  } catch {
    err.value = '登录信息获取失败，请重试';
    loading.value = false;
  }
}

/** 内嵌面板授权成功（拿到 auth code）：AJAX 换 token，不走 iframe 302 */
async function exchangeCode(code: string, state: string) {
  loading.value = true;
  try {
    const result = await requestClient.post<{
      accessToken: string;
      refreshToken: string;
    }>('/auth/qrlogin/exchange', { code, state });
    await enterHome(result.accessToken, result.refreshToken);
  } catch (error: any) {
    // 优先取后端 message（axios 泛化文案在 response.data.message）
    err.value = error?.response?.data?.message || '登录失败，请重试';
    loading.value = false;
  }
}

async function loadQR() {
  loading.value = true;
  err.value = '';
  panelParams.value = null;
  try {
    const result = await requestClient.get<{
      panel?: {
        agentid?: string;
        appid: string;
        redirectUri: string;
        state: string;
        wwLoginType: string;
      };
      url?: string;
    }>('/auth/qrlogin/url');
    if (result.panel) {
      panelParams.value = result.panel;
      isMock.value = false;
    } else if (result.url) {
      qrText.value = result.url;
      isMock.value = result.url.includes('provider=mock');
    }
  } catch {
    err.value = '获取二维码失败，请稍后重试';
  } finally {
    loading.value = false;
  }
}

onMounted(async () => {
  const token = route.query.token;
  if (typeof token === 'string' && token) {
    // iframe 302 回调落地（钉钉/飞书）：?token=access + #refresh=refresh
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
      return;
    }
    loading.value = false;
    return;
  }
  await loadQR();
});

// 面板容器渲染完成后初始化官方登录组件（redirect_type=callback：
// 授权成功经 onLoginSuccess 回调 auth code，不做页面跳转）
watchEffect(() => {
  if (!panelParams.value || !panelEl.value) return;
  const { appid, agentid, redirectUri, state } = panelParams.value;
  const panel = ww.createWWLoginPanel({
    el: panelEl.value,
    params: {
      login_type: ww.WWLoginType.corpApp,
      appid,
      agentid,
      redirect_uri: redirectUri,
      redirect_type: ww.WWLoginRedirectType.callback,
      state,
      panel_size: ww.WWLoginPanelSizeType.small,
    },
    onLoginFail: (res) => {
      err.value = `登录失败（${res?.errMsg || res?.errCode}），请重试`;
    },
    onLoginSuccess: ({ code }) => {
      if (code) exchangeCode(code, state);
    },
  });
  panelDestroy = () => panel.unmount();
});

onBeforeUnmount(() => panelDestroy?.());
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
          <a-button type="primary" @click="loadQR">重试</a-button>
          <a-button @click="router.push({ path: '/auth/login' })">
            账号登录
          </a-button>
        </template>
      </a-result>
    </template>

    <template v-else>
      <!-- 企微：官方 JSSDK 内嵌登录面板，扫码直达确认页 -->
      <div v-if="panelParams" ref="panelEl" class="rounded border"></div>
      <!-- 其他提供商：URL 渲染为二维码图片 -->
      <img
        v-else-if="qrSrc"
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
