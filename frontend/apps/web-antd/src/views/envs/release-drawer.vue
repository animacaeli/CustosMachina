<script lang="ts" setup>
import type { ReleaseItem } from '#/api/release';

import { computed, ref, watch } from 'vue';

import { message } from 'ant-design-vue';

import {
  createReleaseApi,
  getPassedTagsApi,
  getReleasesApi,
} from '#/api/release';

defineOptions({ name: 'ReleaseDrawer' });

const props = defineProps<{
  env: 'canary' | 'prod' | 'test';
  open: boolean;
  projectId: number | undefined;
}>();

const emit = defineEmits<{ close: [] }>();

const list = ref<ReleaseItem[]>([]);

// test 环境的 tag 形如 "分支@dev1"——拆成 标签/槽位 两列
function splitTag(tag: string): { branch: string; slot: string } {
  const at = tag.lastIndexOf('@');
  if (at === -1) return { branch: tag, slot: '-' };
  return { branch: tag.slice(0, at), slot: tag.slice(at + 1) };
}

const columns = computed(() => {
  const base = [
    { title: '发布人', dataIndex: 'releaseBy', width: 100 },
    { title: '时间', key: 'time', width: 170 },
    { title: '耗时', key: 'duration', width: 80 },
    { title: '状态', key: 'status', width: 90 },
    { title: '操作', key: 'action', width: 120 },
  ];
  if (props.env === 'test') {
    return [
      { title: '标签', key: 'tagcol' },
      { title: '槽位', key: 'slot', width: 80 },
      ...base,
    ];
  }
  return [{ title: '标签', dataIndex: 'tag' }, ...base];
});
const total = ref(0);
const page = ref(1);
const size = ref(10);
const loading = ref(false);

const passedTags = ref<string[]>([]);
const selectedTag = ref<string | undefined>();
const releasing = ref(false);
const deployingTag = ref<null | string>(null); // 行级 loading：只转圈当前部署的标签

function fmtDuration(secs: number) {
  if (secs > 0) {
    return secs >= 60 ? `${Math.floor(secs / 60)}m${secs % 60}s` : `${secs}s`;
  }
  return '<1s'; // 镜像缓存命中时 up -d 亚秒完成
}

// ---- 部署日志（CD 输出）----
const logOpen = ref(false);
const logText = ref('');
const logTitle = ref('');
function openLog(record: ReleaseItem) {
  logTitle.value = `${record.tag} 部署日志`;
  logText.value = record.output || '（无输出）';
  logOpen.value = true;
}

// 行内"部署"：对任意历史标签重新执行部署（含当前标签）——回滚即部署旧版本
async function doDeploy(rel: ReleaseItem) {
  if (!props.projectId) return;
  deployingTag.value = rel.tag;
  releasing.value = true;
  try {
    const nr = await createReleaseApi({
      envType: props.env,
      projectId: props.projectId,
      tag: rel.tag,
    });
    if (nr.status === 'success') {
      message.success(`已部署 ${rel.tag}`);
    } else {
      message.error(`部署失败：${(nr.output ?? '').slice(0, 200)}`);
    }
    page.value = 1;
    await load().catch((error) => console.warn('[load]', error));
  } finally {
    releasing.value = false;
    deployingTag.value = null;
  }
}

const envTitles: Record<string, string> = {
  canary: '灰度环境发布',
  prod: '正式环境发布',
  test: '测试环境发布',
};

async function load() {
  if (!props.projectId) return;
  loading.value = true;
  try {
    const res = await getReleasesApi({
      env: props.env,
      page: page.value,
      projectId: props.projectId,
      size: size.value,
    });
    list.value = res.items ?? [];
    total.value = res.total ?? 0;
  } finally {
    loading.value = false;
  }
}

watch(
  () => props.projectId,
  () => {
    page.value = 1; // 换项目回到第一页，避免停在新项目不存在的页码
  },
);

watch(
  () => [props.open, props.projectId, page.value],
  async () => {
    if (!props.open || !props.projectId) return;
    await load().catch((error) => console.warn('[load]', error));
    try {
      passedTags.value = await getPassedTagsApi(props.projectId, props.env);
    } catch {
      passedTags.value = [];
    }
  },
);

async function doRelease() {
  if (!props.projectId || !selectedTag.value) {
    message.warning('请选择要发布的标签');
    return;
  }
  releasing.value = true;
  try {
    const rel = await createReleaseApi({
      envType: props.env,
      projectId: props.projectId,
      tag: selectedTag.value,
    });
    if (rel.status === 'success') {
      message.success(`已发布 ${selectedTag.value}`);
    } else {
      message.error(`发布失败：${(rel.output ?? '').slice(0, 200)}`);
    }
    selectedTag.value = undefined;
    page.value = 1;
    await load().catch((error) => console.warn('[load]', error));
  } finally {
    releasing.value = false;
  }
}

function fmtTime(v: string) {
  return v ? new Date(v).toLocaleString('zh-CN', { hour12: false }) : '-';
}
</script>

<template>
  <a-drawer
    :open="open"
    :title="envTitles[env] ?? '发布'"
    :width="860"
    @close="emit('close')"
  >
    <div class="mb-4 flex gap-2">
      <a-select
        v-model:value="selectedTag"
        :options="passedTags.map((t) => ({ label: t, value: t }))"
        :placeholder="
          passedTags.length === 0
            ? '暂无已通过 CI 的标签'
            : '选择已通过 CI 的标签'
        "
        show-search
        style="width: 320px"
      />
      <a-button
        :loading="releasing"
        :disabled="!selectedTag"
        danger
        type="primary"
        @click="doRelease"
      >
        发布
      </a-button>
    </div>
    <a-table
      :columns="columns"
      :data-source="list"
      :loading="loading"
      :pagination="{
        current: page,
        pageSize: size,
        total,
        showSizeChanger: false,
        onChange: (p: number) => (page = p),
      }"
      row-key="id"
      size="middle"
    >
      <template #bodyCell="{ column, record }">
        <template v-if="column.key === 'tagcol'">
          {{ splitTag(record.tag).branch }}
        </template>
        <template v-else-if="column.key === 'slot'">
          {{ splitTag(record.tag).slot }}
        </template>
        <template v-else-if="column.key === 'time'">
          {{ fmtTime(record.createdAt) }}
        </template>
        <template v-else-if="column.key === 'duration'">
          {{ fmtDuration(record.durationSecs) }}
        </template>
        <template v-else-if="column.key === 'status'">
          <a-tag :color="record.status === 'success' ? 'green' : 'red'">
            {{ record.status === 'success' ? '成功' : '失败' }}
          </a-tag>
        </template>
        <template v-else-if="column.key === 'action'">
          <a-popconfirm
            :title="`部署 ${record.tag} 到${envTitles[props.env] ?? ''}，确认？`"
            @confirm="doDeploy(record)"
          >
            <a-button
              :loading="deployingTag === record.tag"
              size="small"
              type="link"
            >
              部署
            </a-button>
          </a-popconfirm>
          <a-button size="small" type="link" @click="openLog(record)">
            日志
          </a-button>
        </template>
      </template>
    </a-table>

    <a-modal v-model:open="logOpen" :title="logTitle" :width="820" footer="">
      <pre
        class="max-h-[65vh] overflow-auto rounded p-3 text-xs leading-5"
        style="color: #c9d1d9; background: #0b0e14"
        >{{ logText }}</pre>
    </a-modal>
  </a-drawer>
</template>
