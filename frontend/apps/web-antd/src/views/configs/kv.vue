<script lang="ts" setup>
import type { KVDiff, KVItem } from '#/api/configs/kv';

import { onMounted, reactive, ref } from 'vue';

import {
  CloudUploadOutlined,
  EyeOutlined,
  SafetyCertificateOutlined,
  SyncOutlined,
} from '@ant-design/icons-vue';
import { message } from 'ant-design-vue';

import {
  createKVApi,
  deleteKVApi,
  getKVSettingsApi,
  listKVApi,
  pushKVApi,
  reconcileKVApi,
  revealKVApi,
  saveKVSettingsApi,
  updateKVApi,
} from '#/api/configs/kv';
import { getProjectsApi } from '#/api/projects';

defineOptions({ name: 'ConfigsKv' });

/**
 * K/V 配置（P6-M7，AgileConfig 共存）：平台为真相源单向推送，
 * 控制台手改视为漂移（对账告警不覆盖）。未配置 AgileConfig 时仅平台存储。
 */

const projects = ref<{ id: number; name: string }[]>([]);
const projectId = ref<number | undefined>();
const env = ref<'canary' | 'prod' | 'test'>('prod');
const items = ref<KVItem[]>([]);
const loading = ref(false);
const pushing = ref(false);
const reconciling = ref(false);

const settingsOpen = ref(false);
const settings = reactive({ configured: false, endpoint: '' });
const settingsForm = reactive({ endpoint: '', user: '', password: '' });

const editorOpen = ref(false);
const editing = ref<KVItem | null>(null);
const form = reactive({
  key: '',
  value: '',
  sensitive: false,
  remark: '',
});

const diff = ref<KVDiff | null>(null);
const diffOpen = ref(false);

async function loadProjects() {
  const list = await getProjectsApi();
  projects.value = list.map((p) => ({ id: p.id, name: p.name }));
  if (!projectId.value && projects.value.length > 0) {
    projectId.value = projects.value[0]!.id;
  }
}

async function load() {
  if (!projectId.value) return;
  loading.value = true;
  try {
    items.value = await listKVApi(projectId.value, env.value);
  } catch (e: any) {
    message.error(e?.response?.data?.message ?? '加载失败');
  } finally {
    loading.value = false;
  }
}

function openCreate() {
  editing.value = null;
  form.key = '';
  form.value = '';
  form.sensitive = false;
  form.remark = '';
  editorOpen.value = true;
}

function openEdit(it: KVItem) {
  editing.value = it;
  form.key = it.key;
  form.value = it.sensitive ? '' : it.value; // 敏感项不回填（需 reveal 取）
  form.sensitive = it.sensitive;
  form.remark = it.remark ?? '';
  editorOpen.value = true;
}

async function save() {
  if (!projectId.value || !form.key.trim()) return;
  const payload = {
    env: env.value,
    key: form.key.trim(),
    projectId: projectId.value,
    remark: form.remark.trim() || undefined,
    sensitive: form.sensitive,
    value: form.value,
  };
  try {
    if (editing.value) {
      await updateKVApi(editing.value.id, payload);
    } else {
      await createKVApi(payload);
    }
    editorOpen.value = false;
    await load();
  } catch (e: any) {
    message.error(e?.response?.data?.message ?? '保存失败');
  }
}

async function remove(it: KVItem) {
  await deleteKVApi(it.id);
  await load();
}

async function reveal(it: KVItem) {
  try {
    const v = await revealKVApi(it.id);
    navigator.clipboard?.writeText(v);
    message.success('明文已复制到剪贴板（查看已审计）');
  } catch (e: any) {
    message.error(e?.response?.data?.message ?? '查看失败');
  }
}

async function push() {
  if (!projectId.value) return;
  pushing.value = true;
  try {
    const n = await pushKVApi(projectId.value, env.value);
    message.success(`已推送 ${n} 项到 AgileConfig（并上线）`);
  } catch (e: any) {
    message.error(e?.response?.data?.message ?? '推送失败');
  } finally {
    pushing.value = false;
  }
}

