<script lang="ts" setup>
import { onMounted, ref } from 'vue';

import { message } from 'ant-design-vue';

import { requestClient } from '#/api/request';

defineOptions({ name: 'SystemRole' });

interface Policy {
  act: string;
  path: string;
  role: string;
}

interface RoleWithPolicies {
  builtin: boolean;
  name: string;
  policies: Policy[];
}

const loading = ref(false);
const roles = ref<RoleWithPolicies[]>([]);

async function load() {
  loading.value = true;
  try {
    roles.value = await requestClient.get<RoleWithPolicies[]>('/roles');
  } finally {
    loading.value = false;
  }
}

onMounted(load);

// 编辑某角色矩阵（admin 不可编辑）
const editOpen = ref(false);
const editRole = ref<null | RoleWithPolicies>(null);
const draft = ref<Policy[]>([]);

function openEdit(role: RoleWithPolicies) {
  editRole.value = role;
  draft.value = role.policies.map((p) => ({ ...p }));
  editOpen.value = true;
}

function addRow() {
  draft.value.push({ act: 'GET', path: '', role: editRole.value!.name });
}

function removeRow(index: number) {
  draft.value.splice(index, 1);
}

async function save() {
  if (!editRole.value) return;
  if (draft.value.some((p) => !p.path)) {
    message.warning('资源路径不能为空');
    return;
  }
  await requestClient.put(`/roles/${editRole.value.name}/policies`, {
    policies: draft.value,
  });
  message.success('已保存权限矩阵');
  editOpen.value = false;
  await load();
}
</script>

<template>
  <div class="p-4">
    <a-card title="角色权限矩阵">
      <a-alert
        class="mb-4"
        message="admin（本地超管）默认放行全部资源，策略不可编辑；其余角色权限即刻生效。"
        type="info"
        show-icon
      />
      <a-table :data-source="roles" :loading="loading" :pagination="false" row-key="name">
        <a-table-column title="角色" data-index="name" :width="120">
          <template #default="{ text }">
            <a-tag color="purple">{{ text }}</a-tag>
          </template>
        </a-table-column>
        <a-table-column title="权限数" :width="80">
          <template #default="{ record }">{{ record.policies.length }}</template>
        </a-table-column>
        <a-table-column title="权限概览">
          <template #default="{ record }">
            <a-tag v-for="p in record.policies.slice(0, 6)" :key="`${p.path}:${p.act}`">
              {{ p.path }} · {{ p.act }}
            </a-tag>
            <span v-if="record.policies.length > 6">等 {{ record.policies.length }} 条</span>
            <span v-if="record.policies.length === 0">（无权限）</span>
          </template>
        </a-table-column>
        <a-table-column title="操作" :width="100">
          <template #default="{ record }">
            <a-button
              :disabled="record.name === 'admin'"
              size="small"
              type="link"
              @click="openEdit(record)"
            >
              编辑矩阵
            </a-button>
          </template>
        </a-table-column>
      </a-table>
    </a-card>

    <a-modal v-model:open="editOpen" :title="`编辑权限矩阵：${editRole?.name ?? ''}`" width="640px" @ok="save">
      <div class="mb-2">
        <a-button size="small" @click="addRow">+ 添加策略</a-button>
      </div>
      <div v-for="(p, i) in draft" :key="i" class="mb-2 flex items-center gap-2">
        <a-input v-model:value="p.path" placeholder="/services/*" />
        <a-input v-model:value="p.act" placeholder="GET|POST" style="width: 160px" />
        <a-button danger size="small" type="link" @click="removeRow(i)">删除</a-button>
      </div>
    </a-modal>
  </div>
</template>
