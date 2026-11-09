<script lang="ts" setup>
import type { ConfigFile, ConfigVersion } from '#/api/configs';
import type { ConfigFormat } from '#/utils/config-format';

import {
  computed,
  onBeforeUnmount,
  onMounted,
  reactive,
  ref,
  watch,
} from 'vue';

import { DownOutlined } from '@ant-design/icons-vue';
import { message } from 'ant-design-vue';

import {
  createConfigFileApi,
  deleteConfigFileApi,
  deployConfigApi,
  envSyncConfigApi,
  getConfigContentApi,
  getConfigFilesApi,
  getConfigVersionContentApi,
  getConfigVersionsApi,
  getMergedConfigApi,
  rollbackConfigApi,
  saveConfigContentApi,
  updateConfigFileApi,
} from '#/api/configs';
import { getProjectApi, getProjectsApi } from '#/api/projects';
import { getServerListApi } from '#/api/resources/server';
import AiAssist from '#/components/ai-assist.vue';
import DiffEditor from '#/components/diff-editor.vue';
import YamlEditor from '#/components/yaml-editor.vue';
import { CONFIG_FORMATS, convertView } from '#/utils/config-format';
import { extractErrMsg } from '#/utils/extract-err';

defineOptions({ name: 'ConfigsFiles' });

/**
 * 配置文件管理器（R2）：左侧目录树 + 顶部地址栏（面包屑/一键跳转）+
 * 右侧编辑器（语言按钮组切换高亮）+ 版本侧栏（双选 diff / 回滚）。
 * 层级语义（2026-11-09 返工对齐）：relPath 首段 = prod 或槽位名 dev1..devN
 * （N=项目槽位数；devN 须项目已绑 test 部署目标）；canary 全面引用 prod，
 * 树中无 canary 目录。脱敏为字段级自动识别（token/secret/pwd/password）：
 * 测试槽位任何登录用户可看明文，正式环境需 config.reveal 权限（均落审计）。
 */

const loading = ref(false);
const allFiles = ref<ConfigFile[]>([]);
const servers = ref<Array<{ id: number; name: string }>>([]);
const projects = ref<Array<{ id: number; name: string }>>([]);
const envTargets = ref<Array<{ envType: string; serverId: number }>>([]);
const testSlotCount = ref(3);
const currentProject = ref<number | undefined>(undefined);
const currentProjectName = computed(
  () => projects.value.find((p) => p.id === currentProject.value)?.name,
);

// 环境首段集合：prod + dev1..devN（联动项目槽位数）
const slotEnvs = computed(() =>
  Array.from({ length: testSlotCount.value }, (_, i) => `dev${i + 1}`),
);
const envSegments = computed(() => ['prod', ...slotEnvs.value]);

function isSlotEnv(seg: string) {
  return /^dev[1-9][0-9]*$/.test(seg);
}

// 首段 → 部署目标环境族（目标主机预填用）
function envTypeOf(seg: string): string {
  if (seg === 'prod') return 'prod';
  if (isSlotEnv(seg)) return 'test';
  return '';
}

const FORMAT_LANG: Record<string, string> = {
  env: 'ini',
  ini: 'ini',
  json: 'json',
  toml: 'ini',
  yaml: 'yaml',
};

function actionText(action: string, target: string) {
  if (action === 'none' || !action) return '仅落盘';
  const label: Record<string, string> = {
    http: 'refresh 端点',
    restart: '重启容器',
    sighup: 'SIGHUP',
  };
  return `${label[action] ?? action}：${target}`;
}
const serverName = (id: number) =>
  servers.value.find((s) => s.id === id)?.name ?? `#${id}`;

async function load() {
  loading.value = true;
  try {
    const [fs, ss, ps] = await Promise.all([
      getConfigFilesApi(),
      getServerListApi(),
      getProjectsApi(),
    ]);
    allFiles.value = fs;
    servers.value = ss;
    projects.value = ps;
    const first = ps.at(0);
    if (currentProject.value === undefined && first) {
      currentProject.value = first.id;
    }
  } finally {
    loading.value = false;
  }
}

onMounted(load);

// 项目切换 → 拉环境绑定与槽位数（层级校验/下发默认目标/槽位目录生成用）
watch(currentProject, async (id) => {
  selected.value = null;
  expandedKeys.value = [];
  if (!id) {
    envTargets.value = [];
    return;
  }
  try {
    const r = await getProjectApi(id);
    envTargets.value = (r.targets ?? []).map((t) => ({
      envType: t.envType,
      serverId: t.serverId,
    }));
    testSlotCount.value = r.project.testSlotCount || 3;
  } catch {
    envTargets.value = [];
  }
});

const projectFiles = computed(() =>
  allFiles.value.filter((f) => f.projectId === currentProject.value),
);

// ---- 目录树（relPath 派生虚拟目录）----
interface TreeNode {
  children?: TreeNode[];
  file?: ConfigFile;
  isLeaf: boolean;
  key: string;
  title: string;
}

// 手动新建的空目录（虚拟节点，会话内有效——目录本质由文件派生，落文件后固化）
const ephemeralDirs = ref<string[]>([]);

