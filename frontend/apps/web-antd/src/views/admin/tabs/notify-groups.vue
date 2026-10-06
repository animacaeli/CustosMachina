<script lang="ts" setup>
import type { NotifyGroup } from '#/api/projects';

import { onMounted, reactive, ref } from 'vue';

import { message } from 'ant-design-vue';

import {
  createNotifyGroupApi,
  deleteNotifyGroupApi,
  getChannelSettingsApi,
  getNotifyGroupsApi,
  getOpsGroupApi,
  saveChannelSettingsApi,
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

// ---- P6-M9 渠道凭据设置（Telegram Bot / SMTP，平台级）----
const channelForm = reactive({
  smtpHost: '',
  smtpPort: '',
  smtpUser: '',
  smtpPass: '',
  smtpFrom: '',
});
const channelState = reactive({
  smtpConfigured: false,
});

async function loadChannels() {
  const s = await getChannelSettingsApi();
  channelState.smtpConfigured = s.smtpConfigured;
  channelForm.smtpHost = s.smtpHost ?? '';
  channelForm.smtpPort = s.smtpPort ?? '';
  channelForm.smtpUser = s.smtpUser ?? '';
  channelForm.smtpFrom = s.smtpFrom ?? '';
  channelForm.smtpPass = '';
}

async function saveChannels() {
  await saveChannelSettingsApi({
    ...(channelForm.smtpHost ? { smtpHost: channelForm.smtpHost } : {}),
    ...(channelForm.smtpPort ? { smtpPort: channelForm.smtpPort } : {}),
    ...(channelForm.smtpUser ? { smtpUser: channelForm.smtpUser } : {}),
    ...(channelForm.smtpPass ? { smtpPass: channelForm.smtpPass } : {}),
    ...(channelForm.smtpFrom ? { smtpFrom: channelForm.smtpFrom } : {}),
  });
  await loadChannels();
  message.success('已保存（凭据加密落库）');
}

onMounted(() => {
  load();
  loadChannels();
});

const columns = [
  { title: 'ID', dataIndex: 'id', width: 60 },
  { title: '群名称', dataIndex: 'name' },
  {
    title: '用途',
    dataIndex: 'scope',
    width: 100,
  },
  { title: '备注', dataIndex: 'remark' },
  { title: '渠道', key: 'channel', width: 100 },
  { title: 'Webhook', key: 'webhook', width: 130 },
  { title: '操作', key: 'action', width: 230 },
];

const formOpen = ref(false);
const editingId = ref<null | number>(null);
const form = reactive<{
  name: string;
  scope: 'dev' | 'prod';
  channel: 'smtp' | 'webhook';
  target: string;
  webhook: string;
  remark: string;
}>({
  name: '',
  scope: 'prod',
  channel: 'webhook',
  target: '',
  webhook: '',
  remark: '',
});

function openCreate() {
  editingId.value = null;
  form.name = '';
  form.scope = 'prod';
  form.channel = 'webhook';
  form.target = '';
  form.webhook = '';
  form.remark = '';
  formOpen.value = true;
}

function openEdit(g: NotifyGroup) {
  editingId.value = g.id;
  form.name = g.name;
  form.scope = (g.scope as 'prod') ?? 'prod';
  form.channel = g.channel ?? 'webhook';
  form.target = g.target ?? '';
  form.webhook = ''; // 留空保留
  form.remark = g.remark ?? '';
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
    <a-alert show-icon type="info" style="margin-bottom: 1rem">
      <template #message>
        登记时显式选择用途：生产类（正式 /
        灰度环境可选）或测试类（测试环境可选）。 支持企微 / 钉钉 / 飞书群机器人
        webhook（按地址自动识别消息格式；钉钉加签机器人请把 sign 拼进 webhook
        地址）。
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
      <template #headerCell="{ column }">
        <template v-if="column.key === 'channel'">渠道</template>
      </template>
      <template #bodyCell="{ column, record }">
        <template v-if="column.dataIndex === 'scope'">
          <a-tag :color="record.scope === 'prod' ? 'red' : 'blue'">
            {{ record.scope === 'prod' ? '生产类' : '测试类' }}
          </a-tag>
        </template>
        <template v-else-if="column.key === 'channel'">
          <a-tag>
            {{ record.channel === 'smtp' ? '邮件' : 'Webhook' }}
          </a-tag>
        </template>
        <template v-else-if="column.key === 'webhook'">
          <a-tag
            :color="
              record.channel !== 'webhook' || record.hasWebhook
                ? 'green'
                : 'orange'
            "
          >
            {{
              record.channel !== 'webhook'
                ? record.target || '—'
                : record.hasWebhook
                  ? '已配置'
                  : '未配置'
            }}
          </a-tag>
        </template>
        <template v-else-if="column.key === 'action'">
          <a-button size="small" type="link" @click="openEdit(record)">
            编辑
          </a-button>
          <a-button
            :loading="testingId === record.id"
            :disabled="record.channel !== 'smtp' && !record.hasWebhook"
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

    <!-- P6-M9 渠道凭据（平台级）：Telegram Bot / SMTP 账号，群上只存目标 -->
    <div class="mt-2 rounded-lg border border-border p-3">
      <div class="mb-2 text-sm font-medium">
        通道设置（平台级凭据）
        <span class="ml-2 text-xs font-normal text-muted-foreground">
          SMTP
          账号在此统一配置；各通知群只登记收件人（逗号分隔可群发；通知规则可将一个事件路由到多个群）
        </span>
      </div>
      <div class="flex flex-wrap items-center gap-2">
        <a-input
          v-model:value="channelForm.smtpHost"
          placeholder="SMTP 主机"
          size="small"
          style="width: 140px"
        />
        <a-input
          v-model:value="channelForm.smtpPort"
          placeholder="端口"
          size="small"
          style="width: 80px"
        />
        <a-input
          v-model:value="channelForm.smtpUser"
          placeholder="账号"
          size="small"
          style="width: 130px"
        />
        <a-input-password
          v-model:value="channelForm.smtpPass"
          :placeholder="
            channelState.smtpConfigured ? '密码（已配置，留空保留）' : '密码'
          "
          size="small"
          style="width: 130px"
        />
        <a-input
          v-model:value="channelForm.smtpFrom"
          placeholder="发件人（可选）"
          size="small"
          style="width: 150px"
        />
        <a-button size="small" type="primary" @click="saveChannels">
          保存通道
        </a-button>
      </div>
    </div>

    <a-modal
      v-model:open="formOpen"
      :title="editingId ? '编辑通知群' : '登记通知群'"
      @ok="submitForm"
    >
      <a-form layout="vertical">
        <a-form-item label="群名称" required>
          <a-input v-model:value="form.name" placeholder="运维值班群" />
        </a-form-item>
        <a-form-item label="用途" required>
          <a-select
            v-model:value="form.scope"
            :options="[
              { label: '生产类（正式 / 灰度）', value: 'prod' },
              { label: '测试类（测试环境）', value: 'dev' },
            ]"
          />
        </a-form-item>
        <a-form-item label="通知渠道" required>
          <a-select
            v-model:value="form.channel"
            :options="[
              {
                label: '群机器人 Webhook（企微 / 钉钉 / 飞书）',
                value: 'webhook',
              },
              { label: '邮件（SMTP）', value: 'smtp' },
            ]"
          />
        </a-form-item>
        <a-form-item
          v-if="form.channel === 'webhook'"
          label="机器人 Webhook"
          extra="企微 / 钉钉 / 飞书群机器人地址；编辑时留空保留"
        >
          <a-input-password v-model:value="form.webhook" />
        </a-form-item>
        <a-form-item
          v-if="form.channel === 'smtp'"
          label="收件人"
          extra="多个邮箱逗号分隔；SMTP 账号在下方通道设置统一配置"
          required
        >
          <a-input v-model:value="form.target" placeholder="a@x.com, b@x.com" />
        </a-form-item>
        <a-form-item label="备注">
          <a-input v-model:value="form.remark" />
        </a-form-item>
      </a-form>
    </a-modal>
  </div>
</template>
