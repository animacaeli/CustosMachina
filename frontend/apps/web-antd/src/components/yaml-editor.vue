<script lang="ts" setup>
/**
 * 通用代码编辑器（Monaco + monaco-yaml）：
 * language 支持 yaml/json/shell/python/ini(toml 近似)/plaintext，
 * compose 部署文件、脚本编辑、配置中心、SFTP 在线编辑共用。
 * 按需 chunk 加载，不进首屏；schema 为内置 vendored 副本（离线可用）。
 */
import { onBeforeUnmount, onMounted, ref, watch } from 'vue';

import { usePreferences } from '@vben/preferences';

// 精确入口（v0.12.15 落实注释声称）：根入口 'monaco-editor' 会注册全部
// 语言（ts.worker 6.9MB / css/html worker 因此进产物）；editor.api 为纯
// 编辑器 API，语言由 monaco-env.ts 显式注册（json/shell/python/ini +
// monaco-yaml 自带的 yaml）
// oxlint-disable-next-line import/default
import * as monaco from 'monaco-editor/editor/editor.api';
// oxlint-disable-next-line import/default
import { configureMonacoYaml } from 'monaco-yaml';

import composeSchema from '#/schemas/compose-spec.json';
import { ensureMonacoEnv } from '#/utils/monaco-env';

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
ensureMonacoEnv();

// compose schema 补全（monaco-yaml 单例配置）：仅显式要求时更新一次——
// 无 schema 的实例不触碰全局配置，避免清掉其他编辑器挂上的补全
// （v0.12.3 复核残留项）
let schemaApplied = false;
function applyComposeSchema() {
  if (schemaApplied || !props.schema || props.language !== 'yaml') return;
  schemaApplied = true;
  configureMonacoYaml(monaco, {
    completion: true,
    hover: true,
    validate: true,
    schemas: [
      {
        fileMatch: ['*'],
        schema: composeSchema as any,
        uri: 'https://raw.githubusercontent.com/compose-spec/compose-spec/master/schema/compose-spec.json',
      },
    ],
  });
}
applyComposeSchema();

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

// language 变化（复用实例编辑不同后缀文件）时切换高亮
watch(
  () => props.language,
  (lang) => {
    const model = editor?.getModel();
    if (model) monaco.editor.setModelLanguage(model, lang);
  },
);

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
