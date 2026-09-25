<script lang="ts" setup>
import type { Project } from '#/api/projects';
import type { SlotView } from '#/api/slots';

import { computed, reactive, ref, watch } from 'vue';

import { useUserStore } from '@vben/stores';

import { message } from 'ant-design-vue';

import { getProjectBranchesApi } from '#/api/ci';
import {
  getSlotsApi,
  occupySlotApi,
  releaseSlotApi,
  renewSlotApi,
} from '#/api/slots';

defineOptions({ name: 'SlotDrawer' });

const props = defineProps<{
  open: boolean;
  project: Project | undefined;
}>();

const emit = defineEmits<{ close: [] }>();

const userStore = useUserStore();
const isAdmin = computed(() => {
  const roles = userStore.userInfo?.roles ?? [];
  return roles.includes('superadmin') || roles.includes('admin');
});
const myUid = computed(() => userStore.userInfo?.userId ?? 0);

const list = ref<SlotView[]>([]);
const loading = ref(false);
const branches = ref<string[]>([]);

async function load() {
  if (!props.project) return;
  loading.value = true;
  try {
    list.value = await getSlotsApi(props.project.id);
  } finally {
    loading.value = false;
  }
}

watch(
  () => [props.open, props.project?.id],
  async () => {
    if (!props.open || !props.project) return;
    await load();
    try {
      branches.value = await getProjectBranchesApi(props.project.id);
    } catch {
      branches.value = [];
    }
  },
);

// ---- 占用 ----
const formOpen = ref(false);
const form = reactive({
  branch: undefined as string | undefined,
  durationUnit: 'days' as 'days' | 'hours' | 'weeks',
  durationValue: 1,
  slotName: undefined as string | undefined,
});

const freeSlots = computed(() =>
  list.value.filter((s) => !s.occupied).map((s) => s.slotName),
);

function openOccupy() {
  Object.assign(form, {
    branch: branches.value[0],
    durationUnit: 'days',
    durationValue: 1,
    slotName: freeSlots.value[0],
  });
  formOpen.value = true;
}

const occupying = ref(false);
async function doOccupy() {
  if (!props.project || !form.slotName || !form.branch) {
    message.warning('请选择槽位与分支');
    return;
  }
  occupying.value = true;
  try {
    await occupySlotApi(props.project.id, {
      branch: form.branch,
      durationUnit: form.durationUnit,
      durationValue: form.durationValue,
      slotName: form.slotName,
    });
    message.success(
      `已占用 ${form.slotName}，正在拉起 ${form.branch} 的测试环境`,
    );
    formOpen.value = false;
    await load();
  } catch {
    // 拦截器提示
  } finally {
    occupying.value = false;
  }
}

// ---- 释放 / 续期（仅占用人与管理员可见可操作） ----
const releasing = ref<null | string>(null);
async function doRelease(slotName: string) {
  if (!props.project) return;
  releasing.value = slotName;
  try {
    await releaseSlotApi(props.project.id, slotName);
    message.success(`已释放 ${slotName}（容器销毁，覆盖配置保留）`);
    await load();
  } finally {
    releasing.value = null;
  }
}

const renewing = ref<null | string>(null);
async function doRenew(slotName: string) {
  if (!props.project) return;
  renewing.value = slotName;
  try {
    await renewSlotApi(props.project.id, slotName, {
      durationUnit: 'days',
      durationValue: 1,
    });
    message.success(`已续期 ${slotName}（1 天）`);
    await load();
  } finally {
    renewing.value = null;
  }
}

function canOperate(v: SlotView) {
  return v.occupied && (isAdmin.value || v.slot.occupiedByUid === myUid.value);
}

function fmtTime(v: string) {
  return v ? new Date(v).toLocaleString('zh-CN', { hour12: false }) : '-';
}
</script>

