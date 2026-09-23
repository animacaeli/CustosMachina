<script lang="ts" setup>
/**
 * 通用 YAML 编辑器（Monaco + monaco-yaml）：
 * compose 部署文件、未来的配置中心共用。按需 chunk 加载，不进首屏。
 * schema 为内置 vendored 副本（离线可用），不带远程 $ref。
 */
import { onBeforeUnmount, onMounted, ref, watch } from 'vue';

import { usePreferences } from '@vben/preferences';

import * as monaco from 'monaco-editor';
import editorWorker from 'monaco-editor/editor/editor.worker?worker';
import { configureMonacoYaml } from 'monaco-yaml';
import yamlWorker from 'monaco-yaml/yaml.worker?worker';

import composeSchema from '#/schemas/compose-spec.json';

const props = withDefaults(
  defineProps<{
    modelValue: string;
    readOnly?: boolean;
    /** 传 "compose" 启用 compose-spec 补全；不传则仅语法校验 */
    schema?: 'compose';
    height?: string;
  }>(),
  { readOnly: false, schema: undefined, height: '420px' },
);

const emit = defineEmits<{ 'update:modelValue': [value: string] }>();
const containerRef = ref<HTMLDivElement>();
let editor: monaco.editor.IStandaloneCodeEditor | null = null;

// vite worker 直连打包（不走 CDN，离线单镜像可用）
self.MonacoEnvironment = {
  getWorker(_, label) {
    if (label === 'yaml') return new yamlWorker();
    return new editorWorker();
  },
};

// monaco-yaml v5：configureMonacoYaml 单实例配置，schema 用内置 vendored 副本
configureMonacoYaml(monaco, {
  completion: true,
  hover: true,
  validate: true,
  schemas: props.schema
    ? [
        {
          fileMatch: ['*'],
          schema: composeSchema as any,
          uri: 'https://raw.githubusercontent.com/compose-spec/compose-spec/master/schema/compose-spec.json',
        },
      ]
    : [],
});

// 跟随应用主题切换明暗
const { isDark } = usePreferences();

onMounted(() => {
  if (!containerRef.value) return;
  editor = monaco.editor.create(containerRef.value, {
    value: props.modelValue,
    language: 'yaml',
    readOnly: props.readOnly,
    theme: isDark.value ? 'vs-dark' : 'vs',
    minimap: { enabled: false },
    fontSize: 13,
    automaticLayout: true,
    scrollBeyondLastLine: false,
    tabSize: 2,
    renderWhitespace: 'boundary',
  });
  editor.onDidChangeModelContent(() => {
    emit('update:modelValue', editor!.getValue());
  });
});

watch(isDark, (dark) => {
  monaco.editor.setTheme(dark ? 'vs-dark' : 'vs');
});

watch(
  () => props.modelValue,
  (v) => {
    if (editor && editor.getValue() !== v) editor.setValue(v);
  },
);

watch(
  () => props.readOnly,
  (v) => editor?.updateOptions({ readOnly: v }),
);

onBeforeUnmount(() => editor?.dispose());

defineExpose({ focus: () => editor?.focus() });
</script>

<template>
  <div
    ref="containerRef"
    :style="{
      height,
      width: '100%',
      border: isDark ? '1px solid #333' : '1px solid #d9d9d9',
    }"
  ></div>
</template>
