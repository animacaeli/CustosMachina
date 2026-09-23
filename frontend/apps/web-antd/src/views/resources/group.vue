<script lang="ts" setup>
import type { ServerGroup } from '#/api/resources/server';

import { onMounted, reactive, ref } from 'vue';

import { message } from 'ant-design-vue';

import {
  createGroupApi,
  deleteGroupApi,
  getGroupListApi,
  updateGroupApi,
} from '#/api/resources/server';

defineOptions({ name: 'ResourcesGroup' });

const loading = ref(false);
const list = ref<ServerGroup[]>([]);

async function load() {
  loading.value = true;
  try {
    list.value = await getGroupListApi();
  } finally {
    loading.value = false;
  }
}

onMounted(load);

const formOpen = ref(false);
const editingId = ref<null | number>(null);
const form = reactive({ name: '', remark: '' });

function openCreate() {
  editingId.value = null;
  form.name = '';
  form.remark = '';
  formOpen.value = true;
}

function openEdit(g: ServerGroup) {
  editingId.value = g.id;
  form.name = g.name;
  form.remark = g.remark;
  formOpen.value = true;
}

async function submitForm() {
  if (!form.name) {
    message.warning('请填写分组名称');
    return;
  }
  if (editingId.value) {
    await updateGroupApi(editingId.value, { ...form });
    message.success('已更新');
  } else {
    await createGroupApi({ ...form });
    message.success('创建成功');
  }
  formOpen.value = false;
  await load();
}

async function onDelete(g: ServerGroup) {
  try {
    await deleteGroupApi(g.id);
    message.success(`已删除 ${g.name}`);
    await load();
  } catch {
    // 非空分组等业务错误由拦截器提示
  }
}
</script>

<template>
  <div class="p-4">
    <a-card title="服务器分组">
      <template #extra>
        <a-button type="primary" @click="openCreate">新增分组</a-button>
      </template>
      <a-table
        :data-source="list"
        :loading="loading"
        :pagination="false"
        row-key="id"
      >
        <a-table-column title="ID" data-index="id" :width="60" />
        <a-table-column title="名称" data-index="name" />
        <a-table-column title="备注" data-index="remark">
          <template #default="{ text }">{{ text || '—' }}</template>
        </a-table-column>
        <a-table-column
          title="服务器数"
          data-index="serverCount"
          :width="100"
        />
        <a-table-column title="操作" :width="150">
          <template #default="{ record }">
            <a-button size="small" type="link" @click="openEdit(record)">
              编辑
            </a-button>
            <a-popconfirm
              :title="
                record.serverCount > 0
                  ? `分组下有 ${record.serverCount} 台服务器，无法删除`
                  : `确认删除 ${record.name}？`
              "
              :ok-button-props="{ disabled: record.serverCount > 0 }"
              @confirm="onDelete(record)"
            >
              <a-button
                size="small"
                type="link"
                danger
                :disabled="record.serverCount > 0"
              >
                删除
              </a-button>
            </a-popconfirm>
          </template>
        </a-table-column>
      </a-table>
    </a-card>

    <a-modal
      v-model:open="formOpen"
      :title="editingId ? `编辑分组：${form.name}` : '新增分组'"
      @ok="submitForm"
    >
      <a-form layout="vertical" class="pt-2">
        <a-form-item label="名称" required>
          <a-input v-model:value="form.name" placeholder="如：生产" />
        </a-form-item>
        <a-form-item label="备注">
          <a-input v-model:value="form.remark" />
        </a-form-item>
      </a-form>
    </a-modal>
  </div>
</template>
