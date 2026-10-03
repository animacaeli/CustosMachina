<script lang="ts" setup>
/**
 * 只读 diff 视图（Monaco DiffEditor）：配置文件任意两版本对比用。
 * 行内/并排可切换（renderSideBySide），按需 chunk 加载不进首屏。
 */
import { onBeforeUnmount, onMounted, ref, watch } from 'vue';

import { usePreferences } from '@vben/preferences';

import * as monaco from 'monaco-editor';
// oxlint-disable-next-line import/default
import editorWorker from 'monaco-editor/editor/editor.worker?worker';
// oxlint-disable-next-line import/default
import yamlWorker from 'monaco-yaml/yaml.worker?worker';

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

const YamlWorkerCtor = yamlWorker as unknown as new () => Worker;
const EditorWorkerCtor = editorWorker as unknown as new () => Worker;
globalThis.MonacoEnvironment = {
  getWorker(_, label) {
    if (label === 'yaml') return new YamlWorkerCtor();
    return new EditorWorkerCtor();
  },
};

onMounted(() => {
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
  editor.setModel({
    original: monaco.editor.createModel(props.original, props.language),
    modified: monaco.editor.createModel(props.value, props.language),
  });
});

watch(
  () => [props.original, props.value, props.language],
  () => {
    if (!editor) return;
    editor.setModel({
      original: monaco.editor.createModel(props.original ?? '', props.language),
      modified: monaco.editor.createModel(props.value ?? '', props.language),
    });
  },
);

watch(sideBySide, (v) => editor?.updateOptions({ renderSideBySide: v }));

onBeforeUnmount(() => editor?.dispose());
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
