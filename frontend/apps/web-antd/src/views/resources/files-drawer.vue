<script lang="ts" setup>
import type { FileEntry } from '#/api/resources/files';

import { computed, ref, watch } from 'vue';

import { useUserStore } from '@vben/stores';

import { message } from 'ant-design-vue';

import {
  fileDownloadUrl,
  listFilesApi,
  mkdirApi,
  readFileApi,
  removeApi,
  renameApi,
  uploadFileApi,
  writeFileApi,
} from '#/api/resources/files';
import CodeEditor from '#/components/yaml-editor.vue';

defineOptions({ name: 'FilesDrawer' });

const props = defineProps<{ open: boolean; serverId: null | number }>();
const emit = defineEmits<{ 'update:open': [value: boolean] }>();

const userStore = useUserStore();
const canWrite = computed(() => {
  const roles = userStore.userInfo?.roles ?? [];
  return (
    roles.includes('superadmin') ||
    roles.includes('admin') ||
    roles.includes('ops')
  );
});

const cwd = ref('/');
const quickPaths = [
  { label: '部署目录', path: '/opt/custos-machina/compose' },
  { label: 'nginx 配置', path: '/root/docker-linux/nginx/conf/conf.d' },
  { label: 'cron 任务文件', path: '/opt/custos-machina/cron' },
  { label: '系统日志', path: '/var/log' },
  { label: 'root 主目录', path: '/root' },
];
const entries = ref<FileEntry[]>([]);
const loading = ref(false);

async function load() {
  if (!props.serverId) return;
  loading.value = true;
  try {
    const res = await listFilesApi(props.serverId, cwd.value);
    entries.value = res.entries ?? [];
  } finally {
    loading.value = false;
  }
}

watch(
  () => [props.open, props.serverId],
  ([open]) => {
    if (open) {
      cwd.value = '/';
      load();
    }
  },
);

const crumbs = computed(() => {
  const parts = cwd.value.split('/').filter(Boolean);
  return [
    { name: '/', path: '/' },
    ...parts.map((p, i) => ({
      name: p,
      path: `/${parts.slice(0, i + 1).join('/')}`,
    })),
  ];
});

function goTo(path: string) {
  cwd.value = path;
  load();
}

function enter(e: FileEntry) {
  if (!e.isDir) return;
  cwd.value = cwd.value === '/' ? `/${e.name}` : `${cwd.value}/${e.name}`;
  load();
}

function fmtSize(n: number) {
  if (n > 1024 * 1024) return `${(n / 1024 / 1024).toFixed(1)}MB`;
  if (n > 1024) return `${(n / 1024).toFixed(0)}KB`;
  return `${n}B`;
}
function fmtTime(unix: number) {
  return unix ? new Date(unix * 1000).toLocaleString() : '';
}

// ---- 在线编辑（<=1MB）----
const editOpen = ref(false);
const editPath = ref('');
const editContent = ref('');
// 按扩展名选编辑器语言（toml/conf 用 ini 近似高亮）
const editLanguage = computed(() => {
  const n = (editPath.value || '').toLowerCase();
  if (n.endsWith('.json')) return 'json';
  if (n.endsWith('.yml') || n.endsWith('.yaml')) return 'yaml';
  if (n.endsWith('.sh') || n.endsWith('.bash')) return 'shell';
  if (n.endsWith('.py')) return 'python';
  if (
    n.endsWith('.toml') ||
    n.endsWith('.ini') ||
    n.endsWith('.conf') ||
    n.endsWith('.properties')
  )
    return 'ini';
  if (n.endsWith('.xml') || n.endsWith('.html')) return 'xml';
  return 'plaintext';
});
const editSaving = ref(false);

async function openEdit(e: FileEntry) {
  if (!props.serverId || e.isDir || e.size > 1024 * 1024) return;
  const p = cwd.value === '/' ? `/${e.name}` : `${cwd.value}/${e.name}`;
  try {
    const res = await readFileApi(props.serverId, p);
    editPath.value = p;
    editContent.value = res.content ?? '';
    editOpen.value = true;
  } catch {
    // 二进制文件等读取错误由拦截器提示
  }
}

async function saveEdit() {
  if (!props.serverId) return;
  editSaving.value = true;
  try {
    await writeFileApi(props.serverId, editPath.value, editContent.value);
    message.success('已保存（原文件备份为 .bak）');
    editOpen.value = false;
    load();
  } finally {
    editSaving.value = false;
  }
}

// ---- 上传 ----
const uploadInput = ref<HTMLInputElement | null>(null);
const uploading = ref(false);
async function onUpload(ev: Event) {
  const input = ev.target as HTMLInputElement;
  const file = input.files?.[0];
  if (!file || !props.serverId) return;
  if (file.size > 100 * 1024 * 1024) {
    message.warning('文件超过 100MB，请改用 scp/直传');
    return;
  }
  uploading.value = true;
  try {
    await uploadFileApi(props.serverId, cwd.value, file);
    message.success(`已上传 ${file.name}`);
    load();
  } finally {
    uploading.value = false;
    input.value = '';
  }
}

// ---- 新建目录 / 重命名 / 删除 ----
const mkdirOpen = ref(false);
const mkdirName = ref('');
async function doMkdir() {
  if (!props.serverId || !mkdirName.value) return;
  const p =
    cwd.value === '/'
      ? `/${mkdirName.value}`
      : `${cwd.value}/${mkdirName.value}`;
  try {
    await mkdirApi(props.serverId, p);
    message.success('已创建');
    mkdirOpen.value = false;
    mkdirName.value = '';
    load();
  } catch {
    // 业务错误由拦截器提示
  }
}

