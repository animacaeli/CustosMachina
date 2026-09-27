<script lang="ts" setup>
import type { CronScript } from '#/api/cron';

import { computed, onMounted, reactive, ref } from 'vue';

import { useUserStore } from '@vben/stores';

import { message } from 'ant-design-vue';

import {
  createScriptApi,
  deleteScriptApi,
  getScriptsApi,
  updateScriptApi,
} from '#/api/cron';

defineOptions({ name: 'CronScripts' });

const userStore = useUserStore();
// 创建/修改仅 admin（后端 casbin 兜底，这里只控制按钮可见性）
const canWrite = computed(() => {
  const roles = userStore.userInfo?.roles ?? [];
  return roles.includes('superadmin') || roles.includes('admin');
});

const loading = ref(false);
const list = ref<CronScript[]>([]);

async function load() {
  loading.value = true;
  try {
    const res = await getScriptsApi();
    list.value = res.items ?? [];
  } finally {
    loading.value = false;
  }
}

onMounted(load);

const typeText: Record<string, string> = {
  python: 'Python',
  shell: 'Shell',
  'compose-run': 'Compose 服务',
};

const formOpen = ref(false);
const editingId = ref<null | number>(null);
const form = reactive({
  content: '',
  name: '',
  remark: '',
  type: 'shell' as CronScript['type'],
});

function openCreate() {
  editingId.value = null;
  form.name = '';
  form.content = '';
  form.remark = '';
  form.type = 'shell';
  formOpen.value = true;
}

function openEdit(s: CronScript) {
  editingId.value = s.id;
  form.name = s.name;
  form.content = s.content;
  form.remark = s.remark;
  form.type = s.type;
  formOpen.value = true;
}

async function submitForm() {
  if (!form.name) {
    message.warning('请填写脚本名称');
    return;
  }
  try {
    if (editingId.value) {
      await updateScriptApi(editingId.value, { ...form });
      message.success('已更新');
    } else {
      await createScriptApi({ ...form });
      message.success('创建成功');
    }
    formOpen.value = false;
    await load();
  } catch {
    // 业务错误由拦截器提示
  }
}

async function onDelete(s: CronScript) {
  try {
    await deleteScriptApi(s.id);
    message.success(`已删除 ${s.name}`);
    await load();
  } catch {
    // 被任务绑定等业务错误由拦截器提示
  }
}
</script>

<template>
  <div class="p-4">
    <a-card title="脚本库">
      <template #extra>
        <a-button v-if="canWrite" type="primary" @click="openCreate">
          新增脚本
        </a-button>
      </template>
      <a-table
        :data-source="list"
        :loading="loading"
        :pagination="false"
        row-key="id"
      >
        <a-table-column title="ID" data-index="id" :width="60" />
        <a-table-column title="名称" data-index="name" />
        <a-table-column title="类型" :width="120">
          <template #default="{ record }">
            <a-tag>{{ typeText[record.type] ?? record.type }}</a-tag>
          </template>
        </a-table-column>
        <a-table-column title="内容" data-index="content" :ellipsis="true">
          <template #default="{ text }">
            <code>{{ (text || '—').slice(0, 80) }}</code>
          </template>
        </a-table-column>
        <a-table-column title="备注" data-index="remark">
          <template #default="{ text }">{{ text || '—' }}</template>
        </a-table-column>
        <a-table-column v-if="canWrite" title="操作" :width="150">
          <template #default="{ record }">
            <a-button size="small" type="link" @click="openEdit(record)">
              编辑
            </a-button>
            <a-popconfirm
              :title="`确认删除 ${record.name}？`"
              @confirm="onDelete(record)"
            >
              <a-button size="small" type="link" danger>删除</a-button>
            </a-popconfirm>
          </template>
        </a-table-column>
      </a-table>
    </a-card>

    <a-modal
      v-model:open="formOpen"
      :title="editingId ? `编辑脚本：${form.name}` : '新增脚本'"
      :width="640"
      @ok="submitForm"
    >
      <a-form layout="vertical" style="padding-top: 0.5rem">
        <a-form-item label="名称" required>
          <a-input v-model:value="form.name" placeholder="如：数据库清理" />
        </a-form-item>
        <a-form-item label="类型" required>
          <a-radio-group v-model:value="form.type">
            <a-radio value="shell">Shell（跑在绑定镜像内）</a-radio>
            <a-radio value="python">Python（镜像须含 python3）</a-radio>
            <a-radio value="compose-run">
              Compose 服务（项目内命令型服务）
            </a-radio>
          </a-radio-group>
        </a-form-item>
        <a-form-item
          :label="form.type === 'compose-run' ? '备注 / 命令说明' : '脚本内容'"
        >
          <a-textarea
            v-model:value="form.content"
            :rows="10"
            :placeholder="
              form.type === 'compose-run'
                ? '仅作说明留档，实际命令由 compose 服务定义'
                : '如：#!/bin/sh\necho cleanup'
            "
          />
        </a-form-item>
        <a-form-item label="备注">
          <a-input v-model:value="form.remark" />
        </a-form-item>
      </a-form>
    </a-modal>
  </div>
</template>
