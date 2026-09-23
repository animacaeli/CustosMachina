<script lang="ts" setup>
import type { ReleaseItem } from '#/api/release';

import { ref, watch } from 'vue';

import { message } from 'ant-design-vue';

import {
  createReleaseApi,
  getPassedTagsApi,
  getReleasesApi,
  rollbackReleaseApi,
} from '#/api/release';

defineOptions({ name: 'ReleaseDrawer' });

const props = defineProps<{
  env: 'canary' | 'prod' | 'test';
  open: boolean;
  projectId: number | undefined;
}>();

const emit = defineEmits<{ close: [] }>();

const list = ref<ReleaseItem[]>([]);
const total = ref(0);
const page = ref(1);
const size = ref(10);
const loading = ref(false);

const passedTags = ref<string[]>([]);
const selectedTag = ref<string | undefined>();
const releasing = ref(false);
const rollbackTarget = ref<null | number>(null);

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
  () => [props.open, props.projectId, page.value],
  async () => {
    if (!props.open || !props.projectId) return;
    await load();
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
    await load();
  } finally {
    releasing.value = false;
  }
}

async function doRollback(rel: ReleaseItem) {
  if (!props.projectId) return;
  rollbackTarget.value = rel.id;
  try {
    const nr = await rollbackReleaseApi(rel.id);
    if (nr.status === 'success') {
      message.success(`已回滚到 ${rel.tag}`);
    } else {
      message.error(`回滚失败：${(nr.output ?? '').slice(0, 200)}`);
    }
    await load();
  } finally {
    rollbackTarget.value = null;
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
      :columns="[
        { title: '标签', dataIndex: 'tag' },
        { title: '发布人', dataIndex: 'releaseBy', width: 100 },
        { title: '时间', key: 'time', width: 170 },
        { title: '状态', key: 'status', width: 90 },
        { title: '操作', key: 'action', width: 120 },
      ]"
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
        <template v-if="column.key === 'time'">
          {{ fmtTime(record.createdAt) }}
        </template>
        <template v-else-if="column.key === 'status'">
          <a-tag :color="record.status === 'success' ? 'green' : 'red'">
            {{ record.status === 'success' ? '成功' : '失败' }}
          </a-tag>
          <a-tag v-if="record.rollbackOf" color="purple">回滚</a-tag>
        </template>
        <template v-else-if="column.key === 'action'">
          <a-popconfirm
            :title="`回滚 = 重新发布 ${record.tag}，确认？`"
            @confirm="doRollback(record)"
          >
            <a-button
              :loading="rollbackTarget === record.id"
              size="small"
              type="link"
            >
              回滚
            </a-button>
          </a-popconfirm>
          <a-tooltip v-if="record.output" :title="record.output.slice(0, 500)">
            <a-button size="small" type="link">输出</a-button>
          </a-tooltip>
        </template>
      </template>
    </a-table>
  </a-drawer>
</template>
