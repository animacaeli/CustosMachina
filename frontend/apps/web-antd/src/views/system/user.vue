<script lang="ts" setup>
import { onMounted, reactive, ref } from 'vue';

import { message } from 'ant-design-vue';

import {
  createUserApi,
  getUserListApi,
  type PlatformUser,
  updateUserRolesApi,
} from '#/api/system/user';

defineOptions({ name: 'SystemUser' });

const BUILTIN_ROLES = ['ops', 'dev', 'guest'];

const loading = ref(false);
const list = ref<PlatformUser[]>([]);

async function load() {
  loading.value = true;
  try {
    list.value = await getUserListApi();
  } finally {
    loading.value = false;
  }
}

onMounted(load);

// --- 创建用户 ---
const createOpen = ref(false);
const createForm = reactive({ displayName: '', username: '', roles: 'guest' });

function openCreate() {
  createForm.displayName = '';
  createForm.username = '';
  createForm.roles = 'guest';
  createOpen.value = true;
}

async function submitCreate() {
  if (!createForm.displayName) {
    message.warning('请填写显示名');
    return;
  }
  await createUserApi({ ...createForm });
  message.success('创建成功');
  createOpen.value = false;
  await load();
}

// --- 调整角色 ---
const editOpen = ref(false);
const editUser = ref<null | PlatformUser>(null);
const editRoles = ref<string[]>([]);

function openEdit(user: PlatformUser) {
  editUser.value = user;
  editRoles.value = user.roles ? user.roles.split(',').map((r) => r.trim()) : [];
  editOpen.value = true;
}

async function submitRoles() {
  if (!editUser.value) return;
  await updateUserRolesApi(editUser.value.id, editRoles.value.join(','));
  message.success('已更新角色');
  editOpen.value = false;
  await load();
}
</script>

<template>
  <div class="p-4">
    <a-card title="平台用户">
      <template #extra>
        <a-button type="primary" @click="openCreate">新建用户</a-button>
      </template>
      <a-table
        :data-source="list"
        :loading="loading"
        :pagination="false"
        row-key="id"
      >
        <a-table-column title="ID" data-index="id" :width="60" />
        <a-table-column title="显示名" data-index="displayName" />
        <a-table-column title="登录账号" data-index="username">
          <template #default="{ text }">{{ text || '—（IM 用户）' }}</template>
        </a-table-column>
        <a-table-column title="角色" data-index="roles">
          <template #default="{ text }">
            <a-tag v-for="r in (text || '').split(',')" :key="r" color="blue">
              {{ r }}
            </a-tag>
          </template>
        </a-table-column>
        <a-table-column title="状态" data-index="status" :width="90">
          <template #default="{ text }">
            <a-tag :color="text === 'active' ? 'green' : 'red'">{{ text }}</a-tag>
          </template>
        </a-table-column>
        <a-table-column title="操作" :width="120">
          <template #default="{ record }">
            <a-button size="small" type="link" @click="openEdit(record)">
              调整角色
            </a-button>
          </template>
        </a-table-column>
      </a-table>
    </a-card>

    <a-modal v-model:open="createOpen" title="新建用户" @ok="submitCreate">
      <a-form layout="vertical" class="pt-2">
        <a-form-item label="显示名" required>
          <a-input v-model:value="createForm.displayName" placeholder="如：张三" />
        </a-form-item>
        <a-form-item label="登录账号（可留空，IM 用户无本地账号）">
          <a-input v-model:value="createForm.username" />
        </a-form-item>
        <a-form-item label="角色">
          <a-select v-model:value="createForm.roles" :options="BUILTIN_ROLES.map((r) => ({ label: r, value: r }))" />
        </a-form-item>
      </a-form>
    </a-modal>

    <a-modal v-model:open="editOpen" :title="`调整角色：${editUser?.displayName ?? ''}`" @ok="submitRoles">
      <a-checkbox-group v-model:value="editRoles" class="flex flex-col gap-2 py-2">
        <a-checkbox v-for="r in BUILTIN_ROLES" :key="r" :value="r">{{ r }}</a-checkbox>
      </a-checkbox-group>
    </a-modal>
  </div>
</template>