const treeData = computed<TreeNode[]>(() => {
  const root: TreeNode = { key: '', title: '', isLeaf: false, children: [] };
  // 环境层固化：prod + dev1..devN（空槽位目录也展示，便于右键预建）；
  // canary 不出现（全面引用 prod，合并视图/拉取侧读 prod）
  for (const seg of envSegments.value) {
    root.children!.push({
      key: `/${seg}`,
      title: seg,
      isLeaf: false,
      children: [],
    });
  }
  for (const f of projectFiles.value) {
    const segs = (f.relPath || f.name).split('/').filter(Boolean);
    let dir = root;
    let prefix = '';
    for (const seg of segs.slice(0, -1)) {
      prefix = `${prefix}/${seg}`;
      dir.children ??= [];
      let next = dir.children.find((c) => !c.isLeaf && c.title === seg);
      if (!next) {
        next = { key: prefix, title: seg, isLeaf: false, children: [] };
        dir.children.push(next);
      }
      dir = next;
    }
    dir.children ??= [];
    dir.children.push({
      key: `f-${f.id}`,
      title: segs.at(-1) ?? f.name,
      isLeaf: true,
      file: f,
    });
  }
  for (const dir of ephemeralDirs.value) {
    const segs = dir.split('/').filter(Boolean);
    if (segs.length === 0) continue;
    let node = root;
    let prefix = '';
    for (const seg of segs) {
      prefix = prefix ? `${prefix}/${seg}` : seg;
      node.children ??= [];
      let next = node.children.find((cn) => !cn.isLeaf && cn.title === seg);
      if (!next) {
        next = { key: `/${prefix}`, title: seg, isLeaf: false, children: [] };
        node.children.push(next);
      }
      node = next;
    }
  }
  const sortRec = (n: TreeNode) => {
    n.children?.sort((a, b) => {
      if (a.isLeaf !== b.isLeaf) return a.isLeaf ? 1 : -1;
      return a.title.localeCompare(b.title);
    });
    n.children?.forEach(sortRec);
  };
  sortRec(root);
  return root.children ?? [];
});

const expandedKeys = ref<string[]>([]);
const selected = ref<ConfigFile | null>(null);

function onSelectFile(_keys: number[] | string[], info: any) {
  const f = info?.node?.file as ConfigFile | undefined;
  if (f) {
    // 文件：地址栏实时到全路径（末段文件名不可再点）
    if (f.relPath) currentDir.value = f.relPath.split('/').filter(Boolean);
    openContent(f);
    return;
  }
  // 目录：地址栏切到该目录 + 清空文件选中（编辑器随之隐藏）+ 切换展开
  const key = info?.node?.key;
  if (typeof key === 'string' && key) {
    currentDir.value = key.split('/').filter(Boolean);
    selected.value = null;
    expandedKeys.value = expandedKeys.value.includes(key)
      ? expandedKeys.value.filter((k) => k !== key)
      : [...expandedKeys.value, key];
  }
}

// ---- 地址栏：面包屑 + 一键跳转 ----
// selectedDir = 当前展开导航目录（点击叶子选中文件时取其父目录）
const currentDir = ref<string[]>([]);

const jumpPath = ref('');
function jump() {
  const p = jumpPath.value.trim().replaceAll(/^\/+|\/+$/g, '');
  if (!p) return;
  const segs = p.split('/').filter(Boolean);
  // 展开沿途目录并定位
  let prefix = '';
  const keys: string[] = [];
  for (const seg of segs) {
    prefix = `${prefix}/${seg}`;
    keys.push(prefix);
  }
  expandedKeys.value = [...new Set([...expandedKeys.value, ...keys])];
  currentDir.value = segs;
  // 目录下第一个文件直接选中（也可只定位目录）
  void selected.value;
}

watch(
  () => selected.value,
  (f) => {
    if (f?.relPath) {
      // 展开沿途目录（地址栏由 onSelect 统一驱动，不在此覆盖）
      let prefix = '';
      for (const seg of f.relPath.split('/').filter(Boolean).slice(0, -1)) {
        prefix = `${prefix}/${seg}`;
        if (!expandedKeys.value.includes(prefix))
          expandedKeys.value.push(prefix);
      }
    }
  },
);

function crumbTo(i: number) {
  currentDir.value = currentDir.value.slice(0, i + 1);
}

// ---- 内容编辑 ----
const content = ref('');
const contentMasked = ref(false);
const contentLoading = ref(false);
const revealLoading = ref(false);
// 格式视图：viewFormat ≠ 原格式 = 转换渲染（保存时转回原格式）
const viewFormat = ref<ConfigFormat>('yaml');
const viewConvertError = ref('');
const editorText = ref('');

const editorLang = computed(() => FORMAT_LANG[viewFormat.value] ?? 'yaml');
const isConvertedView = computed(
  () => !!selected.value && viewFormat.value !== selected.value.format,
);

function refreshEditorText() {
  viewConvertError.value = '';
  if (!selected.value) {
    editorText.value = '';
    return;
  }
  if (viewFormat.value === selected.value.format) {
    editorText.value = content.value;
    return;
  }
  const converted = convertView(
    selected.value.format,
    viewFormat.value,
    content.value,
  );
  if (converted === null) {
    viewConvertError.value = `原文件按 ${selected.value.format.toUpperCase()} 解析失败，暂以原文展示（切换视图需原文件语法合法）`;
    editorText.value = content.value;
    return;
  }
  editorText.value = converted;
}

watch([content, viewFormat, selected], refreshEditorText);

async function openContent(f: ConfigFile) {
  selected.value = f;
  viewFormat.value = f.format;
  contentMasked.value = false;
  contentLoading.value = true;
  diffPair.value = [];
  try {
    const r = await getConfigContentApi(f.id);
    content.value = r.content;
    contentMasked.value = r.masked;
    await loadVersions(f.id);
  } finally {
    contentLoading.value = false;
  }
}

