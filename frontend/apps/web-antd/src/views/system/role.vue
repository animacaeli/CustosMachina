<script lang="ts" setup>
import { computed, onMounted, ref } from 'vue';

import { message } from 'ant-design-vue';

import { requestClient } from '#/api/request';

defineOptions({ name: 'SystemRole' });

interface Policy {
  act: string;
  path: string;
  role: string;
}

interface RoleRow {
  actions: string[];
  builtin: boolean;
  description: string;
  name: string;
  policies: Policy[];
  projectIds: number[];
  userCount: number;
}

interface ActionDef {
  category: string;
  desc: string;
  key: string;
}

interface ProjectItem {
  id: number;
  name: string;
}

const loading = ref(false);
const roles = ref<RoleRow[]>([]);
const catalog = ref<ActionDef[]>([]);
const projects = ref<ProjectItem[]>([]);

async function load() {
  loading.value = true;
  try {
    const [r, a, p] = await Promise.all([
      requestClient.get<RoleRow[]>('/roles'),
      requestClient.get<ActionDef[]>('/roles/actions'),
      requestClient.get<ProjectItem[]>('/projects'),
    ]);
    roles.value = r;
    catalog.value = a;
    projects.value = p;
  } finally {
    loading.value = false;
  }
}

onMounted(load);

/** 动作按类别分组（勾选面板与列表展示共用） */
const catalogByCategory = computed(() => {
  const m = new Map<string, ActionDef[]>();
  for (const a of catalog.value) {
    const list = m.get(a.category) ?? [];
    list.push(a);
    m.set(a.category, list);
  }
  return m;
});

const projectName = (id: number) =>
  projects.value.find((p) => p.id === id)?.name ?? `#${id}`;

function scopeText(record: RoleRow) {
  if (
    record.builtin ||
    record.name === 'superadmin' ||
    record.projectIds.length === 0
  ) {
    return '全局';
  }
  return record.projectIds.map(projectName).join('、');
}

// --- 角色 表单（创建/编辑：动作集 + 项目范围） ---
const formOpen = ref(false);
const formMode = ref<'create' | 'edit'>('create');
const formRole = ref<null | RoleRow>(null);
const form = ref({
  name: '',
  description: '',
  actions: [] as string[],
  projectIds: [] as number[],
});
const saving = ref(false);

function openCreate() {
  formMode.value = 'create';
  formRole.value = null;
  form.value = { name: '', description: '', actions: [], projectIds: [] };
  formOpen.value = true;
}

function openEditRole(role: RoleRow) {
  formMode.value = 'edit';
  formRole.value = role;
  form.value = {
    name: role.name,
    description: role.description,
    actions: [...role.actions],
    projectIds: [...role.projectIds],
  };
  formOpen.value = true;
}

async function submitForm() {
  if (formMode.value === 'create' && !form.value.name.trim()) {
    message.warning('请填写角色名');
    return;
  }
  saving.value = true;
  try {
    if (formMode.value === 'create') {
      await requestClient.post('/roles', form.value);
      message.success('角色已创建');
    } else if (formRole.value) {
      await requestClient.put(`/roles/${formRole.value.name}`, {
        description: form.value.description,
        actions: form.value.actions,
        projectIds: formRole.value.builtin ? [] : form.value.projectIds,
      });
      message.success('角色已更新');
    }
    formOpen.value = false;
    await load();
  } finally {
    saving.value = false;
  }
}

async function removeRole(role: RoleRow) {
  await requestClient.delete(`/roles/${role.name}`);
  message.success('角色已删除');
  await load();
}

// --- HTTP 矩阵（高级编辑，原功能保留） ---
const editOpen = ref(false);
const editRole = ref<null | RoleRow>(null);
const draft = ref<Policy[]>([]);

function openEdit(role: RoleRow) {
  editRole.value = role;
  draft.value = role.policies.map((p) => ({ ...p }));
  editOpen.value = true;
}

function addRow() {
  if (editRole.value) {
    draft.value.push({ act: 'GET', path: '', role: editRole.value.name });
  }
}

