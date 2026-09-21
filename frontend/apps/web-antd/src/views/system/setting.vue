<script lang="ts" setup>
import { onMounted, reactive, ref } from 'vue';

import { message } from 'ant-design-vue';

import { requestClient } from '#/api/request';

defineOptions({ name: 'SystemSetting' });

// --- token 有效期 ---
const ttl = reactive({ accessTtl: '30m', refreshTtl: '168h' });
const saving = ref(false);

// --- Redis ---
const redis = reactive({ addr: '', password: '', db: 0 });
const redisConfigured = ref(false);
const redisSaving = ref(false);

async function load() {
  const t = await requestClient.get<{ accessTtl: string; refreshTtl: string }>(
    '/settings/token-ttl',
  );
  ttl.accessTtl = t.accessTtl;
  ttl.refreshTtl = t.refreshTtl;
  const r = await requestClient.get<{
    addr: string;
    configured: boolean;
    db: number;
  }>('/settings/redis');
  redis.addr = r.addr;
  redis.db = r.db;
  redisConfigured.value = r.configured;
}

onMounted(load);

async function save() {
  saving.value = true;
  try {
    const r = await requestClient.put<{
      accessTtl: string;
      refreshTtl: string;
    }>('/settings/token-ttl', { ...ttl });
    ttl.accessTtl = r.accessTtl;
    ttl.refreshTtl = r.refreshTtl;
    message.success('token 有效期已更新（下次签发生效）');
  } catch {
    // 错误信息由全局拦截器弹出
  } finally {
    saving.value = false;
  }
}

async function saveRedis() {
  if (!redis.addr) {
    message.warning('请填写 Redis 地址');
    return;
  }
  redisSaving.value = true;
  try {
    await requestClient.put('/settings/redis', { ...redis });
    redisConfigured.value = true;
    message.success('Redis 已连接并保存');
  } catch {
    // 错误信息由全局拦截器弹出
  } finally {
    redisSaving.value = false;
  }
}
</script>

<template>
  <div class="flex flex-col gap-4 p-4">
    <a-card title="会话 token 有效期">
      <a-form class="max-w-[480px]" layout="inline">
        <a-form-item label="access（短效）">
          <a-input
            v-model:value="ttl.accessTtl"
            style="width: 120px"
            placeholder="30m"
          />
        </a-form-item>
        <a-form-item label="refresh（长时效）">
          <a-input
            v-model:value="ttl.refreshTtl"
            style="width: 120px"
            placeholder="168h"
          />
        </a-form-item>
      </a-form>
      <a-button :loading="saving" type="primary" @click="save">保存</a-button>
      <div class="text-muted-foreground mt-2 text-xs">
        格式如 30m / 2h / 168h；access 应短于 refresh。修改后对新签发的 token
        生效。
      </div>
    </a-card>

    <a-card title="Redis（refresh token 存储）">
      <template #extra>
        <a-tag :color="redisConfigured ? 'green' : 'orange'">
          {{ redisConfigured ? '已配置' : '内存模式' }}
        </a-tag>
      </template>
      <a-form class="max-w-[480px]" layout="vertical">
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
      <a-button :loading="redisSaving" @click="saveRedis">保存</a-button>
      <div class="text-muted-foreground mt-2 text-xs">
        未配置时 refresh token 存本进程内存（重启后需重新登录，仅适合单实例）。
      </div>
    </a-card>
  </div>
</template>
