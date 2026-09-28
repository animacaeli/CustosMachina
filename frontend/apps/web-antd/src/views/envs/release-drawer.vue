<script lang="ts" setup>
import type { ReleaseItem } from '#/api/release';

import { computed, onBeforeUnmount, ref, watch } from 'vue';

import { LoadingOutlined } from '@ant-design/icons-vue';
import { message } from 'ant-design-vue';

import {
  createReleaseApi,
  getActiveColorApi,
  getPassedTagsApi,
  getReleaseApi,
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

// 蓝绿：正式环境当前活跃色（空 = 尚未启用蓝绿/首次发布）
const activeColor = ref('');
const drainSecs = ref(30); // 蓝绿 drain 窗口（秒）

// 发布进行中的记录：行内部署按钮转圈、发布按钮互斥；列表 3s 轮询至终态
const anyRunning = computed(() =>
  list.value.some((r) => r.status === 'running'),
);
let listPoll: ReturnType<typeof setInterval> | undefined;
watch(anyRunning, (running) => {
  if (running && !listPoll) {
    listPoll = setInterval(() => {
      load().catch((error) => console.warn('[load]', error));
    }, 3000);
  } else if (!running) {
    if (listPoll) clearInterval(listPoll);
    listPoll = undefined;
  }
});
onBeforeUnmount(() => {
  if (listPoll) clearInterval(listPoll);
  stopReleasePoll();
});
async function loadActiveColor() {
  if (props.env !== 'prod' || !props.projectId) {
    activeColor.value = '';
    return;
  }
  try {
    const res = await getActiveColorApi(props.projectId);
    activeColor.value = res.color ?? '';
  } catch {
    activeColor.value = '';
  }
}

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
// 正式环境发布历史加"颜色"列（蓝绿落点）
const columnsWithColor = computed(() =>
  props.env === 'prod'
    ? [
        { title: '标签', dataIndex: 'tag' },
        { title: '颜色', key: 'color', width: 80 },
        ...columns.value.slice(1),
      ]
    : columns.value,
);
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
// 发布进行中的记录：日志弹窗每 3s 轮询单条（阶段日志增量刷库）
let releasePollTimer: ReturnType<typeof setInterval> | undefined;
function stopReleasePoll() {
  if (releasePollTimer) {
    clearInterval(releasePollTimer);
    releasePollTimer = undefined;
  }
}

function openLog(record: ReleaseItem) {
  logTitle.value = `${record.tag} 部署日志`;
  logText.value = record.output || '（无输出）';
  logOpen.value = true;
  stopReleasePoll();
  if (record.status === 'running') {
    releasePollTimer = setInterval(async () => {
      try {
        const r = await getReleaseApi(record.id);
        logText.value = r.output || '（无输出）';
        const stLabel =
          r.status === 'running' ? '进行中' : r.status === 'success' ? '成功' : '失败';
        logTitle.value = `${r.tag} 部署日志（${stLabel}）`;
        if (r.status !== 'running') {
          stopReleasePoll();
          page.value = 1;
          await load().catch((error) => console.warn('[load]', error));
          await loadActiveColor();
        }
      } catch {
        stopReleasePoll();
      }
    }, 3000);
  }
}

onBeforeUnmount(stopReleasePoll);

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
    page.value = 1;
    await load().catch((error) => console.warn('[load]', error));
    if (nr.status === 'running') {
      message.info('发布进行中（蓝绿），日志中查看实时进度');
      openLog(nr);
    } else if (nr.status === 'success') {
      message.success(`已部署 ${nr.tag}`);
      await loadActiveColor();
    } else {
      message.error(`部署失败：${(nr.output ?? '').slice(0, 200)}`);
    }
  } catch {
    // 部署错误由拦截器提示
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
    await Promise.all([
      load().catch((error) => console.warn('[load]', error)),
      loadActiveColor(),
    ]);
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
      drainSecs: drainSecs.value || undefined,
      envType: props.env,
      projectId: props.projectId,
      tag: selectedTag.value,
    });
    selectedTag.value = undefined;
    page.value = 1;
    await load().catch((error) => console.warn('[load]', error));
    if (rel.status === 'running') {
      message.info('发布进行中（蓝绿），日志中查看实时进度');
      openLog(rel);
    } else if (rel.status === 'success') {
      message.success(`已发布 ${rel.tag}`);
      await loadActiveColor();
    } else {
      message.error(`发布失败：${(rel.output ?? '').slice(0, 200)}`);
    }
  } catch {
    // 发布错误由拦截器提示
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
    <div class="mb-4 flex items-center gap-2">
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
        :disabled="!selectedTag || anyRunning"
        :loading="releasing || anyRunning"
        danger
        type="primary"
        @click="doRelease"
      >
        {{ anyRunning ? '发布中…' : '发布' }}
      </a-button>
      <a-tooltip
        v-if="env === 'prod'"
        :title="
          activeColor
            ? `当前承载流量的颜色域（本次发布将落到另一半颜色域，健康检查通过后才切换）`
            : '尚未启用蓝绿：首次正式发布将创建蓝色域并切换流量'
        "
      >
        <a-tag :color="activeColor === 'blue' ? 'blue' : 'green'">
          活跃色：{{ activeColor || '未启用' }}
        </a-tag>
      </a-tooltip>
      <a-tooltip
        v-if="env === 'prod'"
        title="蓝绿 drain 窗口（秒）：存量连接在旧颜色上跑完的等待时间"
      >
        <a-input-number
          v-model:value="drainSecs"
          :min="5"
          :max="600"
          size="small"
          style="width: 90px"
        />
      </a-tooltip>
    </div>
    <a-table
      :columns="columnsWithColor"
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
        <template v-else-if="column.key === 'color'">
          <a-tag
            v-if="record.color"
            :color="record.color === 'blue' ? 'blue' : 'green'"
          >
            {{ record.color === 'blue' ? '蓝' : '绿' }}
          </a-tag>
          <span v-else>—</span>
        </template>
        <template v-else-if="column.key === 'status'">
          <LoadingOutlined
            v-if="record.status === 'running'"
            class="mr-1"
            spin
          />
          <a-tag v-else :color="record.status === 'success' ? 'green' : 'red'">
            {{
              record.status === 'success'
                ? '成功'
                : record.status === 'running'
                  ? '进行中'
                  : '失败'
            }}
          </a-tag>
        </template>
        <template v-else-if="column.key === 'action'">
          <a-popconfirm
            :title="`部署 ${record.tag} 到${envTitles[props.env] ?? ''}，确认？`"
            @confirm="doDeploy(record)"
          >
            <a-button
              :disabled="record.status === 'running' || anyRunning"
              :loading="
                record.status === 'running' || deployingTag === record.tag
              "
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

    <a-modal
      v-model:open="logOpen"
      :title="logTitle"
      :width="820"
      footer=""
      @cancel="stopReleasePoll"
    >
      <pre
        class="max-h-[65vh] overflow-auto rounded p-3 text-xs leading-5"
        style="color: #c9d1d9; background: #0b0e14"
        >{{ logText }}</pre>
    </a-modal>
  </a-drawer>
</template>