function removeRow(index: number) {
  draft.value.splice(index);
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
    <a-card title="角色管理">
      <template #extra>
        <div class="flex items-center gap-2">
          <a-button :loading="loading" @click="load"> 刷 新 </a-button>
          <a-button type="primary" @click="openCreate"> 新建角色 </a-button>
        </div>
      </template>
      <a-alert
        class="mb-4"
        message="自定义角色由动作集（业务语义粒度）与可选的项目范围组成；内置角色的动作集可调整，HTTP 矩阵仍可在「高级」中编辑。密钥明文查看（config.reveal）是与编辑分离的独立动作。"
        type="info"
        show-icon
      />
      <a-table
        :data-source="roles"
        :loading="loading"
        :pagination="false"
        row-key="name"
      >
        <a-table-column title="角色" data-index="name" :width="150">
          <template #default="{ record }">
            <a-tag color="purple">{{ record.name }}</a-tag>
            <a-tag v-if="record.builtin" color="blue">内置</a-tag>
          </template>
        </a-table-column>
        <a-table-column title="说明" data-index="description" :width="160">
          <template #default="{ text }">
            <span v-if="text">{{ text }}</span>
            <span v-else class="text-gray-400">—</span>
          </template>
        </a-table-column>
        <a-table-column title="动作集">
          <template #default="{ record }">
            <a-tag v-for="a in record.actions" :key="a" color="cyan">
              {{ a }}
            </a-tag>
            <span v-if="record.actions.length === 0" class="text-gray-400">
              （无动作）
            </span>
          </template>
        </a-table-column>
        <a-table-column title="项目范围" :width="140">
          <template #default="{ record }">
            <a-tag v-if="scopeText(record) === '全局'">全局</a-tag>
            <a-tooltip v-else :title="scopeText(record)">
              <a-tag color="geekblue">
                {{ record.projectIds.length }} 个项目
              </a-tag>
            </a-tooltip>
          </template>
        </a-table-column>
        <a-table-column title="用户数" data-index="userCount" :width="80" />
        <a-table-column title="操作" :width="230">
          <template #default="{ record }">
            <a-button
              :disabled="record.name === 'superadmin'"
              size="small"
              type="link"
              @click="openEditRole(record)"
            >
              编辑动作
            </a-button>
            <a-button
              :disabled="record.name === 'superadmin'"
              size="small"
              type="link"
              @click="openEdit(record)"
            >
              高级：矩阵
            </a-button>
            <a-popconfirm
              v-if="!record.builtin"
              :title="`删除角色 ${record.name}？有用户绑定时将拒绝`"
              @confirm="removeRole(record)"
            >
              <a-button danger size="small" type="link"> 删 除 </a-button>
            </a-popconfirm>
          </template>
        </a-table-column>
      </a-table>
    </a-card>

    <!-- 角色 表单 -->
    <a-drawer
      v-model:open="formOpen"
      :title="
        formMode === 'create'
          ? '新建自定义角色'
          : `编辑角色：${formRole?.name ?? ''}`
      "
      :width="560"
    >
      <a-form layout="vertical">
        <a-form-item label="角色名（中英文/数字，2~32 位）">
          <a-input
            v-model:value="form.name"
            :disabled="formMode === 'edit'"
            placeholder="如：运维值班、只读审计"
          />
        </a-form-item>
        <a-form-item label="说明">
          <a-input
            v-model:value="form.description"
            placeholder="如：可编辑下发配置、可进终端，不可查看密钥"
          />
        </a-form-item>
        <a-form-item label="动作集（业务语义粒度）">
          <div
            v-for="[category, actions] in catalogByCategory"
            :key="category"
            class="mb-3"
          >
            <div class="mb-1 font-medium">{{ category }}</div>
            <a-checkbox-group
              v-model:value="form.actions"
              class="flex flex-col gap-1"
            >
              <a-checkbox v-for="a in actions" :key="a.key" :value="a.key">
                <span class="font-mono text-xs">{{ a.key }}</span>
                <span class="ml-1 text-gray-500">{{ a.desc }}</span>
              </a-checkbox>
            </a-checkbox-group>
          </div>
        </a-form-item>
        <a-form-item
          v-if="formMode === 'create' || !formRole?.builtin"
          :extra="
            formMode === 'edit' && formRole?.builtin
              ? ''
              : '不选 = 全局（所有项目）；选择后该角色仅可见/可操作所选项目'
          "
          label="项目范围"
        >
          <a-select
            v-model:value="form.projectIds"
            :options="projects.map((p) => ({ label: p.name, value: p.id }))"
            mode="multiple"
            placeholder="不选 = 全局"
          />
        </a-form-item>
      </a-form>
      <template #footer>
        <div class="flex justify-end gap-2">
          <a-button @click="formOpen = false"> 取 消 </a-button>
          <a-button :loading="saving" type="primary" @click="submitForm">
            保 存
          </a-button>
        </div>
      </template>
    </a-drawer>

    <!-- HTTP 矩阵（高级） -->
    <a-modal
      v-model:open="editOpen"
      :title="`编辑 HTTP 权限矩阵：${editRole?.name ?? ''}`"
      width="640px"
      @ok="save"
    >
      <div class="mb-2">
        <a-button size="small" @click="addRow">+ 添加策略</a-button>
      </div>
      <div
        v-for="(p, i) in draft"
        :key="i"
        class="mb-2 flex items-center gap-2"
      >
        <a-input v-model:value="p.path" placeholder="/services/*" />
        <a-input
          v-model:value="p.act"
          placeholder="GET|POST"
          style="width: 160px"
        />
        <a-button danger size="small" type="link" @click="removeRow(i)">
          删除
        </a-button>
      </div>
    </a-modal>
  </div>
</template>
