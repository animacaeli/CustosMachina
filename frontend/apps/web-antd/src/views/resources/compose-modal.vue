<script lang="ts" setup>
import type { EnvProbe } from '#/api/resources/containers';

import { ref, watch } from 'vue';

import { message } from 'ant-design-vue';
import { parse } from 'yaml';

import {
  deployComposeApi,
  installGuideApi,
  probeEnvApi,
} from '#/api/resources/containers';
import YamlEditor from '#/components/yaml-editor.vue';

const props = defineProps<{ serverId: null | number }>();
const open = defineModel<boolean>('open', { default: false });

const TEMPLATE = `services:
  app:
    image: hello-world
    restart: unless-stopped
`;

const step = ref(0); // 0 环境检测 1 编辑 2 结果
const probing = ref(false);
const probe = ref<EnvProbe | null>(null);
const guide = ref('');
const yamlText = ref(TEMPLATE);
const projectName = ref('');
const deploying = ref(false);
const deployOutput = ref('');
const deployDir = ref('');
const yamlError = ref('');

async function runProbe() {
  if (!props.serverId) return;
  probing.value = true;
  try {
    probe.value = await probeEnvApi(props.serverId);
    if (!probe.value.ready) {
      const g = await installGuideApi(probe.value.distro || 'unknown');
      guide.value = g.guide;
    }
  } finally {
    probing.value = false;
  }
}

watch(open, (v) => {
  if (v) {
    step.value = 0;
    probe.value = null;
    guide.value = '';
    yamlError.value = '';
    deployOutput.value = '';
    deployDir.value = '';
    projectName.value = '';
    yamlText.value = TEMPLATE;
    runProbe();
  }
});

function validateYaml(): boolean {
  yamlError.value = '';
  try {
    const doc = parse(yamlText.value) as any;
    if (!doc || typeof doc !== 'object' || !doc.services) {
      yamlError.value = '缺少顶层 services 字段';
      return false;
    }
    // 项目名缺省取 YAML 顶层 name 字段
    if (!projectName.value && typeof doc.name === 'string' && doc.name) {
      projectName.value = doc.name.toLowerCase();
    }
    return true;
  } catch (e) {
    yamlError.value = `YAML 语法错误：${(e as Error).message}`;
    return false;
  }
}

function onImportFile(ev: Event) {
  const file = (ev.target as HTMLInputElement).files?.[0];
  if (!file) return;
  file.text().then((t) => {
    yamlText.value = t;
    message.success(`已导入：${file.name}`);
    validateYaml();
  });
}

async function deploy() {
  if (!props.serverId || !validateYaml()) return;
  if (!projectName.value.trim()) {
    message.warning('请填写项目名（用于目标机部署目录）');
    return;
  }
  deploying.value = true;
  deployOutput.value = '';
  try {
    const res = await deployComposeApi(
      props.serverId,
      yamlText.value,
      projectName.value.trim(),
    );
    deployOutput.value = res.output;
    deployDir.value = res.dir ?? '';
    step.value = 2;
    message.success('部署成功');
  } catch {
    // 错误详情在拦截器提示；停留在编辑步骤
  } finally {
    deploying.value = false;
  }
}
</script>

<template>
  <a-modal
    v-model:open="open"
    :footer="null"
    :width="780"
    title="部署 Compose 应用"
    destroy-on-close
  >
    <a-steps :current="step" class="pb-4" :items="[{ title: '环境检测' }, { title: '编辑部署' }, { title: '完成' }]" />

    <!-- 步骤 1：环境检测 -->
    <div v-if="step === 0">
      <a-spin :spinning="probing">
        <template v-if="probe">
          <a-alert
            v-if="probe.ready"
            message="环境就绪"
            :description="`Docker ${probe.dockerVersion} + Compose ${probe.composeVer}（${probe.distro || '未知发行版'}）`"
            type="success"
            show-icon
            class="mb-3"
          />
          <template v-else>
            <a-alert
              message="目标机 Docker 环境不完整"
              :description="`发行版：${probe.distro || '未识别'}；Docker：${probe.dockerVersion || `不可用 ${ probe.dockerErr}`}；Compose 插件：${probe.composeVer || '不可用'}`"
              type="warning"
              show-icon
              class="mb-3"
            />
            <div class="pb-2 font-medium">按以下步骤安装（完成后再点重新检测）：</div>
            <pre class="max-h-64 overflow-auto rounded bg-[#1e1e1e] p-3 text-xs leading-5 text-gray-200">{{ guide }}</pre>
          </template>
        </template>
        <div v-else class="py-6 text-center text-gray-400">探测中…</div>
      </a-spin>
      <div class="flex justify-end gap-2 pt-4">
        <a-button @click="runProbe">重新检测</a-button>
        <a-button type="primary" :disabled="!probe?.ready" @click="step = 1">
          下一步
        </a-button>
      </div>
    </div>

    <!-- 步骤 2：编辑 + 部署 -->
    <div v-else-if="step === 1">
      <div class="flex items-center gap-2 pb-2">
        <span class="shrink-0 text-sm">项目名</span>
        <a-input
          v-model:value="projectName"
          placeholder="如 my-app（留空取 YAML name 字段）"
          style="width: 260px"
          size="small"
        />
        <input type="file" accept=".yml,.yaml" @change="onImportFile" />
        <span v-if="yamlError" class="text-xs text-red-500">{{ yamlError }}</span>
      </div>
      <div class="pb-2 text-xs text-gray-400">
        部署到目标机：/opt/custos-machina/compose/&lt;项目名&gt;/compose.yaml（同名重新部署为覆盖更新）
      </div>
      <YamlEditor v-model="yamlText" height="380px" schema="compose" />
      <div class="flex justify-end gap-2 pt-3">
        <a-button @click="step = 0">上一步</a-button>
        <a-button @click="validateYaml">校验</a-button>
        <a-button
          type="primary"
          :loading="deploying"
          :disabled="!!yamlError"
          @click="deploy"
        >
          部署
        </a-button>
      </div>
    </div>

    <!-- 步骤 3：结果 -->
    <div v-else>
      <a-alert
        :message="`部署成功：${deployDir || '目标机'}`"
        type="success"
        show-icon
        class="mb-3"
      />
      <pre class="max-h-96 overflow-auto rounded bg-[#1e1e1e] p-3 text-xs leading-5 text-gray-200">{{ deployOutput }}</pre>
      <div class="flex justify-end pt-3">
        <a-button type="primary" @click="open = false">完成</a-button>
      </div>
    </div>
  </a-modal>
</template>