<template>
  <a-drawer
    :open="open"
    title="测试环境槽位"
    :width="920"
    @close="emit('close')"
  >
    <a-alert show-icon type="info" style="margin-bottom: 0.75rem">
      <template #message>
        占用后平台立即拉起该分支的测试环境；之后每次 push
        自动重建。到期限不强制销毁， 宽限期后自动回收。释放 /
        续期仅占用人与管理员可操作。
      </template>
    </a-alert>

    <div class="mb-4">
      <a-button
        :disabled="freeSlots.length === 0"
        type="primary"
        @click="openOccupy"
      >
        占用槽位
      </a-button>
    </div>

    <a-table
      :columns="[
        { title: '槽位', dataIndex: 'slotName', width: 80 },
        { title: '分支', key: 'branch' },
        { title: '使用人', key: 'user', width: 100 },
        { title: '占用时间', key: 'at', width: 160 },
        { title: '预估释放', key: 'expire', width: 160 },
        { title: '操作', key: 'action', width: 140 },
      ]"
      :data-source="list"
      :loading="loading"
      :pagination="false"
      row-key="slotName"
      size="middle"
    >
      <template #bodyCell="{ column, record }">
        <template v-if="column.key === 'branch'">
          <template v-if="record.occupied">
            {{ record.slot.branch }}
            <a-tag v-if="record.slot.status === 'expired'" color="red">
              已过期
            </a-tag>
          </template>
          <span v-else class="text-muted-foreground">空闲</span>
        </template>
        <template v-else-if="column.key === 'user'">
          {{ record.occupied ? record.slot.occupiedBy : '-' }}
        </template>
        <template v-else-if="column.key === 'at'">
          {{ record.occupied ? fmtTime(record.slot.occupiedAt) : '-' }}
        </template>
        <template v-else-if="column.key === 'expire'">
          {{ record.occupied ? fmtTime(record.slot.expireAt) : '-' }}
        </template>
        <template v-else-if="column.key === 'action'">
          <template v-if="canOperate(record)">
            <a-popconfirm
              :title="`释放 ${record.slotName}？对应容器将立即销毁。`"
              @confirm="doRelease(record.slotName)"
            >
              <a-button
                :loading="releasing === record.slotName"
                danger
                size="small"
                type="link"
              >
                释放
              </a-button>
            </a-popconfirm>
            <a-button
              :loading="renewing === record.slotName"
              size="small"
              type="link"
              @click="doRenew(record.slotName)"
            >
              续期
            </a-button>
          </template>
          <span
            v-else-if="record.occupied"
            class="text-muted-foreground text-xs"
          >
            {{ record.slot.occupiedBy }} 占用中
          </span>
        </template>
      </template>
    </a-table>

    <a-modal v-model:open="formOpen" title="占用槽位" @ok="doOccupy">
      <a-form layout="vertical" style="padding-top: 0.5rem">
        <a-form-item label="槽位" required>
          <a-select
            v-model:value="form.slotName"
            :options="freeSlots.map((s) => ({ label: s, value: s }))"
            placeholder="空闲槽位"
          />
        </a-form-item>
        <a-form-item label="占用时长" required>
          <div class="flex gap-2">
            <a-input-number v-model:value="form.durationValue" :min="1" />
            <a-select
              v-model:value="form.durationUnit"
              :options="[
                { label: '小时', value: 'hours' },
                { label: '天', value: 'days' },
                { label: '周', value: 'weeks' },
              ]"
              style="width: 100px"
            />
          </div>
        </a-form-item>
        <a-form-item
          label="分支"
          required
          extra="push 到该分支会自动重建测试环境"
        >
          <a-select
            v-model:value="form.branch"
            :options="branches.map((b) => ({ label: b, value: b }))"
            :placeholder="
              branches.length === 0
                ? '拉取分支失败（检查 CI 配置）'
                : '选择分支'
            "
            show-search
          />
        </a-form-item>
      </a-form>
    </a-modal>
  </a-drawer>
</template>
