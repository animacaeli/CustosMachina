<script lang="ts" setup>
/**
 * P7-M3 编辑器 AI 助手抽屉（advisory）：三场景共用——
 * editor（compose 骨架/配置排错，可带当前文件内容）、cron（自然语言→五段
 * crontab）、alert_rule（监控诉求→PromQL）。生成结果只展示，写入目标
 * 由用户点「插入」确认（AI 不直接改编辑器）。
 */
import { computed, ref } from 'vue';

import { message } from 'ant-design-vue';

import { assistApi } from '#/api/chat';

const props = withDefaults(
  defineProps<{
    /** 场景：editor / cron / alert_rule（决定后端提示词） */
    scene: 'alert_rule' | 'cron' | 'editor';
    /** 抽屉标题 */
    title?: string;
    /** 输入占位示例 */
    placeholder?: string;
    /** 场景附加上下文（editor 场景传当前文件内容等） */
    context?: { content?: string; fileName?: string; fileType?: string };
    /** 插入按钮文案（如「填入表达式」/「替换文件内容」） */
    applyLabel?: string;
    /** 结果后处理（cron 取首段表达式等；默认原样） */
    extract?: (result: string) => string;
  }>(),
  {
    // title/applyLabel/placeholder 默认 undefined（显式写以满足 lint 规则）：
    // 保持 undefined 让 sceneMeta 的场景化文案生效，设真实默认值会短路 ?? 分支
    title: undefined,
    placeholder: undefined,
    applyLabel: undefined,
    context: undefined,
    extract: undefined,
  },
);

const emit = defineEmits<{ apply: [value: string] }>();

const open = ref(false);
const question = ref('');
const result = ref('');
const loading = ref(false);

const sceneMeta = computed(() => {
  switch (props.scene) {
    case 'cron': {
      return {
        title: 'AI 生成 cron 表达式',
        placeholder: '如：每天凌晨 3 点 / 每周一 8:30 / 每 15 分钟',
        apply: '填入表达式',
      };
    }
    case 'alert_rule': {
      return {
        title: 'AI 生成告警查询',
        placeholder: '如：监控 API 五分钟平均错误率超过 5%',
        apply: '填入查询',
      };
    }
    default: {
      return {
        title: 'AI 编辑助手',
        placeholder: '如：帮我生成 MySQL + Redis 的 compose / 检查此配置的问题',
        apply: '替换文件内容',
      };
    }
  }
});

function show() {
  question.value = '';
  result.value = '';
  open.value = true;
}

async function generate() {
  if (!question.value.trim()) {
    message.warning('请先描述诉求');
    return;
  }
  loading.value = true;
  result.value = '';
  try {
    result.value = await assistApi({
      scene: props.scene,
      question: question.value,
      ...props.context,
    });
  } catch (error: any) {
    message.error(
      error?.response?.data?.message ?? error?.message ?? '生成失败',
    );
  } finally {
    loading.value = false;
  }
}

function applyResult() {
  const value = props.extract
    ? props.extract(result.value)
    : result.value.trim();
  if (!value) {
    message.warning('没有可插入的内容');
    return;
  }
  emit('apply', value);
  open.value = false;
}

async function copyResult() {
  try {
    await navigator.clipboard.writeText(result.value);
    message.success('已复制');
  } catch {
    message.warning('复制失败，请手动选择');
  }
}

defineExpose({ show });
</script>

<template>
  <span>
    <slot :open="show">
      <a-button size="small" type="link" @click="show"> AI 助手 </a-button>
    </slot>
    <a-drawer
      v-model:open="open"
      :title="title ?? sceneMeta.title"
      :width="520"
    >
      <a-form layout="vertical">
        <a-form-item label="你的诉求">
          <a-textarea
            v-model:value="question"
            :placeholder="placeholder ?? sceneMeta.placeholder"
            :rows="3"
            @press-enter="generate"
          />
        </a-form-item>
        <a-button :loading="loading" type="primary" @click="generate">
          {{ loading ? '生成中…' : '生 成' }}
        </a-button>
      </a-form>
      <div v-if="result" class="mt-4">
        <div class="mb-1 flex items-center justify-between">
          <span class="font-medium">生成结果（AI 建议，插入前请审阅）</span>
        </div>
        <pre
          class="max-h-[45vh] overflow-auto rounded bg-gray-50 p-3 font-mono text-xs dark:bg-gray-900"
          >{{ result }}</pre>
        <div class="mt-3 flex justify-end gap-2">
          <a-button @click="copyResult"> 复 制 </a-button>
          <a-button type="primary" @click="applyResult">
            {{ applyLabel ?? sceneMeta.apply }}
          </a-button>
        </div>
      </div>
    </a-drawer>
  </span>
</template>
