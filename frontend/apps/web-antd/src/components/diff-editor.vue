<script lang="ts" setup>
/**
 * 只读 diff 视图（Monaco DiffEditor）：配置文件任意两版本对比用。
 * 行内/并排可切换（renderSideBySide），按需 chunk 加载不进首屏。
 */
import { onBeforeUnmount, onMounted, ref, watch } from 'vue';

import { usePreferences } from '@vben/preferences';

// 精确入口（v0.12.15 落实注释声称）：根入口会注册全部语言、把未使用的
// ts/css/html worker 拉进产物；语言由 monaco-env.ts 显式注册
// oxlint-disable-next-line import/default
import * as monaco from 'monaco-editor/editor/editor.api';

import { ensureMonacoEnv } from '#/utils/monaco-env';

const props = withDefaults(
  defineProps<{
    height?: string;
    language?: string;
    /** 对比基准（左） */
    original: string;
    /** 对比目标（右） */
    value: string;
  }>(),
  { height: '420px', language: 'yaml' },
);

const sideBySide = ref(true);
const containerRef = ref<HTMLDivElement>();
let editor: monaco.editor.IDiffEditor | null = null;

onMounted(() => {
  ensureMonacoEnv();
  if (!containerRef.value) return;
  const { isDark } = usePreferences();
  editor = monaco.editor.createDiffEditor(containerRef.value, {
    theme: isDark.value ? 'vs-dark' : 'vs',
    automaticLayout: true,
    enableSplitViewResizing: false,
    fontSize: 12,
    lineNumbersMinChars: 3,
    readOnly: true,
    renderOverviewRuler: false,
    renderSideBySide: sideBySide.value,
    scrollBeyondLastLine: false,
  });
  setModels(props.original, props.value);
});

// setModel 换新后旧 model 不被 editor.dispose 释放（model 归全局管理器），
// 必须显式 dispose，否则每次对比泄漏 2 个 model（v0.12.0 审计资源泄漏项）
function setModels(original?: string, value?: string) {
  if (!editor) return;
  const old = editor.getModel();
  if (old) {
    old.original?.dispose();
    old.modified?.dispose();
  }
  editor.setModel({
    original: monaco.editor.createModel(original ?? '', props.language),
    modified: monaco.editor.createModel(value ?? '', props.language),
  });
}

watch(
  () => [props.original, props.value, props.language],
  () => setModels(props.original, props.value),
);

watch(sideBySide, (v) => editor?.updateOptions({ renderSideBySide: v }));

onBeforeUnmount(() => {
  const m = editor?.getModel();
  m?.original?.dispose();
  m?.modified?.dispose();
  editor?.dispose();
});
</script>

<template>
  <div class="flex flex-col gap-2">
    <div class="flex items-center justify-end gap-2">
      <a-radio-group
        v-model:value="sideBySide"
        size="small"
        button-style="solid"
      >
        <a-radio-button :value="true">并排</a-radio-button>
        <a-radio-button :value="false">行内</a-radio-button>
      </a-radio-group>
    </div>
    <div ref="containerRef" :style="{ height, minHeight: '160px' }"></div>
  </div>
</template>
