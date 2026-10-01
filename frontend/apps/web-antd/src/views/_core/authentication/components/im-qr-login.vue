<script lang="ts" setup>
import { onBeforeUnmount, onMounted, ref, watchEffect } from 'vue';

import { useQRCode } from '@vueuse/integrations/useQRCode';
import * as ww from '@wecom/jssdk';

import { requestClient } from '#/api/request';

/**
 * IM 扫码登录统一组件：
 * - 企微：官方 @wecom/jssdk createWWLoginPanel 内嵌面板（redirect_type=callback
 *   回调 auth code，扫码直达确认页，免二次扫码），code 由父级经
 *   /auth/qrlogin/exchange 换 token（emit success）。
 * - 其他提供商（钉钉/飞书/mock）：授权页 URL 自绘二维码图片，确认后
 *   页面被 302 到回调再回前端落地页（父级处理 ?token= 路由参数）。
 */
defineOptions({ name: 'ImQrLogin' });

const emit = defineEmits<{
  success: [{ accessToken: string; refreshToken: string }];
}>();

const qrText = ref('');
const qrSrc = useQRCode(qrText, { margin: 1, width: 220 });
const loading = ref(true);
const err = ref('');
const isMock = ref(false);
const panelParams = ref<null | {
  agentid?: string;
  appid: string;
  redirectUri: string;
  state: string;
}>(null);
const panelEl = ref<HTMLDivElement>();
let panelDestroy: (() => void) | undefined;

async function exchangeCode(code: string, state: string) {
  loading.value = true;
  try {
    const result = await requestClient.post<{
      accessToken: string;
      refreshToken: string;
    }>('/auth/qrlogin/exchange', { code, state });
    emit('success', result);
  } catch (error: any) {
    // 优先取后端 message（axios 泛化文案在 response.data.message）
    err.value = error?.response?.data?.message || '登录失败，请重试';
  } finally {
    loading.value = false;
  }
}

async function load() {
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
    err.value = '二维码获取失败，请稍后重试';
  } finally {
    loading.value = false;
  }
}

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

onMounted(load);
onBeforeUnmount(() => panelDestroy?.());

defineExpose({ reload: load });
</script>

<template>
  <div class="flex w-full flex-col items-center">
    <a-spin v-if="loading" size="large" style="margin: 3.5rem 0" />

    <template v-else-if="err">
      <a-result
        class="p-0"
        status="warning"
        :sub-title="err"
        style="padding: 0"
      >
        <template #extra>
          <a-button type="primary" @click="load">重试</a-button>
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
      <a-button size="small" type="link" @click="load"> 刷新二维码 </a-button>
    </template>
  </div>
</template>