async function reveal() {
  if (!selected.value) return;
  revealLoading.value = true;
  try {
    const r = await getConfigContentApi(selected.value.id, true);
    content.value = r.content;
    contentMasked.value = false;
    message.info('已显示明文（本次查看已落审计）');
  } catch {
    // 403（正式环境无 config.reveal）等：全局拦截器已弹具体提示，
    // 此处吞掉防 unhandled rejection（编辑器页不额外处理）
  } finally {
    revealLoading.value = false;
  }
}

const savingContent = ref(false);
async function saveContent() {
  if (!selected.value || contentMasked.value) return;
  const fmt = selected.value.format;
  let toSave = editorText.value;
  if (viewFormat.value !== fmt) {
    const back = convertView(viewFormat.value, fmt, editorText.value);
    if (back === null) {
      message.error(
        `${viewFormat.value.toUpperCase()} 视图内容转换回 ${fmt.toUpperCase()} 失败（语法错误？）`,
      );
      return;
    }
    toSave = back;
  }
  savingContent.value = true;
  try {
    await saveConfigContentApi(selected.value.id, toSave);
    message.success('内容已保存（新版本）');
    await openContent(selected.value);
    await loadVersions(selected.value.id);
  } finally {
    savingContent.value = false;
  }
}

const deploying = ref(0);
async function deploy() {
  if (!selected.value) return;
  deploying.value = selected.value.id;
  try {
    await deployConfigApi(selected.value.id);
    message.success('已下发（旧文件已备份 .bak.时间戳）');
    await loadVersions(selected.value.id);
  } catch (error: any) {
    message.error(extractErrMsg(error, '下发失败'));
  } finally {
    deploying.value = 0;
  }
}

// ---- 目录右键：新建文件 / 新建文件夹（Windows 风格） ----
const ctxMenu = reactive({
  open: false,
  x: 0,
  y: 0,
  dirKey: '', // 目标目录（'' = 根）
});
const folderModalOpen = ref(false);
const newFolderName = ref('');

function closeCtxMenu() {
  ctxMenu.open = false;
}
onMounted(() => document.addEventListener('click', closeCtxMenu));
onBeforeUnmount(() => document.removeEventListener('click', closeCtxMenu));

function openCtxMenu(x: number, y: number, dirKey: string) {
  ctxMenu.x = x;
  ctxMenu.y = y;
  ctxMenu.dirKey = dirKey;
  ctxMenu.open = true;
}

// 树节点右键：目录（含文件节点不给菜单）
function onTreeRightClick(state: { event: MouseEvent; node: any }) {
  const f = state?.node?.file;
  if (f) return; // 文件不给右键菜单
  const key = typeof state?.node?.key === 'string' ? state.node.key : '';
  state?.event?.preventDefault?.();
  openCtxMenu(state.event.clientX, state.event.clientY, key);
}

// 树容器空白处右键 = 根目录菜单（节点右键会冒泡到此——按目标是否节点区分）
function onRootRightClick(ev: MouseEvent) {
  const onNode = (ev.target as HTMLElement | null)?.closest(
    '.ant-tree-node-content-wrapper',
  );
  if (onNode) return; // 节点右键由 onTreeRightClick 处理
  openCtxMenu(ev.clientX, ev.clientY, '');
}

function ctxNewFile() {
  closeCtxMenu();
  openCreateIn(ctxMenu.dirKey);
}

function ctxNewFolder() {
  closeCtxMenu();
  newFolderName.value = '';
  folderModalOpen.value = true;
}

function confirmNewFolder() {
  const name = newFolderName.value.trim().replaceAll(/^\/+|\/+$/g, '');
  if (!name) return;
  // dirKey 形如 /prod/demo（树 key 带前导斜杠）——拼装后统一归一化，
  // 否则首字符是 / 导致环境首段校验永假
  const full = (ctxMenu.dirKey ? `${ctxMenu.dirKey}/${name}` : name).replaceAll(
    /^\/+|\/+$/g,
    '',
  );
  const segs = full.split('/').filter(Boolean);
  const first = segs[0] ?? '';
  if (first !== 'prod' && !isSlotEnv(first)) {
    message.warning('首段须为 prod 或槽位名（dev1、dev2…）');
    return;
  }
  if (isSlotEnv(first) && !envSegments.value.includes(first)) {
    message.warning(
      `槽位 ${first} 超出项目槽位数（共 ${testSlotCount.value} 个）`,
    );
    return;
  }
  if (!ephemeralDirs.value.includes(full)) ephemeralDirs.value.push(full);
  // 展开新目录并把地址栏切过去
  let prefix = '';
  for (const seg of segs) {
    prefix = prefix ? `${prefix}/${seg}` : seg;
    if (!expandedKeys.value.includes(`/${prefix}`))
      expandedKeys.value.push(`/${prefix}`);
  }
  currentDir.value = segs;
  folderModalOpen.value = false;
}

// 在指定目录下新建文件（右键入口；openCreate 保留供历史调用，根=右键空白处）
function openCreateIn(dirKey: string) {
  editingId.value = null;
  Object.assign(form, {
    name: '',
    serverId: undefined,
    path: '',
    relPath: dirKey.replaceAll(/^\/+|\/+$/g, ''),
    format: 'yaml',
    content: '',
    applyAction: 'none',
    applyTarget: '',
    remark: '',
  });
  formOpen.value = true;
}

// ---- 更多菜单：历史版本 / 环境同步 / 导入 / 导出 ----
const historyOpen = ref(false);
const importInput = ref<HTMLInputElement | null>(null);

