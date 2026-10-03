<script lang="ts" setup>
import type { ConfigFile, ConfigVersion } from '#/api/configs';

import { computed, onMounted, reactive, ref, watch } from 'vue';

import { message } from 'ant-design-vue';

import {
  createConfigFileApi,
  deleteConfigFileApi,
  deployConfigApi,
  getConfigContentApi,
  getConfigFilesApi,
  getConfigVersionContentApi,
  getConfigVersionsApi,
  rollbackConfigApi,
  saveConfigContentApi,
  updateConfigFileApi,
} from '#/api/configs';
import { getProjectApi, getProjectsApi } from '#/api/projects';
import { getServerListApi } from '#/api/resources/server';
import DiffEditor from '#/components/diff-editor.vue';
import YamlEditor from '#/components/yaml-editor.vue';

defineOptions({ name: 'ConfigsFiles' });

/**
 * 配置文件管理器（R2）：左侧目录树 + 顶部地址栏（面包屑/一键跳转）+
 * 右侧编辑器（语言按钮组切换高亮）+ 版本侧栏（双选 diff / 回滚）。
 * 层级语义：relPath 首段 = 环境（prod/canary/test，须项目已绑定部署目标），
 * 深层子目录自由（test/dev1/... 对应槽位目录）。
 */

const loading = ref(false);
const allFiles = ref<ConfigFile[]>([]);
const servers = ref<Array<{ id: number; name: string }>>([]);
const projects = ref<Array<{ id: number; name: string }>>([]);
const envTargets = ref<Array<{ envType: string; serverId: number }>>([]);
const currentProject = ref<number | undefined>(undefined);

