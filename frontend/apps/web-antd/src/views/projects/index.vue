<script lang="ts" setup>
import type { Project } from '#/api/projects';

import { onMounted, ref } from 'vue';
import { useRouter } from 'vue-router';

import { message } from 'ant-design-vue';

import {
  createProjectApi,
  deleteProjectApi,
  getProjectsApi,
  updateProjectApi,
} from '#/api/projects';

defineOptions({ name: 'ProjectsList' });

const router = useRouter();
const loading = ref(false);
const list = ref<Project[]>([]);

const columns = [
  { title: 'ID', dataIndex: 'id', width: 60 },
  { title: '项目名', dataIndex: 'name' },
  { title: '仓库', dataIndex: 'repoPath' },
  { title: '默认分支', dataIndex: 'defaultBranch', width: 120 },
  { title: '测试槽位', dataIndex: 'testSlotCount', width: 90 },
  { title: '流量上限 %', dataIndex: 'trafficCap', width: 100 },
  { title: '操作', key: 'action', width: 200 },
];

async function load() {
  loading.value = true;
  try {
    list.value = await getProjectsApi();
  } finally {
    loading.value = false;
  }
}

onMounted(load);

const formOpen = ref(false);
const editingId = ref<null | number>(null);
const form = ref({
  name: '',
  repoUrl: '',
  repoPath: '',
  defaultBranch: 'main',
});

function openCreate() {
  editingId.value = null;
  form.value = { name: '', repoUrl: '', repoPath: '', defaultBranch: 'main' };
  formOpen.value = true;
}

function openEdit(p: Project) {
  editingId.value = p.id;
  form.value = {
    name: p.name,
    repoUrl: p.repoUrl,
    repoPath: p.repoPath,
    defaultBranch: p.defaultBranch,
  };
  formOpen.value = true;
}

async function submitForm() {
  if (!form.value.name || !form.value.repoUrl || !form.value.repoPath) {
    message.warning('请填写项目名、仓库地址与仓库路径');
    return;
  }
  if (editingId.value) {
    await updateProjectApi(editingId.value, { ...form.value });
    message.success('已更新基础信息（其余配置请进详情页）');
  } else {
    const created = await createProjectApi({ ...form.value });
    message.success('创建成功');
    formOpen.value = false;
    await load();
    router.push(`/projects/${created.id}`);
    return;
  }
  formOpen.value = false;
  await load();
}

async function onDelete(p: Project) {
  await deleteProjectApi(p.id);
  message.success(`已删除 ${p.name}`);
  await load();
}
</script>

<template>
  <div class="p-4">
    <a-card title="项目管理">
      <template #extra>
        <a-button type="primary" @click="openCreate">新增项目</a-button>
      </template>
      <a-table
        :columns="columns"
        :data-source="list"
        :loading="loading"
        :pagination="false"
        row-key="id"
        size="middle"
      >
        <template #bodyCell="{ column, record }">
          <template v-if="column.key === 'action'">
            <a-button
              size="small"
              type="link"
              @click="router.push(`/projects/${record.id}`)"
            >
              详情
            </a-button>
            <a-button size="small" type="link" @click="openEdit(record)">
              编辑
            </a-button>
            <a-popconfirm
              title="删除项目会同时删除其部署目标配置，确认？"
              @confirm="onDelete(record)"
            >
              <a-button danger size="small" type="link">删除</a-button>
            </a-popconfirm>
          </template>
        </template>
      </a-table>

      <a-modal
        v-model:open="formOpen"
        :title="editingId ? '编辑项目' : '新增项目'"
        @ok="submitForm"
      >
        <a-form layout="vertical" class="pt-2">
          <a-form-item label="项目名" required>
            <a-input
              v-model:value="form.name"
              placeholder="如 custos-machina"
            />
          </a-form-item>
          <a-form-item label="仓库地址（gitea）" required>
            <a-input
              v-model:value="form.repoUrl"
              placeholder="https://gitea.internal/org/repo"
            />
          </a-form-item>
          <a-form-item
            label="仓库路径"
            required
            extra="org/repo，调 gitea API 用"
          >
            <a-input v-model:value="form.repoPath" placeholder="org/repo" />
          </a-form-item>
          <a-form-item label="默认分支">
            <a-input v-model:value="form.defaultBranch" />
          </a-form-item>
        </a-form>
      </a-modal>
    </a-card>
  </div>
</template>
