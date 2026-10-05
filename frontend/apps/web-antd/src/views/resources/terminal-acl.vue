<script lang="ts" setup>
import { ref, watch } from 'vue';

import { message } from 'ant-design-vue';

import {
  getTerminalAclsApi,
  setTerminalAclsApi,
} from '#/api/resources/terminal-audit';
import { getUserListApi } from '#/api/system/user';

/**
 * 主机终端授权（P6-M6 堡垒机细粒度）：勾选可开该机终端的本地账号。
 * 角色通配策略（admin）不在此管理；dev 默认无终端权限，授权后生效。
 */

defineOptions({ name: 'TerminalAcl' });

const props = defineProps<{
  serverId?: number;
  serverName?: string;
}>();

const open = defineModel<boolean>('open');

const users = ref<{ username: string; displayName: string; status: string }[]>(
  [],
);
const selected = ref<string[]>([]);
const saving = ref(false);

watch(
  () => open.value,
  async (v) => {
    if (!v || !props.serverId) return;
    selected.value = await getTerminalAclsApi(props.serverId);
    if (users.value.length === 0) {
      const list = await getUserListApi();
      // 仅本地账号可授权（IM 账号 username 为空，无稳定 sub）
      users.value = list
        .filter((u) => (u.username ?? '') !== '')
        .map((u) => ({
          username: u.username as string,
          displayName: u.displayName,
          status: u.status,
        }));
    }
  },
);

async function save() {
  if (!props.serverId) return;
  saving.value = true;
  try {
    await setTerminalAclsApi(props.serverId, selected.value);
    message.success('授权已保存（即时生效）');
    open.value = false;
  } catch (error: any) {
    message.error(error?.response?.data?.message ?? '保存失败');
  } finally {
    saving.value = false;
  }
}
</script>

<template>
  <a-modal
    v-model:open="open"
    :title="`终端授权 · ${props.serverName ?? ''}`"
    :confirm-loading="saving"
    @ok="save"
  >
    <div class="mb-2 text-xs text-muted-foreground">
      勾选可打开该主机 Web 终端的本地账号（角色权限不受影响：admin 始终可开；
      dev 授权后可开，未授权 403 并留审计）。会话全程录制可回放。
    </div>
    <a-select
      v-model:value="selected"
      :options="
        users.map((u) => ({
          value: u.username,
          label: `${u.displayName || u.username}（${u.username}）${u.status === 'disabled' ? ' · 已禁用' : ''}`,
        }))
      "
      allow-clear
      mode="multiple"
      placeholder="选择可开终端的账号"
      style="width: 100%"
    />
  </a-modal>
</template>