const FORMAT_LANG: Record<string, string> = {
  env: 'ini',
  ini: 'ini',
  json: 'json',
  toml: 'ini',
  yaml: 'yaml',
};
const LANG_LABEL: Record<string, string> = {
  ini: 'INI',
  json: 'JSON',
  yaml: 'YAML',
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

// 项目切换 → 拉环境绑定（层级校验/下发默认目标用）
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

const treeData = computed<TreeNode[]>(() => {
  const root: TreeNode = { key: '', title: '', isLeaf: false, children: [] };
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
    openContent(f);
    return;
  }
  // 目录节点：点击切换展开（expandedKeys 受控，antd 不会自动加）
  const key = info?.node?.key;
  if (typeof key === 'string' && key) {
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
      const segs = f.relPath.split('/').filter(Boolean);
      currentDir.value = segs.slice(0, -1);
      let prefix = '';
      for (const seg of segs.slice(0, -1)) {
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
const langOverride = ref<null | string>(null);

const editorLang = computed(() => {
  if (langOverride.value) return langOverride.value;
  return FORMAT_LANG[selected.value?.format ?? 'yaml'] ?? 'yaml';
});

async function openContent(f: ConfigFile) {
  selected.value = f;
  langOverride.value = null;
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
  } finally {
    revealLoading.value = false;
  }
}

const savingContent = ref(false);
async function saveContent() {
  if (!selected.value || contentMasked.value) return;
  savingContent.value = true;
  try {
    await saveConfigContentApi(selected.value.id, content.value);
    message.success('内容已保存（新版本）');
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
    message.error(error?.response?.data?.message || '下发失败');
  } finally {
    deploying.value = 0;
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

async function viewVersion(v: ConfigVersion) {
  if (!selected.value) return;
  const r = await getConfigVersionContentApi(selected.value.id, v.id);
  content.value = r.content;
  contentMasked.value = selected.value.sensitive;
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
  sensitive: false,
  content: '',
  applyAction: 'none' as 'http' | 'none' | 'restart' | 'sighup',
  applyTarget: '',
  remark: '',
});

const envOptions = computed(() =>
  envTargets.value.map((t) => ({
    label: t.envType,
    value: t.envType,
  })),
);
// 环境段变化 → 下发目标默认带出（可覆盖）
watch(
  () => form.relPath.split('/')[0],
  (env, old) => {
    if (!editingId.value && env && env !== old) {
      const t = envTargets.value.find((x) => x.envType === env);
      if (t && !form.serverId) form.serverId = t.serverId;
    }
  },
);

function openCreate() {
  editingId.value = null;
  Object.assign(form, {
    name: '',
    serverId: undefined,
    path: '',
    relPath: currentDir.value.join('/'),
    format: 'yaml',
    sensitive: false,
    content: '',
    applyAction: 'none',
    applyTarget: '',
    remark: '',
  });
  formOpen.value = true;
}

function openEdit(f: ConfigFile) {
  editingId.value = f.id;
  Object.assign(form, {
    name: f.name,
    serverId: f.serverId,
    path: f.path,
    relPath: f.relPath,
    format: f.format,
    sensitive: f.sensitive,
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
    message.error(error?.response?.data?.message || '保存失败');
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
      <a-button type="primary" @click="openCreate">新建文件</a-button>
    </div>

    <div class="flex min-h-0 flex-1 gap-2">
      <!-- 左：目录树 -->
      <div class="w-64 shrink-0 overflow-auto rounded border p-1">
        <a-spin :spinning="loading">
          <a-tree
            v-if="treeData.length > 0"
            v-model:expanded-keys="expandedKeys"
            :field-names="{ title: 'title', key: 'key', children: 'children' }"
            :tree-data="treeData"
            block-node
            show-icon
            @select="onSelectFile"
          >
            <template #title="{ dataRef }">
              <span :class="{ 'font-medium text-blue-600': dataRef.isLeaf }">
                {{ dataRef.title }}
              </span>
            </template>
          </a-tree>
          <a-empty
            v-else
            description="暂无文件（新建时路径首段为环境名）"
            :image-style="{ height: '40px' }"
          />
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
              <a-tag>{{ selected.path }}</a-tag>
              <a-tag v-if="selected.sensitive" color="red">敏感</a-tag>
              <span class="text-muted-foreground text-xs">
                {{ actionText(selected.applyAction, selected.applyTarget) }}
              </span>
              <!-- 语言按钮组（GitHub 风格，切换编辑器高亮） -->
              <a-radio-group
                v-model:value="langOverride"
                class="ml-auto"
                size="small"
                button-style="solid"
                @change="(e: any) => (langOverride = e.target.value)"
              >
                <a-radio-button
                  v-for="(label, lang) in LANG_LABEL"
                  :key="lang"
                  :value="lang"
                >
                  {{ label }}
                </a-radio-button>
              </a-radio-group>
            </div>
            <div class="flex items-center gap-2">
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
                v-if="selected.sensitive && contentMasked"
                :loading="revealLoading"
                size="small"
                @click="reveal"
              >
                查看明文（审计）
              </a-button>
              <a-button size="small" @click="openEdit(selected)">设置</a-button>
              <a-popconfirm title="确认删除？" @confirm="remove(selected)">
                <a-button danger size="small">删除</a-button>
              </a-popconfirm>
              <a-tag v-if="contentMasked" class="ml-auto" color="orange">
                脱敏视图（编辑禁用）
              </a-tag>
            </div>
            <a-spin
              :spinning="contentLoading"
              wrapper-class-name="min-h-0 flex-1"
            >
              <YamlEditor
                v-model="content"
                height="100%"
                :language="editorLang"
                :read-only="contentMasked"
              />
            </a-spin>
          </template>
          <a-empty
            v-else
            class="m-auto"
            description="从左侧选择文件（目录树按 环境/子目录 组织）"
          />
        </div>

        <!-- 版本侧栏 -->
        <div
          v-if="selected"
          class="flex w-80 shrink-0 flex-col gap-2 overflow-auto rounded border p-2"
        >
          <div class="text-sm font-medium">版本历史（勾选两个对比 diff）</div>
          <a-table
            :columns="[
              { title: '', key: 'pick', width: 32 },
              { title: '指纹', dataIndex: 'hash', width: 110 },
              { title: '来源', dataIndex: 'source', width: 70 },
              { title: '操作', key: 'op', width: 120 },
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
                  <a-button
                    size="small"
                    type="link"
                    @click="viewVersion(record)"
                  >
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
              height="360px"
            />
          </template>
        </div>
      </div>
    </div>

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
          :extra="`首段为环境名（${envOptions.map((e) => e.label).join(' / ') || '该项目未绑定环境'}），更深层层级自由`"
          label="层级路径"
          required
        >
          <a-input
            v-model:value="form.relPath"
            addon-before="层级"
            placeholder="prod/sub/app.yaml（首段=环境）"
          />
        </a-form-item>
        <a-form-item label="名称" required>
          <a-input v-model:value="form.name" placeholder="如 myapp 主配置" />
        </a-form-item>
        <a-form-item label="目标主机" required>
          <a-select
            v-model:value="form.serverId"
            :options="servers.map((s) => ({ label: s.name, value: s.id }))"
            placeholder="选择主机（按环境段自动带出该环境部署目标，可改）"
          />
        </a-form-item>
        <a-form-item label="远端绝对路径" required>
          <a-input
            v-model:value="form.path"
            placeholder="/etc/myapp/config.yaml"
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
        <a-form-item label="敏感文件（默认脱敏，明文查看落审计）">
          <a-switch v-model:checked="form.sensitive" />
        </a-form-item>
        <a-form-item label="备注">
          <a-input v-model:value="form.remark" />
        </a-form-item>
      </a-form>
    </a-modal>
  </div>
</template>
