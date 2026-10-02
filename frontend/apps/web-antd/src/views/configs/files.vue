<script lang="ts" setup>
import type { ConfigFile, ConfigVersion } from '#/api/configs';

import { onMounted, reactive, ref } from 'vue';

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
import { getServerListApi } from '#/api/resources/server';
import YamlEditor from '#/components/yaml-editor.vue';

defineOptions({ name: 'ConfigsFiles' });

const loading = ref(false);
const list = ref<ConfigFile[]>([]);
const servers = ref<Array<{ id: number; name: string }>>([]);

const FORMAT_LANG: Record<string, string> = {
  yaml: 'yaml',
  json: 'json',
  toml: 'ini',
  env: 'ini',
  ini: 'ini',
};
function actionText(action: string, target: string) {
  if (action === 'none' || !action) return '仅落盘';
  const label: Record<string, string> = {
    sighup: 'SIGHUP',
    http: 'refresh 端点',
    restart: '重启容器',
  };
  return `${label[action] ?? action}：${target}`;
}

async function load() {
  loading.value = true;
  try {
    const [fs, ss] = await Promise.all([
      getConfigFilesApi(),
      getServerListApi(),
    ]);
    list.value = fs;
    servers.value = ss;
  } finally {
    loading.value = false;
  }
}

onMounted(load);

const serverName = (id: number) =>
  servers.value.find((s) => s.id === id)?.name ?? `#${id}`;

const columns = [
  { title: 'ID', dataIndex: 'id', width: 56 },
  { title: '名称', dataIndex: 'name' },
  { title: '主机', key: 'server', width: 120 },
  { title: '路径', dataIndex: 'path' },
  { title: '格式', dataIndex: 'format', width: 70 },
  { title: '生效动作', key: 'action', width: 110 },
  { title: '敏感', dataIndex: 'sensitive', width: 70 },
  { title: '操作', key: 'action-btn', width: 250 },
];

// ---- 新建/编辑 ----
const formOpen = ref(false);
const saving = ref(false);
const editingId = ref<null | number>(null);
const form = reactive({
  name: '',
  serverId: undefined as number | undefined,
  path: '',
  format: 'yaml' as 'env' | 'ini' | 'json' | 'toml' | 'yaml',
  sensitive: false,
  content: '',
  applyAction: 'none' as 'http' | 'none' | 'restart' | 'sighup',
  applyTarget: '',
  remark: '',
});