async function reconcile() {
  if (!projectId.value) return;
  reconciling.value = true;
  try {
    diff.value = await reconcileKVApi(projectId.value, env.value);
    diffOpen.value = true;
  } catch (e: any) {
    message.error(e?.response?.data?.message ?? '对账失败');
  } finally {
    reconciling.value = false;
  }
}

async function openSettings() {
  const s = await getKVSettingsApi();
  settings.configured = s.configured;
  settings.endpoint = s.endpoint;
  settingsForm.endpoint = s.endpoint;
  settingsForm.user = '';
  settingsForm.password = '';
  settingsOpen.value = true;
}

async function saveSettings() {
  await saveKVSettingsApi({
    ...(settingsForm.endpoint ? { endpoint: settingsForm.endpoint } : {}),
    ...(settingsForm.user ? { user: settingsForm.user } : {}),
    ...(settingsForm.password ? { password: settingsForm.password } : {}),
  });
  settingsOpen.value = false;
  message.success('已保存（凭据 AES 落库）');
}

function onProjectChange() {
  diff.value = null;
  load();
}

onMounted(async () => {
  await loadProjects();
  await load();
});
</script>

<template>
  <div class="flex h-full flex-col gap-3 p-3">
    <!-- 工具栏：应用×环境 + 动作 -->
    <div class="flex flex-wrap items-center gap-2">
      <span class="text-xs text-muted-foreground">应用</span>
      <a-select
        v-model:value="projectId"
        :options="projects.map((p) => ({ value: p.id, label: p.name }))"
        size="small"
        style="width: 160px"
        @change="onProjectChange"
      />
      <span class="text-xs text-muted-foreground">环境</span>
      <a-select
        v-model:value="env"
        :options="[
          { value: 'prod', label: 'prod' },
          { value: 'canary', label: 'canary' },
          { value: 'test', label: 'test' },
        ]"
        size="small"
        style="width: 110px"
        @change="load"
      />
      <div class="flex-1"></div>
      <a-button size="small" @click="openSettings">
        <SafetyCertificateOutlined /> AgileConfig
      </a-button>
      <a-button :loading="reconciling" size="small" @click="reconcile">
        <SyncOutlined /> 对账
      </a-button>
      <a-button :loading="pushing" size="small" type="primary" @click="push">
        <CloudUploadOutlined /> 推送
      </a-button>
      <a-button size="small" type="primary" @click="openCreate">新增键值</a-button>
    </div>

    <a-table
      :data-source="items"
      :loading="loading"
      :pagination="false"
      row-key="id"
      size="small"
    >
      <a-table-column title="键" data-index="key" :width="240">
        <template #default="{ record }">
          <span class="font-mono text-xs">{{ record.key }}</span>
        </template>
      </a-table-column>
      <a-table-column title="值" :width="300">
        <template #default="{ record }">
          <span v-if="record.sensitive" class="font-mono text-xs text-muted-foreground">
            ******
          </span>
          <span v-else class="block truncate font-mono text-xs" :title="record.value">
            {{ record.value }}
          </span>
        </template>
      </a-table-column>
      <a-table-column title="敏感" :width="70">
        <template #default="{ record }">
          <a-tag v-if="record.sensitive" color="red">敏感</a-tag>
          <span v-else class="text-muted-foreground">—</span>
        </template>
      </a-table-column>
      <a-table-column title="备注" data-index="remark" :width="160" />
      <a-table-column title="更新时间" :width="150">
        <template #default="{ record }">
          {{ (record.updatedAt ?? '').slice(5, 16).replace('T', ' ') }}
        </template>
      </a-table-column>
      <a-table-column title="操作" :width="170">
        <template #default="{ record }">
          <a-button size="small" type="link" @click="openEdit(record)">编辑</a-button>
          <a-button v-if="record.sensitive" size="small" type="link" @click="reveal(record)">
            <EyeOutlined /> 明文
          </a-button>
          <a-popconfirm title="确认删除该键值？" @confirm="remove(record)">
            <a-button danger size="small" type="link">删除</a-button>
          </a-popconfirm>
        </template>
      </a-table-column>
      <template #emptyText>
        <a-empty
          :image-style="{ height: '48px' }"
          description="暂无键值——新增后可推送到 AgileConfig 供应用 SDK 热更"
        />
      </template>
    </a-table>

    <!-- 新增/编辑 -->
    <a-modal
      v-model:open="editorOpen"
      :title="editing ? `编辑：${editing.key}` : '新增键值'"
      @ok="save"
    >
      <a-form layout="vertical" class="pt-2">
        <a-form-item label="键" required>
          <a-input v-model:value="form.key" placeholder="如 db.host" class="font-mono" />
        </a-form-item>
        <a-form-item :label="form.sensitive ? '值（敏感，编辑留空则不修改）' : '值'" required>
          <a-textarea
            v-model:value="form.value"
            :auto-size="{ minRows: 1, maxRows: 6 }"
            :placeholder="form.sensitive && editing ? '留空 = 保持原值' : ''"
            class="font-mono"
          />
        </a-form-item>
        <a-form-item>
          <a-checkbox v-model:checked="form.sensitive">敏感值（列表脱敏、查看留审计）</a-checkbox>
        </a-form-item>
        <a-form-item label="备注">
          <a-input v-model:value="form.remark" placeholder="可选" />
        </a-form-item>
      </a-form>
    </a-modal>

    <!-- 对账结果 -->
    <a-modal v-model:open="diffOpen" title="对账结果（AgileConfig 漂移检测）" footer-only-close>
      <template v-if="diff">
        <a-alert
          v-if="diff.drifted.length + diff.extra.length + diff.missing.length === 0"
          message="无漂移：远端与平台一致"
          type="success"
          show-icon
        />
        <div v-else class="space-y-3 text-sm">
          <a-alert
            message="检测到漂移（平台为真相源：推送可覆盖改值项；控制台手加项不自动删除）"
            type="warning"
            show-icon
          />
          <div v-if="diff.missing.length">
            <div class="mb-1 font-medium">平台有、远端缺（{{ diff.missing.length }}）</div>
            <div class="font-mono text-xs text-muted-foreground">
              {{ diff.missing.join('、') }}
            </div>
          </div>
          <div v-if="diff.drifted.length">
            <div class="mb-1 font-medium">值不同（{{ diff.drifted.length }}）</div>
            <div class="font-mono text-xs text-muted-foreground">
              {{ diff.drifted.join('、') }}
            </div>
          </div>
          <div v-if="diff.extra.length">
            <div class="mb-1 font-medium">远端多出（控制台手加，{{ diff.extra.length }}）</div>
            <div class="font-mono text-xs text-muted-foreground">
              {{ diff.extra.join('、') }}
            </div>
          </div>
        </div>
      </template>
    </a-modal>

    <!-- AgileConfig provider 设置 -->
    <a-modal v-model:open="settingsOpen" title="AgileConfig 连接（provider）" @ok="saveSettings">
      <a-form layout="vertical" class="pt-2">
        <a-alert
          v-if="settings.configured"
          :message="`已连接：${settings.endpoint}`"
          type="success"
          show-icon
          class="mb-3"
        />
        <a-alert
          v-else
          message="未配置：K/V 仅平台存储与文件/API 消费形态，不影响配置文件视图"
          type="info"
          show-icon
          class="mb-3"
        />
        <a-form-item label="服务地址" required>
          <a-input v-model:value="settingsForm.endpoint" placeholder="http://agileconfig:5000" />
        </a-form-item>
        <a-form-item label="admin 用户名（默认 admin）">
          <a-input v-model:value="settingsForm.user" placeholder="admin" />
        </a-form-item>
        <a-form-item label="admin 密码（留空保留）">
          <a-input-password v-model:value="settingsForm.password" placeholder="••••••" />
        </a-form-item>
        <div class="text-xs text-muted-foreground">
          平台为真相源单向推送（自动创建同名应用）；应用映射 appId=项目名。
          敏感值脱敏与推送：敏感项照常推送（应用需要真值），平台侧列表/查看受审计。
        </div>
      </a-form>
    </a-modal>
  </div>
</template>