function onMoreMenu({ key }: { key: number | string }) {
  if (key === 'history') historyOpen.value = true;
  if (key === 'env-sync') {
    envSyncForm.sourceEnv =
      currentDir.value[0] ?? envOptions.value.at(0)?.value;
    envSyncForm.targetEnv = undefined;
    envSyncForm.subPath = currentDir.value.slice(1).join('/');
    envSyncOpen.value = true;
  }
  if (key === 'export') exportFile();
  if (key === 'import') importInput.value?.click();
}

const EXT_BY_FORMAT: Record<string, string> = {
  env: '.env',
  ini: '.ini',
  json: '.json',
  toml: '.toml',
  yaml: '.yaml',
};

function exportFile() {
  if (!selected.value) return;
  const ext = EXT_BY_FORMAT[selected.value.format] ?? '.txt';
  const blob = new Blob([content.value], { type: 'text/plain;charset=utf-8' });
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download =
    (selected.value.relPath.split('/').at(-1) ?? selected.value.name).replace(
      /\.[^.]+$/,
      '',
    ) + ext;
  a.click();
  URL.revokeObjectURL(url);
  message.success('已导出（含敏感字段时为脱敏内容，明文需先查看）');
}

async function onImportFile(ev: Event) {
  const input = ev.target as HTMLInputElement;
  const file = input.files?.[0];
  input.value = '';
  if (!file || !selected.value) return;
  if (contentMasked.value) {
    message.warning('脱敏视图不能导入（先查看明文）');
    return;
  }
  if (file.size > 1024 * 1024) {
    message.error('文件超过 1MB 上限');
    return; // 先按 size 拒绝，避免超大文件整体读入内存
  }
  const text = await file.text();
  content.value = text;
  message.success(`已导入 ${file.name} 到编辑器（点「仅保存」生成新版本）`);
}

// ---- 环境同步 ----
const envSyncOpen = ref(false);
const envSyncing = ref(false);
const envSyncForm = reactive({
  sourceEnv: undefined as string | undefined,
  targetEnv: undefined as string | undefined,
  subPath: '',
});

async function doEnvSync() {
  if (
    !currentProject.value ||
    !envSyncForm.sourceEnv ||
    !envSyncForm.targetEnv
  ) {
    message.warning('请选择源与目标环境');
    return;
  }
  envSyncing.value = true;
  try {
    const r = await envSyncConfigApi({
      projectId: currentProject.value,
      sourceEnv: envSyncForm.sourceEnv,
      targetEnv: envSyncForm.targetEnv,
      subPath: envSyncForm.subPath.trim(),
    });
    message.success(
      `同步完成：新建 ${r.created}、覆盖 ${r.updated}（不自动下发）`,
    );
    // 覆盖风险（源路径无 {env} 且同主机落盘同路径）：黄条逐条展示，不阻断
    for (const w of r.warnings ?? []) message.warning(w, 8);
    envSyncOpen.value = false;
    await load();
  } catch (error: any) {
    message.error(extractErrMsg(error, '同步失败'));
  } finally {
    envSyncing.value = false;
  }
}

// ---- 版本侧栏：双选 diff + 回滚 ----
const versions = ref<ConfigVersion[]>([]);
const diffPair = ref<ConfigVersion[]>([]);
const diffOriginal = ref('');
const diffModified = ref('');

async function loadVersions(id: number) {
  versions.value = await getConfigVersionsApi(id);
}

async function toggleDiff(v: ConfigVersion, checked: any) {
  if (checked) {
    diffPair.value =
      diffPair.value.length >= 2
        ? [diffPair.value.at(-1) ?? v, v]
        : [...diffPair.value, v];
  } else {
    diffPair.value = diffPair.value.filter((x) => x.id !== v.id);
  }
  await refreshDiff();
}

async function refreshDiff() {
  if (!selected.value || diffPair.value.length !== 2) {
    diffOriginal.value = '';
    diffModified.value = '';
    return;
  }
  const [a, b] = diffPair.value as [ConfigVersion, ConfigVersion];
  const [ra, rb] = await Promise.all([
    getConfigVersionContentApi(selected.value.id, a.id),
    getConfigVersionContentApi(selected.value.id, b.id),
  ]);
  diffOriginal.value = ra.content;
  diffModified.value = rb.content;
}

// 单版本查看：与当前内容做 diff（不再覆盖主编辑器——此前历史内容可被直接
// 编辑/保存，且无「正在查看历史版本」标记，易误操作）
const versionDiffOpen = ref(false);
const versionDiffMeta = ref<null | { createdAt: string; hash: string }>(null);
const versionDiffOriginal = ref(''); // 历史版本
const versionDiffModified = ref(''); // 当前已保存内容
const versionDiffLang = computed(() =>
  selected.value ? (FORMAT_LANG[selected.value.format] ?? 'yaml') : 'yaml',
);
const fmtVersionAt = computed(() =>
  versionDiffMeta.value?.createdAt
    ? new Date(versionDiffMeta.value.createdAt).toLocaleString('zh-CN', {
        hour12: false,
      })
    : '',
);

async function viewVersion(v: ConfigVersion) {
  if (!selected.value) return;
  // 版本查看跟随当前明文状态（已 reveal 的会话版本查看也免打码；
  // 后端仍按环境分流：正式环境无权限 reveal 返回脱敏内容）
  const r = await getConfigVersionContentApi(
    selected.value.id,
    v.id,
    !contentMasked.value,
  );
  versionDiffOriginal.value = r.content;
  versionDiffModified.value = content.value;
  versionDiffMeta.value = { hash: v.hash, createdAt: v.createdAt };
  versionDiffOpen.value = true;
}