const renameTarget = ref<FileEntry | null>(null);
const renameOpen = ref(false);
const renameTo = ref('');
function openRename(e: FileEntry) {
  renameTarget.value = e;
  renameTo.value = e.name;
  renameOpen.value = true;
}
async function doRename() {
  if (!props.serverId || !renameTarget.value) return;
  const from =
    cwd.value === '/'
      ? `/${renameTarget.value.name}`
      : `${cwd.value}/${renameTarget.value.name}`;
  const to =
    cwd.value === '/' ? `/${renameTo.value}` : `${cwd.value}/${renameTo.value}`;
  try {
    await renameApi(props.serverId, from, to);
    message.success('已重命名');
    renameOpen.value = false;
    load();
  } catch {
    // 业务错误由拦截器提示
  }
}

const downloading = ref('');
async function onDownload(e: FileEntry) {
  if (!props.serverId) return;
  const p = cwd.value === '/' ? `/${e.name}` : `${cwd.value}/${e.name}`;
  downloading.value = e.name;
  try {
    const url = await fileDownloadUrl(props.serverId, p);
    window.open(url, '_blank');
  } catch {
    // ticket 签发失败由拦截器提示
  } finally {
    downloading.value = '';
  }
}

async function onRemove(e: FileEntry) {
  if (!props.serverId) return;
  const p = cwd.value === '/' ? `/${e.name}` : `${cwd.value}/${e.name}`;
  try {
    await removeApi(props.serverId, p, e.isDir);
    message.success(`已删除 ${e.name}`);
    load();
  } catch {
    // 非空目录等业务错误由拦截器提示
  }
}
</script>

<template>
  <a-drawer
    :open="open"
    :title="`文件管理（服务器 #${serverId}）`"
    :width="760"
    @close="emit('update:open', false)"
  >
    <div class="mb-3 flex flex-wrap items-center gap-1">
      <a-breadcrumb>
        <a-breadcrumb-item v-for="c in crumbs" :key="c.path">
          <a @click="goTo(c.path)">{{ c.name }}</a>
        </a-breadcrumb-item>
      </a-breadcrumb>
      <span class="flex-1"></span>
      <a-select
        :value="undefined"
        :options="quickPaths.map((p) => ({ label: p.label, value: p.path }))"
        placeholder="常用路径"
        size="small"
        style="width: 190px"
        @change="
          (v: string) => {
            cwd = v;
            load();
          }
        "
      />
      <template v-if="canWrite">
        <input ref="uploadInput" type="file" hidden @change="onUpload" />
        <a-button
          size="small"
          :loading="uploading"
          @click="uploadInput?.click()"
        >
          上传
        </a-button>
        <a-button size="small" @click="mkdirOpen = true">新建目录</a-button>
      </template>
      <a-button size="small" :loading="loading" @click="load">刷新</a-button>
    </div>
    <a-table
      :data-source="entries"
      :loading="loading"
      :pagination="false"
      :show-header="false"
      row-key="name"
      size="small"
    >
      <a-table-column data-index="name">
        <template #default="{ record }">
          <a @click="enter(record)">
            {{ record.isDir ? '📁' : '📄' }} {{ record.name }}
          </a>
        </template>
      </a-table-column>
      <a-table-column :width="90">
        <template #default="{ record }">
          {{ record.isDir ? '—' : fmtSize(record.size) }}
        </template>
      </a-table-column>
      <a-table-column :width="160">
        <template #default="{ record }">{{ fmtTime(record.mtime) }}</template>
      </a-table-column>
      <a-table-column :width="190">
        <template #default="{ record }">
          <template v-if="!record.isDir">
            <a-button
              size="small"
              type="link"
              :loading="downloading === record.name"
              @click="onDownload(record)"
            >
              下载
            </a-button>
            <a-button
              v-if="canWrite && record.size <= 1024 * 1024"
              size="small"
              type="link"
              @click="openEdit(record)"
            >
              编辑
            </a-button>
          </template>
          <template v-if="canWrite">
            <a-button size="small" type="link" @click="openRename(record)">
              重命名
            </a-button>
            <a-popconfirm
              :title="
                record.isDir
                  ? `删除空目录 ${record.name}？`
                  : `确认删除 ${record.name}？`
              "
              @confirm="onRemove(record)"
            >
              <a-button size="small" type="link" danger>删除</a-button>
            </a-popconfirm>
          </template>
        </template>
      </a-table-column>
    </a-table>

    <a-modal
      v-model:open="editOpen"
      :title="`编辑：${editPath}`"
      :width="760"
      :confirm-loading="editSaving"
      ok-text="保存"
      @ok="saveEdit"
    >
      <CodeEditor
        v-model="editContent"
        :language="editLanguage"
        height="480px"
      />
    </a-modal>

    <a-modal v-model:open="mkdirOpen" title="新建目录" @ok="doMkdir">
      <a-input v-model:value="mkdirName" placeholder="目录名" />
    </a-modal>

    <a-modal
      v-model:open="renameOpen"
      :title="`重命名：${renameTarget?.name}`"
      @ok="doRename"
    >
      <a-input v-model:value="renameTo" />
    </a-modal>
  </a-drawer>
</template>
