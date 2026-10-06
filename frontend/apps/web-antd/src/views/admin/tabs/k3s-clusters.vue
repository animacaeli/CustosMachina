<script lang="ts" setup>
import type { Cluster } from '#/api/k3s';

import { onMounted, reactive, ref } from 'vue';

import { message } from 'ant-design-vue';

import {
  createClusterApi,
  deleteClusterApi,
  listClustersApi,
  testClusterApi,
  updateClusterApi,
} from '#/api/k3s';

/** P8-M3.3 k3s 集群管理（admin）：kubeconfig AES 落库 + 连通测试 */
defineOptions({ name: 'K3sClusters' });

const loading = ref(false);
const list = ref<Cluster[]>([]);

async function load() {
  loading.value = true;
  try {
    list.value = await listClustersApi();
  } finally {
    loading.value = false;
  }
}

onMounted(load);

const editOpen = ref(false);
const editId = ref(0); // 0 = 新建
const form = reactive({
  name: '',
  kubeconfig: '',
  domain: '',
  remark: '',
});

function openCreate() {
  editId.value = 0;
  form.name = '';
  form.kubeconfig = '';
  form.domain = '';
  form.remark = '';
  editOpen.value = true;
}

function openEdit(c: Cluster) {
  editId.value = c.id;
  form.name = c.name;
  form.kubeconfig = ''; // 留空保留
  form.domain = c.domain ?? '';
  form.remark = c.remark ?? '';
  editOpen.value = true;
}

async function save() {
  if (!form.name.trim()) {
    message.warning('请填写集群名');
    return;
  }
  const payload = {
    name: form.name,
    kubeconfig: form.kubeconfig || undefined,
    domain: form.domain,
    remark: form.remark,
  };
  if (editId.value === 0) {
    await createClusterApi(payload);
    message.success('集群已创建');
  } else {
    await updateClusterApi(editId.value, payload);
    message.success('集群已更新');
  }
  editOpen.value = false;
  await load();
}

async function remove(c: Cluster) {
  await deleteClusterApi(c.id);
  message.success('集群已删除');
  await load();
}

const testing = ref(0);

async function test(c: Cluster) {
  testing.value = c.id;
  try {
    const out = await testClusterApi(c.id);
    message.success(`连通 OK：${out.version}`);
  } finally {
    testing.value = 0;
  }
}
</script>

<template>
  <div>
    <a-table
      :data-source="list"
      :loading="loading"
      :pagination="false"
      row-key="id"
    >
      <template #title>
        <div class="flex items-center gap-2">
          <span>k3s 集群</span>
          <a-button
            class="ml-auto"
            size="small"
            :loading="loading"
            @click="load"
          >
            刷 新
          </a-button>
          <a-button size="small" type="primary" @click="openCreate">
            新建集群
          </a-button>
        </div>
      </template>
      <a-table-column data-index="name" title="名称" :width="160" />
      <a-table-column data-index="server" title="API 地址" :width="220">
        <template #default="{ text }">
          <span v-if="text">{{ text }}</span>
          <span v-else class="text-gray-400">（kubeconfig 内）</span>
        </template>
      </a-table-column>
      <a-table-column data-index="domain" title="Ingress 域名后缀" :width="200">
        <template #default="{ text }">
          <span v-if="text">{{ text }}</span>
          <span v-else class="text-gray-400">—</span>
        </template>
      </a-table-column>
      <a-table-column data-index="remark" title="备注" />
      <a-table-column title="操作" :width="230">
        <template #default="{ record }">
          <a-button
            :loading="testing === record.id"
            size="small"
            type="link"
            @click="test(record)"
          >
            连通测试
          </a-button>
          <a-button size="small" type="link" @click="openEdit(record)">
            编辑
          </a-button>
          <a-popconfirm
            :title="`删除集群 ${record.name}？`"
            @confirm="remove(record)"
          >
            <a-button danger size="small" type="link"> 删 除 </a-button>
          </a-popconfirm>
        </template>
      </a-table-column>
      <template #emptyText>
        <a-empty description="暂无集群（新建后可在环境目标里选 k3s 载体）" />
      </template>
    </a-table>

    <a-modal
      v-model:open="editOpen"
      :title="editId === 0 ? '新建 k3s 集群' : `编辑集群：${form.name}`"
      @ok="save"
    >
      <a-form layout="vertical">
        <a-form-item label="集群名">
          <a-input v-model:value="form.name" placeholder="如 prod-k3s" />
        </a-form-item>
        <a-form-item
          :extra="
            editId === 0
              ? '/etc/rancher/k3s/k3s.yaml 内容（AES 加密落库）'
              : '留空保留既有 kubeconfig'
          "
          label="kubeconfig"
        >
          <a-textarea
            v-model:value="form.kubeconfig"
            :rows="6"
            class="font-mono text-xs"
            placeholder="apiVersion: v1 kind: Config ..."
          />
        </a-form-item>
        <a-form-item
          extra="k3s 项目 Ingress host = <项目名>-<环境>.<该后缀>（留空不建 Ingress）"
          label="Ingress 域名后缀"
        >
          <a-input v-model:value="form.domain" placeholder="如 k3s.internal" />
        </a-form-item>
        <a-form-item label="备注">
          <a-input v-model:value="form.remark" />
        </a-form-item>
      </a-form>
    </a-modal>
  </div>
</template>