async function doRollback(v: ConfigVersion) {
  if (!selected.value) return;
  await rollbackConfigApi(selected.value.id, v.id);
  message.success(`已回滚到 ${v.hash}（内容未下发，请手动下发）`);
  await openContent(selected.value);
}

// ---- 新建/设置（含环境默认目标预填）----
const formOpen = ref(false);
const saving = ref(false);
const editingId = ref<null | number>(null);
const form = reactive({
  name: '',
  serverId: undefined as number | undefined,
  path: '',
  relPath: '',
  format: 'yaml' as 'env' | 'ini' | 'json' | 'toml' | 'yaml',
  content: '',
  applyAction: 'none' as 'http' | 'none' | 'restart' | 'sighup',
  applyTarget: '',
  remark: '',
});

// 环境同步/层级提示的环境选项：prod + 槽位名（canary 引用 prod 不在列）
const envOptions = computed(() =>
  envSegments.value.map((e) => ({
    label: e,
    value: e,
  })),
);

// {env} 占位符渲染预览（与后端 renderPath 同语义；仅表单预览，实际下发以后端为准）
function renderEnvPath(path: string, relPath: string) {
  if (!path.includes('{env}')) return path;
  const seg = (relPath.split('/').find(Boolean) ?? '').trim();
  const env = seg === '' || seg === 'canary' ? 'prod' : seg;
  return path.replaceAll('{env}', env);
}
const formRenderedPath = computed(() => renderEnvPath(form.path, form.relPath));
// 环境段变化 → 下发目标默认带出（首段映射环境族：devN→test；可覆盖）
watch(
  () => form.relPath.split('/')[0],
  (env, old) => {
    if (!editingId.value && env && env !== old) {
      const t = envTargets.value.find((x) => x.envType === envTypeOf(env));
      if (t && !form.serverId) form.serverId = t.serverId;
    }
  },
);

function openEdit(f: ConfigFile) {
  editingId.value = f.id;
  Object.assign(form, {
    name: f.name,
    serverId: f.serverId,
    path: f.path,
    relPath: f.relPath,
    format: f.format,
    content: '',
    applyAction: f.applyAction,
    applyTarget: f.applyTarget,
    remark: f.remark,
  });
  formOpen.value = true;
}

async function save() {
  if (!form.name || !form.serverId || !form.path) {
    message.warning('请填写名称、主机与远端路径');
    return;
  }
  saving.value = true;
  try {
    const payload = {
      ...form,
      projectId: currentProject.value ?? 0,
      relPath: form.relPath.trim(),
      serverId: form.serverId ?? 0,
    };
    if (editingId.value) {
      await updateConfigFileApi(editingId.value, payload);
      message.success('已保存');
    } else {
      await createConfigFileApi(payload);
      message.success('已创建');
    }
    formOpen.value = false;
    await load();
  } catch (error: any) {
    message.error(extractErrMsg(error, '保存失败'));
  } finally {
    saving.value = false;
  }
}

async function remove(f: ConfigFile) {
  await deleteConfigFileApi(f.id);
  if (selected.value?.id === f.id) selected.value = null;
  message.success('已删除');
  await load();
}

// ---- 合并视图（AgileConfig 式 UI 适配）：项目×环境聚合最终生效配置，只读 ----
const mergedOpen = ref(false);
const mergedEnv = ref<string>('prod');
const mergedFormat = ref<'json' | 'yaml'>('json');
// canary（引用 prod）附在末尾——读侧后端映射到 prod 内容
const mergedEnvOptions = computed(() => [
  ...envSegments.value.map((e) => ({ label: e, value: e })),
  { label: 'canary（引用 prod）', value: 'canary' },
]);
const mergedBody = ref('');
const mergedMeta = ref<null | { files: number; version: string }>(null);
const mergedLoading = ref(false);
// 敏感字段默认脱敏；查看明文走后端 reveal 分流（正式环境需 config.reveal）
const mergedReveal = ref(false);

async function loadMerged() {
  if (!currentProject.value) return;
  mergedLoading.value = true;
  try {
    const r = await getMergedConfigApi(
      currentProject.value,
      mergedEnv.value,
      mergedFormat.value,
      mergedReveal.value,
    );
    mergedBody.value = r.body;
    mergedMeta.value = { files: r.files, version: r.version };
  } catch (error: any) {
    mergedBody.value = '';
    mergedMeta.value = null;
    message.error(extractErrMsg(error, '聚合失败'));
  } finally {
    mergedLoading.value = false;
  }
}

function openMerged() {
  mergedOpen.value = true;
  mergedReveal.value = false;
  loadMerged();
}

async function toggleMergedReveal() {
  mergedReveal.value = !mergedReveal.value;
  await loadMerged();
}
</script>

