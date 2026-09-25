<script lang="ts" setup>
import type { Project } from '#/api/projects';

import { computed, onMounted, reactive, ref } from 'vue';

import { message } from 'ant-design-vue';

import {
  createProjectApi,
  deleteProjectApi,
  getProjectsApi,
  updateProjectApi,
} from '#/api/projects';

import BuildDrawer from './build-drawer.vue';
import PolicyDrawer from './policy-drawer.vue';
import ProjectDetailDrawer from './project-detail-drawer.vue';
import ReleaseDrawer from './release-drawer.vue';
import SlotDrawer from './slot-drawer.vue';

defineOptions({ name: 'EnvPage' });

const props = defineProps<{ env: 'canary' | 'prod' | 'test' }>();

interface EnvMeta {
  actions: Array<'build' | 'policy' | 'release' | 'slot'>;
  desc: string;
  title: string;
}

const envMeta: Record<'canary' | 'prod' | 'test', EnvMeta> = {
  canary: {
    actions: ['build', 'release', 'policy'],
    desc: '打 canary-yyyymmdd-缩写 标签自动构建；发布与流量策略在此操作',
    title: '灰度环境',
  },
  prod: {
    actions: ['build', 'release'],
    desc: '打 v* 标签自动构建，构建通过后手动发布（仅管理员）',
    title: '正式环境',
  },
  test: {
    actions: ['build', 'slot'],
    desc: '全自动 CI/CD：占用槽位后 push 分支即自动重建测试环境',
    title: '测试环境',
  },
};
const meta = computed(() => envMeta[props.env]);
const actionLabel: Record<string, string> = {
  build: '构建',
  policy: '策略',
  release: '发布',
  slot: '槽位',
};

const loading = ref(false);
const projects = ref<Project[]>([]);

async function load() {
  loading.value = true;
  try {
    projects.value = await getProjectsApi();
  } finally {
    loading.value = false;
  }
}

onMounted(load);

// ---- 行内抽屉（同一时刻只开一个，作用于当前行项目） ----
const currentId = ref<number | undefined>();
const currentProject = computed(() =>
  projects.value.find((p) => p.id === currentId.value),
);
const openDrawer = ref<'' | 'build' | 'detail' | 'policy' | 'release' | 'slot'>(
  '',
);

function open(
  which: 'build' | 'detail' | 'policy' | 'release' | 'slot',
  p: Project,
) {
  currentId.value = p.id;
  openDrawer.value = which;
}

// ---- 新增 / 编辑（基础信息） ----
const formOpen = ref(false);
const editingId = ref<null | number>(null);
const form = reactive({
  defaultBranch: 'main',
  name: '',
  repoPath: '',
  repoUrl: '',
});

function openCreate() {
  editingId.value = null;
  Object.assign(form, {
    name: '',
    repoUrl: '',
    repoPath: '',
    defaultBranch: 'main',
  });
  formOpen.value = true;
}

function openEdit(p: Project) {
  editingId.value = p.id;
  Object.assign(form, {
    name: p.name,
    repoUrl: p.repoUrl,
    repoPath: p.repoPath,
    defaultBranch: p.defaultBranch,
  });
  formOpen.value = true;
}

async function submitForm() {
  if (!form.name || !form.repoUrl || !form.repoPath) {
    message.warning('请填写项目名、仓库地址与仓库路径');
    return;
  }
  if (editingId.value) {
    await updateProjectApi(editingId.value, { ...form });
    message.success('已更新（其余配置见"详情"）');
  } else {
    const created = await createProjectApi({ ...form });
    message.success('创建成功，请在详情里完成部署目标与通知配置');
    formOpen.value = false;
    await load();
    currentId.value = created.id;
    openDrawer.value = 'detail';
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
    <a-card>
      <template #title>
        <span class="text-base">{{ meta.title }}</span>
        <span class="text-muted-foreground ml-2 text-xs font-normal">{{
          meta.desc
        }}</span>
      </template>
      <template #extra>
        <a-button type="primary" @click="openCreate">新增项目</a-button>
      </template>
      <a-table
        :columns="[
          { title: '项目', key: 'name' },
          { title: '默认分支', dataIndex: 'defaultBranch', width: 110 },
          { title: '环境操作', key: 'envActions', width: 220 },
          { title: '管理', key: 'manage', width: 170 },
        ]"
        :data-source="projects"
        :loading="loading"
        :pagination="false"
        row-key="id"
        size="middle"
      >
        <template #bodyCell="{ column, record }">
          <template v-if="column.key === 'name'">
            <div class="font-medium">{{ record.name }}</div>
            <div class="text-muted-foreground text-xs">
              {{ record.repoPath }}
            </div>
          </template>
          <template v-else-if="column.key === 'envActions'">
            <a-button
              v-for="act in meta.actions"
              :key="act"
              :type="act === 'release' ? 'primary' : 'default'"
              size="small"
              @click="open(act, record)"
            >
              {{ actionLabel[act] }}
            </a-button>
          </template>
          <template v-else-if="column.key === 'manage'">
            <a-button size="small" type="link" @click="open('detail', record)">
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
    </a-card>

    <!-- 项目管理（详情 = 配置/部署目标/容器） -->
    <ProjectDetailDrawer
      :open="openDrawer === 'detail'"
      :project-id="currentId"
      @changed="load"
      @update:open="(v: boolean) => !v && (openDrawer = '')"
    />

    <!-- 环境操作抽屉 -->
    <BuildDrawer
      :env="env"
      :open="openDrawer === 'build'"
      :project-id="currentId"
      @close="openDrawer = ''"
    />
    <ReleaseDrawer
      v-if="env !== 'test'"
      :env="env"
      :open="openDrawer === 'release'"
      :project-id="currentId"
      @close="openDrawer = ''"
    />
    <PolicyDrawer
      v-if="env === 'canary'"
      :open="openDrawer === 'policy'"
      :project="currentProject"
      @close="openDrawer = ''"
    />
    <SlotDrawer
      v-if="env === 'test'"
      :open="openDrawer === 'slot'"
      :project="currentProject"
      @close="openDrawer = ''"
    />

    <a-modal
      v-model:open="formOpen"
      :title="editingId ? '编辑项目' : '新增项目'"
      @ok="submitForm"
    >
      <a-form layout="vertical" class="pt-2">
        <a-form-item label="项目名" required>
          <a-input v-model:value="form.name" placeholder="如 custos-machina" />
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
  </div>
</template>
