<script lang="ts" setup>
/**
 * 通用代码编辑器（Monaco + monaco-yaml）：
 * language 支持 yaml/json/shell/python/ini(toml 近似)/plaintext，
 * compose 部署文件、脚本编辑、配置中心、SFTP 在线编辑共用。
 * 按需 chunk 加载，不进首屏；schema 为内置 vendored 副本（离线可用）。
 */
import { onBeforeUnmount, onMounted, ref, watch } from 'vue';

import { usePreferences } from '@vben/preferences';

import * as monaco from 'monaco-editor';
// oxlint-disable-next-line import/default
import editorWorker from 'monaco-editor/editor/editor.worker?worker';
import { configureMonacoYaml } from 'monaco-yaml';
// oxlint-disable-next-line import/default
import yamlWorker from 'monaco-yaml/yaml.worker?worker';

import composeSchema from '#/schemas/compose-spec.json';

const props = withDefaults(
  defineProps<{
    /** monaco 语言 id：yaml/json/shell/python/ini/plaintext（toml 用 ini 近似高亮） */
    language?: string;
    modelValue: string;
    readOnly?: boolean;
    /** 传 "compose" 启用 compose-spec 补全（仅 yaml）；不传则仅语法校验 */
    schema?: 'compose';
    height?: string;
  }>(),
  { language: 'yaml', readOnly: false, schema: undefined, height: '420px' },
);

const emit = defineEmits<{ 'update:modelValue': [value: string] }>();
const containerRef = ref<HTMLDivElement>();
let editor: monaco.editor.IStandaloneCodeEditor | null = null;

// vite worker 直连打包（不走 CDN，离线单镜像可用）
const YamlWorkerCtor = yamlWorker as unknown as new () => Worker;
const EditorWorkerCtor = editorWorker as unknown as new () => Worker;

globalThis.MonacoEnvironment = {
  getWorker(_, label) {
    if (label === 'yaml') return new YamlWorkerCtor();
    return new EditorWorkerCtor();
  },
};

// monaco-yaml v5：configureMonacoYaml 单实例配置，schema 用内置 vendored 副本
// monaco-yaml v5：configureMonacoYaml 单实例配置，schema 用内置 vendored 副本
// （仅 yaml 语言且显式要求 compose schema 时挂补全，其他语言不受影响）
configureMonacoYaml(monaco, {
  completion: true,
  hover: true,
  validate: true,
  schemas:
    props.schema && props.language === 'yaml'
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
    language: props.language,
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
    if (editor) emit('update:modelValue', editor.getValue());
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