<template>
  <div class="flex h-full flex-col gap-2 p-3">
    <!-- 顶部：项目选择 + 地址栏（面包屑 + 一键跳转） -->
    <div class="flex items-center gap-2">
      <a-select
        v-model:value="currentProject"
        :options="projects.map((p) => ({ label: p.name, value: p.id }))"
        placeholder="选择项目"
        show-search
        option-filter-prop="label"
        style="width: 180px"
      />
      <a-button @click="openMerged">合并视图</a-button>
      <div
        class="bg-muted flex min-w-0 flex-1 items-center gap-1 rounded px-2 py-1"
      >
        <span class="text-muted-foreground">/</span>
        <template v-for="(seg, i) in currentDir" :key="i">
          <a @click="crumbTo(i)">{{ seg }}</a>
          <span class="text-muted-foreground">/</span>
        </template>
        <a-input
          v-model:value="jumpPath"
          placeholder="输入路径跳转，如 prod/sub"
          size="small"
          variant="borderless"
          class="ml-auto max-w-64"
          @press-enter="jump"
        />
      </div>
    </div>

    <div class="flex min-h-0 flex-1 gap-2">
      <!-- 左：目录树（环境层固化：prod + 槽位目录） -->
      <div
        class="w-64 shrink-0 overflow-auto rounded border p-1"
        @contextmenu.prevent="onRootRightClick"
      >
        <a-spin :spinning="loading">
          <a-tree
            v-model:expanded-keys="expandedKeys"
            :field-names="{ title: 'title', key: 'key', children: 'children' }"
            :tree-data="treeData"
            block-node
            show-icon
            @right-click="onTreeRightClick"
            @select="onSelectFile"
          >
            <template #title="{ dataRef }">
              <span :class="{ 'font-medium text-blue-600': dataRef.isLeaf }">
                {{ dataRef.title }}
              </span>
            </template>
          </a-tree>
        </a-spin>
      </div>

      <!-- 右：编辑区 + 版本侧栏 -->
      <div class="flex min-w-0 flex-1 gap-2">
        <div class="flex min-w-0 flex-1 flex-col gap-2 rounded border p-2">
          <template v-if="selected">
            <div class="flex flex-wrap items-center gap-2">
              <span class="font-medium">{{ selected.name }}</span>
              <a-tag class="ml-1" color="geekblue">
                {{ serverName(selected.serverId) }}
              </a-tag>
              <a-tag>
                <a-tooltip
                  v-if="selected.renderedPath !== selected.path"
                  :title="`路径模板 ${selected.path}，{env} 按该文件层级首段渲染`"
                >
                  {{ selected.renderedPath }}
                </a-tooltip>
                <template v-else>{{ selected.path }}</template>
              </a-tag>
              <span class="text-muted-foreground text-xs">
                {{ actionText(selected.applyAction, selected.applyTarget) }}
              </span>
              <!-- 语言按钮组（GitHub 风格，切换编辑器高亮） -->
              <a-radio-group
                v-model:value="viewFormat"
                class="ml-auto"
                size="small"
                button-style="solid"
              >
                <a-radio-button v-for="f in CONFIG_FORMATS" :key="f" :value="f">
                  {{ f.toUpperCase() }}
                </a-radio-button>
              </a-radio-group>
            </div>
            <div class="flex items-center gap-2">
              <!-- P7-M3 编辑器 AI（advisory）：生成 compose 骨架/排查配置问题；
                   插入=替换编辑器内容（Monaco ctrl+z 可撤销），AI 不直接改文件 -->
              <AiAssist
                :context="{
                  content: editorText,
                  fileName: selected?.name,
                  fileType: selected?.format,
                }"
                scene="editor"
                @apply="(v) => (editorText = v)"
              >
                <template #default="{ open }">
                  <a-button
                    :disabled="contentMasked"
                    size="small"
                    title="AI 生成/排错（建议需审阅后插入）"
                    @click="open"
                  >
                    ✨ AI
                  </a-button>
                </template>
              </AiAssist>
              <a-button
                :loading="deploying === selected.id"
                size="small"
                type="primary"
                @click="deploy"
              >
                下发
              </a-button>
              <a-button
                :disabled="contentMasked"
                :loading="savingContent"
                size="small"
                @click="saveContent"
              >
                仅保存
              </a-button>
              <a-button
                v-if="contentMasked"
                :loading="revealLoading"
                size="small"
                @click="reveal"
              >
                查看明文（审计）
              </a-button>
              <a-dropdown>
                <a-button size="small">
                  更多
                  <DownOutlined class="ml-1 text-xs" />
                </a-button>
                <template #overlay>
                  <a-menu @click="onMoreMenu">
                    <a-menu-item key="history">历史版本</a-menu-item>
                    <a-menu-item key="env-sync">环境同步</a-menu-item>
                    <a-menu-item key="export">导出</a-menu-item>
                    <a-menu-item key="import">导入</a-menu-item>
                  </a-menu>
                </template>
              </a-dropdown>
              <a-button size="small" @click="openEdit(selected)">设置</a-button>
              <a-popconfirm title="确认删除？" @confirm="remove(selected)">
                <a-button danger size="small">删除</a-button>
              </a-popconfirm>
              <a-tag v-if="contentMasked" class="ml-auto" color="orange">
                脱敏视图（敏感字段；编辑禁用）
              </a-tag>
            </div>
          </template>
          <a-alert
            v-if="isConvertedView || viewConvertError"
            :message="
              viewConvertError ||
              `结构视图：由 ${selected?.format.toUpperCase()} 转换渲染为 ${viewFormat.toUpperCase()}；保存将回写为 ${selected?.format.toUpperCase()}（注释与键序不保留）`
            "
            :type="viewConvertError ? 'error' : 'warning'"
            class="shrink-0"
            show-icon
          />
          <div v-show="selected" class="editor-host min-h-0 flex-1">
            <YamlEditor
              v-model="editorText"
              height="calc(100vh - 320px)"
              :language="editorLang"
              :read-only="contentMasked"
            />
          </div>
          <a-empty
            v-if="!selected"
            class="m-auto"
            description="从左侧选择文件（目录树按 prod/槽位/子目录 组织）"
          />
        </div>
      </div>
    </div>

    <!-- 历史版本抽屉（宽，diff 并排） -->
    <a-drawer
      v-model:open="historyOpen"
      :title="`历史版本 · ${selected?.name ?? ''}`"
      width="min(1100px, 92vw)"
    >
      <div class="flex flex-col gap-3">
        <div class="text-muted-foreground text-xs">
          勾选任意两个版本对比差异（git diff
          视图，并排/行内可切）；回滚会生成新版本，需手动下发
        </div>
        <a-table
          :columns="[
            { title: '', key: 'pick', width: 32 },
            { title: '指纹', dataIndex: 'hash', width: 130 },
            { title: '来源', dataIndex: 'source', width: 90 },
            { title: '操作人', dataIndex: 'createdBy', width: 120 },
            { title: '时间', dataIndex: 'createdAt' },
            { title: '操作', key: 'op', width: 150 },
          ]"
          :data-source="versions"
          :pagination="{ pageSize: 10 }"
          row-key="id"
          size="small"
        >
          <template #bodyCell="{ column, record }">
            <template v-if="column.key === 'pick'">
              <a-checkbox
                :checked="diffPair.some((d) => d.id === record.id)"
                :disabled="
                  !diffPair.some((d) => d.id === record.id) &&
                  diffPair.length >= 2
                "
                @change="(e: any) => toggleDiff(record, e.target.checked)"
              />
            </template>
            <template v-else-if="column.key === 'op'">
              <a-space>
                <a-button size="small" type="link" @click="viewVersion(record)">
                  查看
                </a-button>
                <a-popconfirm
                  :title="`回滚到 ${record.hash}？（回滚后需手动下发）`"
                  @confirm="doRollback(record)"
                >
                  <a-button danger size="small" type="link">回滚</a-button>
                </a-popconfirm>
              </a-space>
            </template>
          </template>
        </a-table>
        <template v-if="diffPair.length === 2">
          <div class="text-muted-foreground text-xs">
            {{ diffPair[0]?.hash }} → {{ diffPair[1]?.hash }}
          </div>
          <DiffEditor
            :language="editorLang"
            :original="diffOriginal"
            :value="diffModified"
            height="380px"
          />
        </template>
      </div>
    </a-drawer>

    <!-- 单版本 × 当前内容对比（历史版本抽屉「查看」入口） -->
    <a-modal
      v-model:open="versionDiffOpen"
      :footer="null"
      :title="`版本对比 · ${versionDiffMeta?.hash ?? ''} → 当前`"
      :width="880"
    >
      <div class="text-muted-foreground mb-2 text-xs">
        左侧=历史版本 {{ versionDiffMeta?.hash }}（{{
          fmtVersionAt
        }}），右侧=当前已保存内容；需整体恢复用「回滚」（生成新版本，手动下发）
      </div>
      <DiffEditor
        :language="versionDiffLang"
        :original="versionDiffOriginal"
        :value="versionDiffModified"
        height="420px"
      />
    </a-modal>

    <!-- 合并视图（只读）：项目×环境聚合最终生效配置 = 拉取 API 返回内容预览 -->
    <a-modal
      v-model:open="mergedOpen"
      :title="`合并视图 · ${currentProjectName ?? ''} / ${mergedEnv}（最终生效配置，只读）`"
      :width="860"
      :footer="null"
    >
      <div class="mb-2 flex flex-wrap items-center gap-2">
        <span class="text-xs text-muted-foreground">环境</span>
        <a-select
          v-model:value="mergedEnv"
          :options="mergedEnvOptions"
          size="small"
          style="width: 140px"
          @change="loadMerged"
        />
        <a-segmented
          v-model:value="mergedFormat"
          :options="[
            { value: 'json', label: 'JSON' },
            { value: 'yaml', label: 'YAML' },
          ]"
          size="small"
          @change="loadMerged"
        />
        <span v-if="mergedMeta" class="text-xs text-muted-foreground">
          {{ mergedMeta.files }} 个文件聚合 · 版本
          {{ mergedMeta.version.slice(0, 12) }}
        </span>
        <a-tag v-if="!mergedReveal" color="orange">敏感字段已脱敏</a-tag>
        <div class="flex-1"></div>
        <a-button size="small" @click="toggleMergedReveal">
          {{ mergedReveal ? '恢复脱敏' : '查看明文（审计）' }}
        </a-button>
        <a-button size="small" @click="loadMerged">刷新</a-button>
      </div>
      <a-spin :spinning="mergedLoading">
        <YamlEditor
          v-if="mergedBody"
          :model-value="mergedBody"
          :language="mergedFormat"
          :read-only="true"
          height="480px"
        />
        <a-empty
          v-else-if="!mergedLoading"
          description="该应用×环境无配置文件"
        />
      </a-spin>
    </a-modal>

    <!-- 环境同步模态 -->
    <a-modal
      v-model:open="envSyncOpen"
      :confirm-loading="envSyncing"
      title="环境同步"
      width="520px"
      @ok="doEnvSync"
    >
      <a-form layout="vertical">
        <a-form-item
          extra="把源环境（可选子前缀）下的全部配置文件内容完整同步到目标环境（prod ↔ 槽位）：已存在则生成新版本，不存在则建档（目标主机自动取目标环境族部署目标）。仅同步内容，不自动下发。"
          label="同步范围"
          required
        >
          <div class="flex items-center gap-2">
            <a-select
              v-model:value="envSyncForm.sourceEnv"
              :options="envOptions"
              placeholder="源环境"
              style="width: 120px"
            />
            <span>→</span>
            <a-select
              v-model:value="envSyncForm.targetEnv"
              :options="
                envOptions.filter((o) => o.value !== envSyncForm.sourceEnv)
              "
              placeholder="目标环境"
              style="width: 120px"
            />
          </div>
        </a-form-item>
        <a-form-item
          extra="留空 = 同步该环境全部；如 dev1 只同步该子目录"
          label="子前缀（当前路径）"
        >
          <a-input
            v-model:value="envSyncForm.subPath"
            :placeholder="currentDir.slice(1).join('/')"
          />
        </a-form-item>
      </a-form>
    </a-modal>

    <!-- 目录右键菜单（Windows 风格：新建文件/新建文件夹） -->
    <teleport to="body">
      <div
        v-if="ctxMenu.open"
        :style="{
          left: `${ctxMenu.x}px`,
          position: 'fixed',
          top: `${ctxMenu.y}px`,
          zIndex: 1050,
        }"
        class="border-border bg-popover text-popover-foreground min-w-36 rounded border py-1 shadow-lg"
      >
        <div
          class="hover:bg-accent cursor-pointer px-3 py-1.5"
          @click="ctxNewFile"
        >
          新建文件
        </div>
        <div
          class="hover:bg-accent cursor-pointer px-3 py-1.5"
          @click="ctxNewFolder"
        >
          新建文件夹
        </div>
      </div>
    </teleport>

    <!-- 新建文件夹（虚拟目录，会话内展示；落文件后固化） -->
    <a-modal
      v-model:open="folderModalOpen"
      :extra="`将在 ${ctxMenu.dirKey || '根目录'} 下创建`"
      title="新建文件夹"
      width="420px"
      @ok="confirmNewFolder"
    >
      <a-form-item
        :extra="
          ctxMenu.dirKey && !/^(prod|dev[1-9][0-9]*)(\/|$)/.test(ctxMenu.dirKey)
            ? '首段须为 prod 或槽位名（dev1、dev2…）'
            : '可多级（a/b）；首段为 prod 或槽位名'
        "
        label="文件夹名"
        required
      >
        <a-input
          v-model:value="newFolderName"
          @press-enter="confirmNewFolder"
        />
      </a-form-item>
    </a-modal>

    <!-- 导入（隐藏 file input，内容读入编辑器待保存） -->
    <input
      ref="importInput"
      accept=".json,.jsonc,.yaml,.yml,.toml,.ini,.env,.conf,.txt"
      hidden
      type="file"
      @change="onImportFile"
    />

    <!-- 新建/设置弹窗 -->
    <a-modal
      v-model:open="formOpen"
      :confirm-loading="saving"
      :title="editingId ? '编辑配置文件' : '新建配置文件'"
      width="640px"
      @ok="save"
    >
      <a-form layout="vertical">
        <a-form-item
          :extra="`首段为 prod 或槽位名（${envOptions.map((e) => e.label).join(' / ')}），更深层级自由；槽位目录须项目已绑定 test 部署目标`"
          label="层级路径"
          required
        >
          <a-input
            v-model:value="form.relPath"
            addon-before="层级"
            placeholder="prod/app.yaml 或 dev1/app.yaml（首段=prod/槽位）"
          />
        </a-form-item>
        <a-form-item label="名称" required>
          <a-input v-model:value="form.name" placeholder="如 myapp 主配置" />
        </a-form-item>
        <a-form-item label="目标主机" required>
          <a-select
            v-model:value="form.serverId"
            :options="servers.map((s) => ({ label: s.name, value: s.id }))"
            placeholder="选择主机（按路径首段自动带出环境族部署目标，可改）"
          />
        </a-form-item>
        <a-form-item
          :extra="
            form.path.includes('{env}')
              ? `实际下发：${formRenderedPath}（{env} 按层级首段渲染，多环境同模板各归其位）`
              : '支持 {env} 占位符（按层级路径首段渲染），如 /opt/app/{env}/config.yaml'
          "
          label="远端绝对路径"
          required
        >
          <a-input
            v-model:value="form.path"
            placeholder="/etc/myapp/config.yaml 或 /opt/app/{env}/config.yaml"
          />
        </a-form-item>
        <a-form-item label="格式（编辑器高亮）">
          <a-radio-group v-model:value="form.format">
            <a-radio value="yaml">yaml</a-radio>
            <a-radio value="json">json</a-radio>
            <a-radio value="toml">toml</a-radio>
            <a-radio value="env">.env</a-radio>
            <a-radio value="ini">ini</a-radio>
          </a-radio-group>
        </a-form-item>
        <a-form-item v-if="!editingId" label="初始内容（可留空稍后编辑）">
          <a-textarea v-model:value="form.content" :rows="5" />
        </a-form-item>
        <a-form-item label="生效动作（下发后）">
          <a-radio-group v-model:value="form.applyAction">
            <a-radio value="none">仅落盘</a-radio>
            <a-radio value="sighup">SIGHUP</a-radio>
            <a-radio value="http">refresh 端点</a-radio>
            <a-radio value="restart">重启容器</a-radio>
          </a-radio-group>
        </a-form-item>
        <a-form-item
          v-if="form.applyAction !== 'none'"
          :label="
            form.applyAction === 'sighup'
              ? '目标进程名（pkill -f 匹配）'
              : form.applyAction === 'http'
                ? 'refresh 端点 URL（POST）'
                : '容器名'
          "
          required
        >
          <a-input
            v-model:value="form.applyTarget"
            :placeholder="
              form.applyAction === 'http'
                ? 'http://myapp:8080/actuator/refresh'
                : form.applyAction === 'sighup'
                  ? 'myapp'
                  : 'myapp-container'
            "
          />
        </a-form-item>
        <a-form-item label="备注">
          <a-input v-model:value="form.remark" />
        </a-form-item>
      </a-form>
    </a-modal>
  </div>
</template>
