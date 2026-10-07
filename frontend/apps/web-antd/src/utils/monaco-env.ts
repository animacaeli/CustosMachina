/**
 * Monaco 全局装配（worker 路由 + 语言注册 + JSONC 容忍项）：全应用只执行一次。
 * 此前 yaml-editor 与 diff-editor 各自在 setup 顶层装配——每个实例重跑，
 * 且互相覆盖（v0.12.3 复核残留项）。
 *
 * v0.12.15（独立审核 N6 + 复核 §7.4）：
 * - 编辑器组件一律从 'monaco-editor/editor/editor.api' 精确入口引入——
 *   根入口 'monaco-editor' 会注册全部语言（ts/css/html worker 因此进产物、
 *   6.8MB ts.worker 即由此而来）；
 * - 项目实际语言在此显式注册（v0.8.2 教训：contribution 须显式 import，
 *   惰性挂载会崩）：json（语言服务）+ shell/python/ini（基础高亮），
 *   yaml 由 monaco-yaml 的 configureMonacoYaml 自带注册；
 * - json worker 路由补齐：此前一律回落 editor.worker，JSON 语言服务
 *   （诊断/补全/JSONC 容忍项）从不生效——「不报注释错误」实为诊断缺席。
 */
// oxlint-disable-next-line import/default
import editorWorker from 'monaco-editor/editor/editor.worker?worker';
// oxlint-disable-next-line import/default
import jsonWorker from 'monaco-editor/languages/features/json/json.worker?worker';
import { jsonDefaults } from 'monaco-editor/languages/features/json/register';
// oxlint-disable-next-line import/default
import yamlWorker from 'monaco-yaml/yaml.worker?worker';

// 基础高亮语言（配置文件按钮组实际使用的面）：显式注册，防惰性挂载
import 'monaco-editor/languages/definitions/ini/register';
import 'monaco-editor/languages/definitions/python/register';
import 'monaco-editor/languages/definitions/shell/register';

const YamlWorkerCtor = yamlWorker as unknown as new () => Worker;
const JsonWorkerCtor = jsonWorker as unknown as new () => Worker;
const EditorWorkerCtor = editorWorker as unknown as new () => Worker;

let ready = false;

export function ensureMonacoEnv() {
  if (ready) return;
  ready = true;
  globalThis.MonacoEnvironment = {
    getWorker(_, label) {
      if (label === 'yaml') return new YamlWorkerCtor();
      if (label === 'json') return new JsonWorkerCtor();
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
