<script lang="ts" setup>
import type { NotifyGroup } from '#/api/projects';

import { onMounted, reactive, ref } from 'vue';

import { message } from 'ant-design-vue';

import {
  createNotifyGroupApi,
  deleteNotifyGroupApi,
  getNotifyGroupsApi,
  getOpsGroupApi,
  setOpsGroupApi,
  testNotifyGroupApi,
  updateNotifyGroupApi,
} from '#/api/projects';

defineOptions({ name: 'AdminNotifyGroups' });

const loading = ref(false);
const list = ref<NotifyGroup[]>([]);
const opsGroupId = ref<number | undefined>();
const opsConfigured = ref(false);

async function load() {
  loading.value = true;
  try {
    const [gs, ops] = await Promise.all([
      getNotifyGroupsApi(),
      getOpsGroupApi(),
    ]);
    list.value = gs;
    opsConfigured.value = ops.configured;
    opsGroupId.value = ops.configured ? ops.groupId : undefined;
  } finally {
    loading.value = false;
  }
}

onMounted(load);

const columns = [
  { title: 'ID', dataIndex: 'id', width: 60 },
  { title: '群名称', dataIndex: 'name' },
  {
    title: '用途',
    dataIndex: 'scope',
    width: 100,
  },
  { title: '备注', dataIndex: 'remark' },
  { title: 'Webhook', key: 'webhook', width: 90 },
  { title: '操作', key: 'action', width: 230 },
];

const formOpen = ref(false);
const editingId = ref<null | number>(null);
const form = reactive({ name: '', webhook: '', remark: '' });

function openCreate() {
  editingId.value = null;
  form.name = '';
  form.webhook = '';
  form.remark = '';
  formOpen.value = true;
}

function openEdit(g: NotifyGroup) {
  editingId.value = g.id;
  form.name = g.name;
  form.webhook = ''; // 留空保留
  form.remark = g.remark;
  formOpen.value = true;
}

async function submitForm() {
  if (editingId.value) {
    await updateNotifyGroupApi(editingId.value, { ...form });
    message.success('已更新');
  } else {
    await createNotifyGroupApi({ ...form });
    message.success('登记成功');
  }
  formOpen.value = false;
  await load();
}

async function onDelete(g: NotifyGroup) {
  await deleteNotifyGroupApi(g.id);
  message.success(`已删除 ${g.name}`);
  await load();
}

const testingId = ref<null | number>(null);
async function onTest(g: NotifyGroup) {
  testingId.value = g.id;
  try {
    await testNotifyGroupApi(g.id);
    message.success('测试消息已发送，请到群里确认');
  } catch {
    // 拦截器已提示
  } finally {
    testingId.value = null;
  }
}

async function saveOpsGroup() {
  if (!opsGroupId.value) return;
  await setOpsGroupApi(opsGroupId.value);
  message.success('运维告警群已保存');
}
</script>

<template>
  <div>
    <a-alert class="mb-4" show-icon type="info">
      <template #message>
        群名称前缀是硬约束：【P】开头 = 生产类（正式 / 灰度可选），【dev】开头 =
        测试类（测试环境可选）。webhook 从企微群机器人配置页复制。
      </template>
    </a-alert>

    <div class="mb-4 flex flex-wrap items-center gap-2">
      <span class="text-sm">运维告警群（服务器不可达等平台事件）：</span>
      <a-select
        v-model:value="opsGroupId"
        :options="list.map((g) => ({ label: g.name, value: g.id }))"
        allow-clear
        placeholder="未配置（告警只落库）"
        style="width: 260px"
      />
      <a-button :disabled="!opsGroupId" size="small" @click="saveOpsGroup">
        保存
      </a-button>
      <a-tag v-if="opsConfigured" color="green">已配置</a-tag>
    </div>

    <div class="mb-4">
      <a-button type="primary" @click="openCreate">登记群机器人</a-button>
    </div>

    <a-table
      :columns="columns"
      :data-source="list"
      :loading="loading"
      :pagination="false"
      row-key="id"
      size="middle"
    >
      <template #bodyCell="{ column, record }">
        <template v-if="column.dataIndex === 'scope'">
          <a-tag :color="record.scope === 'prod' ? 'red' : 'blue'">
            {{ record.scope === 'prod' ? '生产类' : '测试类' }}
          </a-tag>
        </template>
        <template v-else-if="column.key === 'webhook'">
          <a-tag :color="record.hasWebhook ? 'green' : 'orange'">
            {{ record.hasWebhook ? '已配置' : '未配置' }}
          </a-tag>
        </template>
        <template v-else-if="column.key === 'action'">
          <a-button size="small" type="link" @click="openEdit(record)">
            编辑
          </a-button>
          <a-button
            :loading="testingId === record.id"
            :disabled="!record.hasWebhook"
            size="small"
            type="link"
            @click="onTest(record)"
          >
            测试
          </a-button>
          <a-popconfirm title="确认删除该通知群？" @confirm="onDelete(record)">
            <a-button danger size="small" type="link">删除</a-button>
          </a-popconfirm>
        </template>
      </template>
    </a-table>

    <a-modal
      v-model:open="formOpen"
      :title="editingId ? '编辑通知群' : '登记群机器人'"
      @ok="submitForm"
    >
      <a-form layout="vertical" class="pt-2">
        <a-form-item label="群名称" required extra="必须以【P】或【dev】开头">
          <a-input v-model:value="form.name" placeholder="【P】运维值班群" />
        </a-form-item>
        <a-form-item
          label="机器人 Webhook"
          extra="https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=...；编辑时留空保留"
        >
          <a-input-password v-model:value="form.webhook" />
        </a-form-item>
        <a-form-item label="备注">
          <a-input v-model:value="form.remark" />
        </a-form-item>
      </a-form>
    </a-modal>
  </div>
</template>