function openCreate() {
  editingId.value = null;
  Object.assign(form, {
    name: '',
    serverId: undefined,
    path: '',
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
    message.warning('请填写名称、主机与路径');
    return;
  }
  saving.value = true;
  try {
    const payload = { ...form, serverId: form.serverId ?? 0 };
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
  message.success('已删除');
  await load();
}

// ---- 内容编辑抽屉 ----
const editDrawer = ref(false);
const editFile = ref<ConfigFile | null>(null);
const content = ref('');
const contentMasked = ref(false);
const contentLoading = ref(false);
const revealLoading = ref(false);

const editorLang = () =>
  FORMAT_LANG[editFile.value?.format ?? 'yaml'] ?? 'yaml';

async function openContent(f: ConfigFile) {
  editFile.value = f;
  contentMasked.value = false;
  editDrawer.value = true;
  contentLoading.value = true;
  try {
    const r = await getConfigContentApi(f.id);
    content.value = r.content;
    contentMasked.value = r.masked;
  } finally {
    contentLoading.value = false;
  }
}

async function reveal() {
  if (!editFile.value) return;
  revealLoading.value = true;
  try {
    const r = await getConfigContentApi(editFile.value.id, true);
    content.value = r.content;
    contentMasked.value = false;
    message.info('已显示明文（本次查看已落审计）');
  } finally {
    revealLoading.value = false;
  }
}

async function saveContent() {
  if (!editFile.value || contentMasked.value) return;
  await saveConfigContentApi(editFile.value.id, content.value);
  message.success('内容已保存（新版本）');
}

const deploying = ref(0);
async function deploy() {
  if (!editFile.value) return;
  deploying.value = editFile.value.id;
  try {
    await deployConfigApi(editFile.value.id);
    message.success('已下发（旧文件已备份 .bak.时间戳）');
    await loadVersions(editFile.value.id);
  } catch (error: any) {
    message.error(error?.response?.data?.message || '下发失败');
  } finally {
    deploying.value = 0;
  }
}

// ---- 版本 ----
const versions = ref<ConfigVersion[]>([]);
async function loadVersions(id: number) {
  versions.value = await getConfigVersionsApi(id);
}

async function viewVersion(v: ConfigVersion) {
  if (!editFile.value) return;
  const r = await getConfigVersionContentApi(editFile.value.id, v.id);
  content.value = r.content;
  contentMasked.value = editFile.value.sensitive;
}

async function doRollback(v: ConfigVersion) {
  if (!editFile.value) return;
  await rollbackConfigApi(editFile.value.id, v.id);
  message.success(`已回滚到 ${v.hash}（内容未下发，请手动下发）`);
  await openContent(editFile.value);
  await loadVersions(editFile.value.id);
}
</script>

<template>
  <div class="p-4">
    <div class="mb-3 flex items-center justify-between">
      <span class="text-muted-foreground text-xs">
        文件级配置的真相源：版本/审计/脱敏/下发（旧文件自动备份）。热更边界：平台只负责落盘与生效动作，不承诺
        SDK 级热更
      </span>
      <a-button type="primary" @click="openCreate">新建配置文件</a-button>
    </div>

    <a-table
      :columns="columns"
      :data-source="list"
      :loading="loading"
      :pagination="false"
      row-key="id"
      size="small"
    >
      <template #bodyCell="{ column, record }">
        <template v-if="column.key === 'server'">
          {{ serverName(record.serverId) }}
        </template>
        <template v-else-if="column.key === 'action'">
          {{ actionText(record.applyAction, record.applyTarget) }}
        </template>
        <template v-else-if="column.dataIndex === 'sensitive'">
          <a-tag v-if="record.sensitive" color="red">敏感</a-tag>
          <a-tag v-else>常规</a-tag>
        </template>
        <template v-else-if="column.key === 'action-btn'">
          <a-space>
            <a-button size="small" type="primary" @click="openContent(record)">
              编辑
            </a-button>
            <a-button
              size="small"
              :loading="deploying === record.id"
              @click="
                () => {
                  editFile = record;
                  deploy();
                }
              "
            >
              下发
            </a-button>
            <a-button size="small" @click="openEdit(record)">设置</a-button>
            <a-popconfirm title="确认删除？" @confirm="remove(record)">
              <a-button danger size="small">删除</a-button>
            </a-popconfirm>
          </a-space>
        </template>
      </template>
    </a-table>

    <!-- 新建/设置弹窗 -->
    <a-modal
      v-model:open="formOpen"
      :confirm-loading="saving"
      :title="editingId ? '编辑配置文件' : '新建配置文件'"
      width="620px"
      @ok="save"
    >
      <a-form layout="vertical">
        <a-form-item label="名称" required>
          <a-input v-model:value="form.name" placeholder="如 myapp 主配置" />
        </a-form-item>
        <a-form-item label="目标主机" required>
          <a-select
            v-model:value="form.serverId"
            :options="servers.map((s) => ({ label: s.name, value: s.id }))"
            placeholder="选择主机"
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
        <a-form-item label="敏感文件（列表/编辑默认脱敏，明文查看落审计）">
          <a-switch v-model:checked="form.sensitive" />
        </a-form-item>
        <a-form-item label="备注">
          <a-input v-model:value="form.remark" />
        </a-form-item>
      </a-form>
    </a-modal>

    <!-- 内容编辑抽屉 -->
    <a-drawer
      v-model:open="editDrawer"
      :title="`${editFile?.name ?? ''} · ${editFile?.path ?? ''}`"
      width="760"
      @after-open-change="
        (o: boolean) => o && editFile && loadVersions(editFile.id)
      "
    >
      <div v-if="editFile" class="flex h-full flex-col gap-3">
        <a-space>
          <a-button
            size="small"
            :loading="deploying === editFile.id"
            type="primary"
            @click="deploy"
          >
            保存并下发
          </a-button>
          <a-button size="small" :disabled="contentMasked" @click="saveContent">
            仅保存
          </a-button>
          <a-button
            v-if="editFile.sensitive && contentMasked"
            size="small"
            :loading="revealLoading"
            @click="reveal"
          >
            查看明文（审计）
          </a-button>
          <a-tag v-if="contentMasked" color="orange">脱敏视图</a-tag>
        </a-space>
        <a-spin :spinning="contentLoading" wrapper-class-name="flex-1">
          <YamlEditor
            v-model="content"
            height="calc(100vh - 300px)"
            :language="editorLang()"
            :read-only="contentMasked"
          />
        </a-spin>
        <div>
          <div class="mb-2 text-sm font-medium">版本历史</div>
          <a-table
            :columns="[
              { title: '版本', dataIndex: 'hash', width: 130 },
              { title: '来源', dataIndex: 'source', width: 90 },
              { title: '操作人', dataIndex: 'createdBy', width: 120 },
              { title: '时间', dataIndex: 'createdAt' },
              { title: '操作', key: 'op', width: 160 },
            ]"
            :data-source="versions"
            :pagination="{ pageSize: 8 }"
            row-key="id"
            size="small"
          >
            <template #bodyCell="{ column, record }">
              <template v-if="column.key === 'op'">
                <a-space>
                  <a-button size="small" @click="viewVersion(record)">
                    查看
                  </a-button>
                  <a-popconfirm
                    :title="`回滚到 ${record.hash}？（回滚后需手动下发）`"
                    @confirm="doRollback(record)"
                  >
                    <a-button danger size="small">回滚</a-button>
                  </a-popconfirm>
                </a-space>
              </template>
            </template>
          </a-table>
        </div>
      </div>
    </a-drawer>
  </div>
</template>
