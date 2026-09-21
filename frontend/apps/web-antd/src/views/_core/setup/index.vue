<script lang="ts" setup>
import { computed, onMounted, reactive, ref } from 'vue';
import { useRoute, useRouter } from 'vue-router';

import { message } from 'ant-design-vue';

import { requestClient } from '#/api/request';

defineOptions({ name: 'SetupWizard' });

const route = useRoute();
const router = useRouter();

const loading = ref(true);
const step = ref(1); // 1 IM 提供商 2 Redis 3 超管
const submitting = ref(false);

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

// --- Step 1: IM 提供商三选一 ---
const PROVIDERS = [
  { key: 'wecom', label: '企业微信', desc: '自建应用扫码授权' },
  { key: 'dingtalk', label: '钉钉', desc: 'OAuth 扫码登录' },
  { key: 'feishu', label: '飞书', desc: '网页应用授权' },
] as const;

const PROVIDER_FIELDS: Record<
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

const im = reactive({
  provider: 'wecom' as 'dingtalk' | 'feishu' | 'wecom',
  config: {} as Record<string, string>,
});
const imFields = computed(() => PROVIDER_FIELDS[im.provider] ?? []);

function selectProvider(key: 'dingtalk' | 'feishu' | 'wecom') {
  im.provider = key;
  im.config = {};
}

async function submitIM() {
  for (const f of imFields.value) {
    if (!im.config[f.key]) {
      message.warning(`请填写 ${f.label}`);
      return;
    }
  }
  submitting.value = true;
  try {
    await requestClient.post('/setup/im', {
      config: im.config,
      enabled: true,
      provider: im.provider,
    });
    message.success('IM 提供商已配置');
    step.value = 2;
  } catch {
    // 错误信息由全局拦截器弹出
  } finally {
    submitting.value = false;
  }
}

// --- Step 2: Redis（可跳过） ---
const redis = reactive({ addr: 'localhost:6379', password: '', db: 0 });

async function submitRedis() {
  submitting.value = true;
  try {
    await requestClient.post('/setup/redis', { ...redis });
    message.success('Redis 已连接并保存');
    step.value = 3;
  } catch {
    // 错误信息由全局拦截器弹出
  } finally {
    submitting.value = false;
  }
}

function skipRedis() {
  message.info('已跳过：refresh token 将使用内存存储（单实例模式）');
  step.value = 3;
}

// --- Step 3: 本地超管 ---
const admin = reactive({
  confirmPassword: '',
  displayName: '',
  password: '',
  username: '',
});

async function submitAdmin() {
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
    message.success('初始化完成，请登录');
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
      class="w-[460px] shadow-md"
      title="CustosMachina · 系统初始化"
    >
      <a-steps
        :current="step"
        :items="[
          { title: 'IM 提供商' },
          { title: 'Redis' },
          { title: '超管账号' },
        ]"
        class="mb-6"
      />

      <!-- Step 1 -->
      <div v-if="step === 1">
        <a-alert
          class="mb-4"
          message="选择扫码登录的 IM 提供商并填入凭证（AES-256-GCM 加密落库）"
          show-icon
          type="info"
        />
        <div class="mb-4 grid grid-cols-3 gap-2">
          <div
            v-for="p in PROVIDERS"
            :key="p.key"
            class="cursor-pointer rounded border p-3 text-center transition-colors"
            :class="
              im.provider === p.key
                ? 'border-blue-500 bg-blue-50 dark:bg-blue-900/20'
                : ''
            "
            @click="selectProvider(p.key)"
          >
            <div class="font-medium">{{ p.label }}</div>
            <div class="text-muted-foreground mt-1 text-xs">{{ p.desc }}</div>
          </div>
        </div>
        <a-form layout="vertical">
          <a-form-item
            v-for="f in imFields"
            :key="f.key"
            :label="f.label"
            required
          >
            <a-input-password
              v-if="f.secret"
              v-model:value="im.config[f.key]"
            />
            <a-input v-else v-model:value="im.config[f.key]" />
          </a-form-item>
        </a-form>
        <a-button block :loading="submitting" type="primary" @click="submitIM">
          下一步
        </a-button>
      </div>

      <!-- Step 2 -->
      <div v-else-if="step === 2">
        <a-alert
          class="mb-4"
          message="指定 Redis（refresh token 存储 / 登出踢下线）。单实例可跳过，降级为内存存储。"
          show-icon
          type="info"
        />
        <a-form layout="vertical">
          <a-form-item label="地址" required>
            <a-input v-model:value="redis.addr" placeholder="localhost:6379" />
          </a-form-item>
          <a-form-item label="密码（可空）">
            <a-input-password v-model:value="redis.password" />
          </a-form-item>
          <a-form-item label="DB 编号">
            <a-input-number
              v-model:value="redis.db"
              :max="15"
              :min="0"
              class="w-full"
            />
          </a-form-item>
        </a-form>
        <div class="flex gap-2">
          <a-button :loading="submitting" type="primary" @click="submitRedis">
            连接并保存
          </a-button>
          <a-button @click="skipRedis">跳过</a-button>
          <a-button class="ml-auto" @click="step = 1">上一步</a-button>
        </div>
      </div>

      <!-- Step 3 -->
      <div v-else>
        <a-alert
          class="mb-4"
          message="创建本地超管（break-glass）。仅账密登录，用于 IM 故障时兜底。"
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
        </a-form>
        <div class="flex gap-2">
          <a-button class="mr-auto" @click="step = 2">上一步</a-button>
          <a-button :loading="submitting" type="primary" @click="submitAdmin">
            完成初始化
          </a-button>
        </div>
      </div>
    </a-card>
  </div>
</template>
