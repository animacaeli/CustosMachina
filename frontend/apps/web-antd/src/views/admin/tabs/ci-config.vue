<script lang="ts" setup>
import type { Registry } from '#/api/ci';

import { onMounted, reactive, ref } from 'vue';

import { message } from 'ant-design-vue';

import {
  createRegistryApi,
  deleteRegistryApi,
  getCiGlobalApi,
  getRegistriesApi,
  saveCiGlobalApi,
  updateRegistryApi,
} from '#/api/ci';

defineOptions({ name: 'AdminCiConfig' });

// ---- gitea 全局配置 ----
const globalForm = reactive({
  giteaBaseUrl: '',
  giteaToken: '',
  webhookSecret: '',
});
const globalState = reactive({ hasToken: false, webhookSet: false, hint: '' });
const globalSaving = ref(false);

async function loadGlobal() {
  const g = await getCiGlobalApi();
  globalForm.giteaBaseUrl = g.giteaBaseUrl;
  globalForm.giteaToken = '';
  globalForm.webhookSecret = '';
  globalState.hasToken = g.hasGiteaToken;
  globalState.webhookSet = g.webhookSet;
  globalState.hint = g.webhookHint;
}

async function saveGlobal() {
  globalSaving.value = true;
  try {
    const g = await saveCiGlobalApi({
      giteaBaseUrl: globalForm.giteaBaseUrl,
      giteaToken: globalForm.giteaToken || undefined,
      webhookSecret: globalForm.webhookSecret || undefined,
    });
    globalState.hasToken = g.hasGiteaToken;
    globalState.webhookSet = g.webhookSet;
    globalState.hint = g.webhookHint;
    message.success('CI 全局配置已保存');
  } finally {
    globalSaving.value = false;
  }
}

// ---- 镜像仓库 ----
const registries = ref<Registry[]>([]);
const regLoading = ref(false);
const regOpen = ref(false);
const editingId = ref<null | number>(null);
const regForm = reactive({
  name: '',
  type: 'gitea',
  address: '',
  credential: '',
  remark: '',
});

const typeLabels: Record<string, string> = {
  aliyun: '阿里云 ACR',
  gitea: 'gitea 内置',
  harbor: '自建 Harbor',
  tencent: '腾讯云 TCR',
};

async function loadRegistries() {
  regLoading.value = true;
  try {
    registries.value = await getRegistriesApi();
  } finally {
    regLoading.value = false;
  }
}

function openCreate() {
  editingId.value = null;
  Object.assign(regForm, {
    name: '',
    type: 'gitea',
    address: '',
    credential: '',
    remark: '',
  });
  regOpen.value = true;
}

function openEdit(r: Registry) {
  editingId.value = r.id;
  Object.assign(regForm, {
    name: r.name,
    type: r.type,
    address: r.address,
    credential: '',
    remark: r.remark,
  });
  regOpen.value = true;
}

async function submitReg() {
  if (!regForm.name || !regForm.address) {
    message.warning('请填写名称与地址');
    return;
  }
  const data = { ...regForm, credential: regForm.credential || undefined };
  if (editingId.value) {
    await updateRegistryApi(editingId.value, data);
    message.success('已更新');
  } else {
    await createRegistryApi(data);
    message.success('已登记');
  }
  regOpen.value = false;
  await loadRegistries();
}

async function onDelete(r: Registry) {
  await deleteRegistryApi(r.id);
  message.success(`已删除 ${r.name}`);
  await loadRegistries();
}

onMounted(async () => {
  await loadGlobal();
  await loadRegistries();
});
</script>

<template>
  <div>
    <a-form layout="vertical" class="max-w-2xl">
      <a-divider orientation="left" plain>gitea 全局配置</a-divider>
      <a-form-item
        label="gitea 地址"
        extra="如 https://gitea.internal（不带末尾斜杠）"
      >
        <a-input
          v-model:value="globalForm.giteaBaseUrl"
          placeholder="https://gitea.internal"
        />
      </a-form-item>
      <a-form-item
        label="全局 token"
        extra="留空保留；项目级 token 在项目配置里覆盖"
      >
        <a-input-password
          v-model:value="globalForm.giteaToken"
          :placeholder="globalState.hasToken ? '已配置，留空保留' : '未配置'"
        />
      </a-form-item>
      <a-form-item
        label="Webhook 密钥"
        :extra="`${globalState.hint}；密钥与 gitea 仓库 Webhook 配置保持一致`"
      >
        <a-input-password
          v-model:value="globalForm.webhookSecret"
          :placeholder="
            globalState.webhookSet
              ? '已配置，留空保留'
              : '未配置（webhook 将被拒绝）'
          "
        />
      </a-form-item>
      <a-button :loading="globalSaving" type="primary" @click="saveGlobal">
        保存
      </a-button>

      <a-divider orientation="left" plain>镜像仓库</a-divider>
    </a-form>
    <div class="mb-4">
      <a-button type="primary" @click="openCreate">登记仓库</a-button>
    </div>
    <a-table
      :data-source="registries"
      :loading="regLoading"
      :pagination="false"
      row-key="id"
      size="middle"
    >
      <a-table-column :width="60" data-index="id" title="ID" />
      <a-table-column data-index="name" title="名称" />
      <a-table-column :width="130" title="类型">
        <template #default="{ record }">
          {{ typeLabels[record.type] ?? record.type }}
        </template>
      </a-table-column>
      <a-table-column data-index="address" title="地址" />
      <a-table-column :width="90" title="凭据">
        <template #default="{ record }">
          <a-tag :color="record.hasCredential ? 'green' : 'orange'">
            {{ record.hasCredential ? '已配置' : '未配置' }}
          </a-tag>
        </template>
      </a-table-column>
      <a-table-column :width="150" key="action" title="操作">
        <template #default="{ record }">
          <a-button size="small" type="link" @click="openEdit(record)">
            编辑
          </a-button>
          <a-popconfirm title="确认删除该仓库？" @confirm="onDelete(record)">
            <a-button danger size="small" type="link">删除</a-button>
          </a-popconfirm>
        </template>
      </a-table-column>
    </a-table>

    <a-modal
      v-model:open="regOpen"
      :title="editingId ? '编辑仓库' : '登记仓库'"
      @ok="submitReg"
    >
      <a-form layout="vertical" class="pt-2">
        <a-form-item label="名称" required>
          <a-input v-model:value="regForm.name" placeholder="aliyun-hz" />
        </a-form-item>
        <a-form-item label="类型" required>
          <a-select
            v-model:value="regForm.type"
            :options="[
              { label: 'gitea 内置', value: 'gitea' },
              { label: '阿里云 ACR', value: 'aliyun' },
              { label: '腾讯云 TCR', value: 'tencent' },
              { label: '自建 Harbor', value: 'harbor' },
            ]"
          />
        </a-form-item>
        <a-form-item label="地址" required>
          <a-input
            v-model:value="regForm.address"
            placeholder="registry.example.com"
          />
        </a-form-item>
        <a-form-item label="凭据（username:password）" extra="编辑时留空保留">
          <a-input-password v-model:value="regForm.credential" />
        </a-form-item>
        <a-form-item label="备注">
          <a-input v-model:value="regForm.remark" />
        </a-form-item>
      </a-form>
    </a-modal>
  </div>
</template>
