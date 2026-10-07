<script lang="ts" setup>
import { onMounted, reactive, ref } from 'vue';

import { message } from 'ant-design-vue';

import { requestClient } from '#/api/request';
import { extractErrMsg } from '#/utils/extract-err';

defineOptions({ name: 'AdminAiConfig' });

const loading = ref(false);
const testing = ref(false);
const settings = reactive({
  configured: false,
  contextWindow: 0,
  endpoint: '',
  model: '',
});
const form = reactive({
  endpoint: '',
  model: '',
  apiKey: '',
  // P7-M4：上下文窗口（token；空 = 默认 32768，超 70% 自动压缩对话历史）
  contextWindow: '' as '' | number,
});
const usages = ref<Array<Record<string, any>>>([]);

async function load() {
  loading.value = true;
  try {
    const s = await requestClient.get('/ai/settings');
    Object.assign(settings, s);
    form.endpoint = s.endpoint;
    form.model = s.model;
    form.apiKey = '';
    form.contextWindow = s.contextWindow || '';
    usages.value = await requestClient.get('/ai/usages');
  } finally {
    loading.value = false;
  }
}

onMounted(load);

async function save() {
  await requestClient.put('/ai/settings', {
    endpoint: form.endpoint || undefined,
    model: form.model || undefined,
    apiKey: form.apiKey || undefined,
    // a-input-number 清空产出 null（非 ''）；0 = 恢复后端默认窗口
    contextWindow:
      form.contextWindow === null ||
      form.contextWindow === undefined ||
      form.contextWindow === ''
        ? 0
        : form.contextWindow,
  });
  message.success('已保存');
  await load();
}

async function test() {
  testing.value = true;
  try {
    await requestClient.post('/ai/test');
    message.success('中转层连通正常');
  } catch (error: any) {
    message.error(extractErrMsg(error, '测试失败'));
  } finally {
    testing.value = false;
  }
}

const usageCols = [
  { title: '时间', dataIndex: 'createdAt', width: 170 },
  { title: '场景', dataIndex: 'caller', width: 110 },
  { title: '模型', dataIndex: 'model', width: 140 },
  { title: '入/出字符', key: 'chars', width: 110 },
  { title: '耗时', key: 'ms', width: 80 },
  { title: '结果', dataIndex: 'ok', width: 70 },
];
</script>

<template>
  <div>
    <a-form class="max-w-[560px]" layout="vertical">
      <a-form-item
        label="中转层 Endpoint（OpenAI 兼容 base，如 https://api.deepseek.com/v1）"
      >
        <a-input
          v-model:value="form.endpoint"
          :placeholder="settings.endpoint || 'https://api.deepseek.com/v1'"
        />
      </a-form-item>
      <a-form-item label="模型名（如 deepseek-chat / qwen2.5:7b）">
        <a-input
          v-model:value="form.model"
          :placeholder="settings.model || 'deepseek-chat'"
        />
      </a-form-item>
      <a-form-item label="API Key（留空保留）">
        <a-input-password v-model:value="form.apiKey" />
      </a-form-item>
      <a-form-item
        extra="对话历史超过窗口 70% 时自动压缩为前情提要；用户也可在对话中输入 /compact 手动压缩"
        label="上下文窗口（token，留空 = 32768）"
      >
        <a-input-number
          v-model:value="form.contextWindow"
          :min="0"
          :max="2_000_000"
          :placeholder="settings.contextWindow || '32768'"
          class="w-full"
        />
      </a-form-item>
      <a-space>
        <a-button :loading="testing" @click="test">连通性测试</a-button>
        <a-button type="primary" @click="save">保存</a-button>
        <a-tag :color="settings.configured ? 'green' : 'orange'">
          {{
            settings.configured
              ? '已启用（告警将附 AI 摘要）'
              : '未配置（纯通知模式）'
          }}
        </a-tag>
      </a-space>
    </a-form>

    <div class="mt-6">
      <div class="mb-2 text-sm font-medium">最近用量（50 条）</div>
      <a-table
        :columns="usageCols"
        :data-source="usages"
        :loading="loading"
        :pagination="false"
        :scroll="{ x: 'max-content' }"
        row-key="id"
        size="small"
      >
        <template #bodyCell="{ column, record }">
          <template v-if="column.key === 'chars'">
            {{ record.promptChars }} / {{ record.outputChars }}
          </template>
          <template v-else-if="column.key === 'ms'">
            {{ record.latencyMs }}ms
          </template>
          <template v-else-if="column.dataIndex === 'ok'">
            <a-tag :color="record.ok ? 'green' : 'red'">
              {{ record.ok ? '成功' : '失败' }}
            </a-tag>
          </template>
        </template>
      </a-table>
    </div>
  </div>
</template>
