/**
 * Monaco 全局装配（worker 路由 + JSONC 容忍项）：全应用只执行一次。
 * 此前 yaml-editor 与 diff-editor 各自在 setup 顶层装配——每个实例重跑，
 * 且互相覆盖（v0.12.3 复核残留项）。
 */
import * as monaco from 'monaco-editor';
// oxlint-disable-next-line import/default
import editorWorker from 'monaco-editor/editor/editor.worker?worker';
import { jsonDefaults } from 'monaco-editor/languages/features/json/register';
// oxlint-disable-next-line import/default
import yamlWorker from 'monaco-yaml/yaml.worker?worker';

const YamlWorkerCtor = yamlWorker as unknown as new () => Worker;
const EditorWorkerCtor = editorWorker as unknown as new () => Worker;

let ready = false;

export function ensureMonacoEnv() {
  if (ready) return;
  ready = true;
  globalThis.MonacoEnvironment = {
    getWorker(_, label) {
      if (label === 'yaml') return new YamlWorkerCtor();
      return new EditorWorkerCtor();
    },
  };
  // JSONC：json 允许 // 注释与尾逗号（配置文件惯例；保存不校验合法性）。
  jsonDefaults.setDiagnosticsOptions({
    allowComments: true,
    trailingComma: 'ignore',
    validate: true,
  });
}

export { monaco };
