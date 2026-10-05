<script lang="ts" setup>
import type { AiSkill } from '#/api/chat/skills';

import { reactive, ref } from 'vue';

import { message } from 'ant-design-vue';

import { deleteSkillApi, listSkillsApi, saveSkillApi } from '#/api/chat/skills';

defineOptions({ name: 'AdminAiSkills' });

const skills = ref<AiSkill[]>([]);
const loading = ref(false);
const open = ref(false);
const editingId = ref<null | number>(null);
const saving = ref(false);

const form = reactive({
  description: '',
  enabled: true,
  name: '',
  prompt: '',
  roles: '',
  runbook: '',
  title: '',
});

async function load() {
  loading.value = true;
  try {
    skills.value = await listSkillsApi(true);
  } finally {
    loading.value = false;
  }
}

function openCreate() {
  editingId.value = null;
  Object.assign(form, {
    name: '',
    title: '',
    description: '',
    prompt: '',
    runbook: '',
    roles: '',
    enabled: true,
  });
  open.value = true;
}

function openEdit(sk: AiSkill) {
  editingId.value = sk.id;
  Object.assign(form, {
    name: sk.name,
    title: sk.title,
    description: sk.description,
    prompt: sk.prompt,
    runbook: sk.runbook,
    roles: sk.roles,
    enabled: sk.enabled,
  });
  open.value = true;
}

async function submit() {
  if (!form.name || !form.title || !form.prompt) {
    message.warning('请填写触发名、标题与提示词模板');
    return;
  }
  saving.value = true;
  try {
    await saveSkillApi(editingId.value, { ...form });
    message.success('已保存');
    open.value = false;
    await load();
  } finally {
    saving.value = false;
  }
}

async function onDelete(sk: AiSkill) {
  await deleteSkillApi(sk.id);
  message.success(`已删除 /${sk.name}`);
  await load();
}
</script>

<template>
  <div>
    <a-alert class="mb-4" type="info" show-icon>
      <template #message>AI 技能（P6-M3，声明式）</template>
      <template #description>
        <div>
          技能 = 提示词模板 + 运维 runbook（不含可执行代码）。用户在对话输入框打
          <code>/</code> 触发命令面板，按名调用。模板中
          <code v-pre>{{ q }}</code>
          会被替换为用户输入。
        </div>
      </template>
    </a-alert>

    <div class="mb-4">
      <a-button type="primary" @click="openCreate">新建技能</a-button>
    </div>

    <a-table
      :data-source="skills"
      :loading="loading"
      :pagination="false"
      row-key="id"
      size="middle"
    >
      <a-table-column :width="160" title="触发名">
        <template #default="{ record }">
          <code class="text-primary">/{{ record.name }}</code>
        </template>
      </a-table-column>
      <a-table-column data-index="title" title="标题" />
      <a-table-column data-index="description" title="说明" />
      <a-table-column :width="120" title="角色范围">
        <template #default="{ record }">
          {{ record.roles || '全部角色' }}
        </template>
      </a-table-column>
      <a-table-column :width="80" title="状态">
        <template #default="{ record }">
          <a-badge
            :status="record.enabled ? 'success' : 'default'"
            :text="record.enabled ? '启用' : '停用'"
          />
        </template>
      </a-table-column>
      <a-table-column :width="140" key="action" title="操作">
        <template #default="{ record }">
          <a-button size="small" type="link" @click="openEdit(record)">
            编辑
          </a-button>
          <a-popconfirm
            :title="`确认删除 /${record.name}？`"
            @confirm="onDelete(record)"
          >
            <a-button danger size="small" type="link">删除</a-button>
          </a-popconfirm>
        </template>
      </a-table-column>
    </a-table>

    <a-modal
      v-model:open="open"
      :title="editingId ? '编辑技能' : '新建技能'"
      :confirm-loading="saving"
      :width="720"
      @ok="submit"
    >
      <a-form layout="vertical" style="padding-top: 0.5rem">
        <div class="flex gap-3">
          <a-form-item
            class="flex-1"
            label="触发名"
            required
            extra="小写字母/数字/-/_；用户输入 /名 触发"
          >
            <a-input v-model:value="form.name" placeholder="troubleshoot" />
          </a-form-item>
          <a-form-item class="flex-1" label="标题" required>
            <a-input v-model:value="form.title" placeholder="故障排查" />
          </a-form-item>
        </div>
        <a-form-item label="说明" extra="命令面板里的一行说明">
          <a-input
            v-model:value="form.description"
            placeholder="按 runbook 结构化排查"
          />
        </a-form-item>
        <a-form-item
          label="提示词模板"
          required
          extra="技能的执行要求；{{q}} 会被替换为用户输入"
        >
          <a-textarea
            v-model:value="form.prompt"
            :auto-size="{ minRows: 4, maxRows: 12 }"
            placeholder="用户报告了以下问题：{{q}}&#10;请按以下流程回答：1) ..."
          />
        </a-form-item>
        <a-form-item
          label="Runbook（可选）"
          extra="运维手册，随提示词注入，按需引用"
        >
          <a-textarea
            v-model:value="form.runbook"
            :auto-size="{ minRows: 3, maxRows: 10 }"
            placeholder="## 通用排查顺序&#10;1. ..."
          />
        </a-form-item>
        <a-form-item
          label="角色范围（可选）"
          extra="逗号分隔（如 admin,ops）；留空 = 全部角色可用"
        >
          <a-input
            v-model:value="form.roles"
            placeholder=""
            style="width: 240px"
          />
        </a-form-item>
        <a-form-item label="启用">
          <a-switch v-model:checked="form.enabled" />
        </a-form-item>
      </a-form>
    </a-modal>
  </div>
</template>
