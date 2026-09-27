<script lang="ts" setup>
import type { CronJob, CronJobItem, CronScript } from '#/api/cron';

import { computed, onMounted, reactive, ref } from 'vue';

import { useUserStore } from '@vben/stores';

import { message } from 'ant-design-vue';

import {
  createJobApi,
  createScriptApi,
  deleteJobApi,
  getJobsApi,
  getScriptsApi,
  previewScheduleApi,
  triggerJobApi,
  updateJobApi,
} from '#/api/cron';
import { getServerListApi } from '#/api/resources/server';

import RunsDrawer from './runs-drawer.vue';

defineOptions({ name: 'CronJobs' });

const userStore = useUserStore();
// 管理/触发仅 admin（后端 casbin + handler 兜底，这里只控制按钮可见性）
const canWrite = computed(() => {
  const roles = userStore.userInfo?.roles ?? [];
  return roles.includes('superadmin') || roles.includes('admin');
});

const loading = ref(false);
const list = ref<CronJobItem[]>([]);
const scripts = ref<CronScript[]>([]);
const servers = ref<{ host: string; id: number; name: string }[]>([]);

async function load() {
  loading.value = true;
  try {
    const [jobsRes, scriptsRes, serversRes] = await Promise.all([
      getJobsApi(),
      getScriptsApi(),
      getServerListApi(),
    ]);
    list.value = jobsRes.items ?? [];
    scripts.value = scriptsRes.items ?? [];
    servers.value = (serversRes ?? []).map((s: any) => ({
      host: s.host,
      id: s.id,
      name: s.name,
    }));
  } finally {
    loading.value = false;
  }
}

onMounted(load);

const statusText: Record<string, string> = {
  failed: '失败',
  running: '运行中',
  skipped: '跳过',
  success: '成功',
  timeout: '超时',
  unknown: '结果未知',
};

// ---- 快速执行（一次性脚本：内联写脚本 → 自动建脚本+手动任务 → 触发 → 看实时日志） ----
const quickOpen = ref(false);
const quickRunning = ref(false);
const quick = reactive({
  carrier: 'run' as CronJob['carrier'],
  content: '',
  image: 'alpine:3',
  projectName: '',
  serverId: undefined as number | undefined,
  service: '',
  timeoutSecs: 300,
  type: 'shell' as 'python' | 'shell',
});

async function quickExecute() {
  if (!quick.content.trim() || !quick.serverId) {
    message.warning('请填写脚本内容与目标主机');
    return;
  }
  if (quick.carrier === 'run' && !quick.image) {
    message.warning('docker run 载体需填写镜像');
    return;
  }
  if (quick.carrier === 'compose-run' && (!quick.projectName || !quick.service)) {
    message.warning('compose 载体需填写项目名与服务名');
    return;
  }
  quickRunning.value = true;
  try {
    const ts = new Date().toISOString().slice(0, 16).replace('T', ' ');
    const script = await createScriptApi({
      content: quick.content,
      name: `快速执行 ${ts}`,
      remark: '快速执行入口创建',
      type: quick.type,
    });
    const job = await createJobApi({
      carrier: quick.carrier,
      enabled: false,
      image: quick.image,
      name: `快速执行 ${ts}`,
      projectName: quick.projectName,
      retry: 0,
      schedule: '',
      scriptId: script.id,
      serverId: quick.serverId,
      service: quick.service,
      timeoutSecs: quick.timeoutSecs,
    });
    const run = await triggerJobApi(job.id);
    message.success('已执行，实时日志查看中');
    quickOpen.value = false;
    quick.content = '';
    await load();
    runsJobId.value = job.id;
    runsJobName.value = job.name;
    runsNonce.value += 1;
    runsAutoOpenRun.value = run.id;
    runsOpen.value = true;
  } catch {
    // 业务错误由拦截器提示
  } finally {
    quickRunning.value = false;
  }
}

function fmtTime(v?: null | string) {
  if (!v) return '—';
  return new Date(v).toLocaleString();
}

// ---- 运行历史抽屉 ----
const runsOpen = ref(false);
const runsJobId = ref<number | undefined>();
const runsJobName = ref<string>();
const runsAutoOpenRun = ref<number>();

function showRuns(item?: CronJobItem) {
  runsJobId.value = item?.job.id;
  runsJobName.value = item?.job.name ?? '全部任务';
  runsOpen.value = true;
}

// ---- 表单 ----
const formOpen = ref(false);
const editingId = ref<null | number>(null);
const form = reactive({
  carrier: 'run' as CronJob['carrier'],
  command: '',
  enabled: true,
  image: '',
  /** 执行方式：schedule 周期执行 / manual 仅手动（一次性脚本） */
  mode: 'schedule' as 'manual' | 'schedule',
  name: '',
  network: '',
  projectName: '',
  retry: 0,
  schedule: '',
  scriptId: undefined as number | undefined,
  serverId: undefined as number | undefined,
  service: '',
  timeoutSecs: 600,
});

const selectedScript = computed(() =>
  scripts.value.find((s) => s.id === form.scriptId),
);

function openCreate() {
  editingId.value = null;
  Object.assign(form, {
    carrier: 'run',
    command: '',
    enabled: true,
    image: '',
    mode: 'schedule',
    name: '',
    network: '',
    retry: 0,
    projectName: '',
    schedule: '0 3 * * *',
    scriptId: undefined,
    serverId: undefined,
    service: '',
    timeoutSecs: 600,
  });
  formOpen.value = true;
}

// 表达式预览：点击 ⏱ 才请求（不做输入实时校验——合规性在保存时统一检查），
// 预览结果顺带暴露表达式是否合规
const previewTimes = ref<string[]>([]);
const previewError = ref('');
const previewLoading = ref(false);
async function loadPreview() {
  if (!form.schedule) {
    previewError.value = '请先填写表达式';
    previewTimes.value = [];
    return;
  }
  previewLoading.value = true;
  previewError.value = '';
  try {
    const res = await previewScheduleApi(form.schedule);
    previewTimes.value = (res.times ?? []).map((t) =>
      new Date(t).toLocaleString('zh-CN', { hour12: false }),
    );
  } catch (e: any) {
    // 优先取后端 message（HTTP 400 时 axios 的 e.message 只有
    // "Request failed with status code 400"，真正的语法错误在 response.data.message）
    previewError.value =
      e?.response?.data?.message ?? e?.message ?? '表达式不合法';
    previewTimes.value = [];
  } finally {
    previewLoading.value = false;
  }
}
function onPreviewOpenChange(open: boolean) {
  if (open) loadPreview();
}

function openEdit(item: CronJobItem) {
  const j = item.job;
  editingId.value = j.id;
  Object.assign(form, {
    carrier: j.carrier,
    command: j.command,
    enabled: j.enabled,
    image: j.image,
    mode: j.schedule ? 'schedule' : 'manual',
    name: j.name,
    network: j.network ?? '',
    retry: j.retry ?? 0,
    projectName: j.projectName,
    schedule: j.schedule || '0 3 * * *',
    scriptId: j.scriptId,
    serverId: j.serverId,
    service: j.service,
    timeoutSecs: j.timeoutSecs,
  });
  formOpen.value = true;
}

async function submitForm() {
  if (!form.name || !form.scriptId || !form.serverId) {
    message.warning('请填写完整（名称 / 脚本 / 主机）');
    return;
  }
  if (form.mode === 'schedule' && !form.schedule) {
    message.warning('周期执行需填写 crontab 表达式');
    return;
  }
  try {
    const data = { ...form, schedule: form.mode === 'manual' ? '' : form.schedule };
    if (editingId.value) {
      await updateJobApi(editingId.value, data);
      message.success('已更新');
    } else {
      await createJobApi(data);
      message.success('创建成功');
    }
    formOpen.value = false;
    await load();
  } catch {
    // 业务错误由拦截器提示，这里拦住避免 unhandled rejection
  }
}

async function onDelete(item: CronJobItem) {
  try {
    await deleteJobApi(item.job.id);
    message.success(`已删除 ${item.job.name}`);
    await load();
  } catch {
    // 运行中等业务错误由拦截器提示
  }
}

const triggering = ref(0);
const runsNonce = ref(0);
async function onTrigger(item: CronJobItem) {
  triggering.value = item.job.id;
  try {
    await triggerJobApi(item.job.id);
    message.success('已触发，稍后在运行历史查看结果');
    showRuns(item);
    runsNonce.value += 1; // 抽屉已开时强制刷新历史
  } catch {
    // Forbid 等业务错误由拦截器提示
  } finally {
    triggering.value = 0;
  }
}
</script>

<template>
  <div class="p-4">
    <a-card title="定时任务">
      <template #extra>
        <a-button class="mr-2" @click="quickOpen = true">快速执行</a-button>
        <a-button class="mr-2" @click="showRuns(undefined)">
          全部运行历史
        </a-button>
        <a-button v-if="canWrite" type="primary" @click="openCreate">
          新增任务
        </a-button>
      </template>
      <a-table
        :data-source="list"
        :loading="loading"
        :pagination="false"
        row-key="job.id"
      >
        <a-table-column title="名称" key="name">
          <template #default="{ record }">{{ record.job.name }}</template>
        </a-table-column>
        <a-table-column title="脚本" :width="140">
          <template #default="{ record }">
            {{ record.scriptName || '—' }}
          </template>
        </a-table-column>
        <a-table-column title="调度" :width="120">
          <template #default="{ record }">
            <code>{{ record.job.schedule }}</code>
          </template>
        </a-table-column>
        <a-table-column title="载体" :width="170">
          <template #default="{ record }">
            <a-tag v-if="record.job.carrier === 'run'" color="blue">
              docker run · {{ record.job.image || '未配镜像' }}
            </a-tag>
            <a-tag v-else color="cyan">
              compose · {{ record.job.projectName }}/{{ record.job.service }}
            </a-tag>
          </template>
        </a-table-column>
        <a-table-column title="下次执行" :width="160">
          <template #default="{ record }">
            <span v-if="!record.job.schedule">手动</span>
            <span v-else-if="!record.job.enabled">已停用</span>
            <span v-else>{{ fmtTime(record.job.nextRunAt) }}</span>
          </template>
        </a-table-column>
        <a-table-column title="上次结果" :width="90">
          <template #default="{ record }">
            <a-badge
              :status="
                (
                  {
                    failed: 'error',
                    running: 'processing',
                    success: 'success',
                  } as Record<string, string>
                )[record.job.lastStatus] ?? 'default'
              "
              :text="
                statusText[record.job.lastStatus] ||
                record.job.lastStatus ||
                '—'
              "
            />
          </template>
        </a-table-column>
        <a-table-column title="操作" :width="240">
          <template #default="{ record }">
            <a-button
              v-if="canWrite"
              size="small"
              type="link"
              :loading="triggering === record.job.id"
              @click="onTrigger(record)"
            >
              立即执行
            </a-button>
            <a-button size="small" type="link" @click="showRuns(record)">
              历史
            </a-button>
            <template v-if="canWrite">
              <a-button size="small" type="link" @click="openEdit(record)">
                编辑
              </a-button>
              <a-popconfirm
                :title="`确认删除 ${record.job.name}？`"
                @confirm="onDelete(record)"
              >
                <a-button size="small" type="link" danger>删除</a-button>
              </a-popconfirm>
            </template>
          </template>
        </a-table-column>
      </a-table>
    </a-card>

    <a-modal
      v-model:open="formOpen"
      :title="editingId ? `编辑任务：${form.name}` : '新增任务'"
      :width="640"
      @ok="submitForm"
    >
      <a-form layout="vertical" style="padding-top: 0.5rem">
        <a-form-item label="任务名称" required>
          <a-input v-model:value="form.name" placeholder="如：每日数据库清理" />
        </a-form-item>
        <a-form-item label="绑定脚本" required>
          <a-select
            v-model:value="form.scriptId"
            :options="
              scripts.map((s) => ({
                label: `${s.name}（${s.type}）`,
                value: s.id,
              }))
            "
            placeholder="选择脚本库中的脚本"
          />
        </a-form-item>
        <a-form-item label="执行方式" required>
          <a-radio-group v-model:value="form.mode">
            <a-radio value="schedule">周期执行（crontab）</a-radio>
            <a-radio value="manual">仅手动（一次性脚本）</a-radio>
          </a-radio-group>
        </a-form-item>
        <a-form-item
          v-if="form.mode === 'schedule'"
          label="调度表达式（标准 crontab：分 时 日 月 周）"
          required
        >
          <div class="flex gap-1">
            <a-input
              v-model:value="form.schedule"
              placeholder="如 0 3 * * *（每日 3 点）/ 30 8 * * 1-5（工作日 8:30）"
            />
            <a-popover
              trigger="click"
              placement="right"
              @open-change="onPreviewOpenChange"
            >
              <template #content>
                <div style="min-width: 220px">
                  <div v-if="previewLoading">计算中…</div>
                  <div v-else-if="previewError" class="text-xs text-red-500">
                    {{ previewError }}
                  </div>
                  <template v-else>
                    <div class="mb-1 text-xs text-gray-500">未来 5 次执行：</div>
                    <div v-for="t in previewTimes" :key="t" class="text-xs">
                      · {{ t }}
                    </div>
                  </template>
                </div>
              </template>
              <a-button title="查看未来 5 次执行时间">⏱</a-button>
            </a-popover>
          </div>
        </a-form-item>
        <a-form-item label="目标主机" required>
          <a-select
            v-model:value="form.serverId"
            :options="
              servers.map((s) => ({
                label: `${s.name}（${s.host}）`,
                value: s.id,
              }))
            "
            placeholder="选择执行主机"
          />
        </a-form-item>
        <a-form-item label="执行载体" required>
          <a-radio-group v-model:value="form.carrier">
            <a-radio value="run">docker run（独立镜像）</a-radio>
            <a-radio value="compose-run">
              docker compose run（项目服务）
            </a-radio>
          </a-radio-group>
        </a-form-item>
        <template v-if="form.carrier === 'run'">
          <a-form-item
            v-if="selectedScript && selectedScript.type !== 'compose-run'"
            :label="`镜像（须含 ${selectedScript.type === 'python' ? 'python3' : 'sh'}）`"
            required
          >
            <a-input
              v-model:value="form.image"
              placeholder="如 alpine:3.20 / python:3.12"
            />
          </a-form-item>
        </template>
        <template v-else>
          <a-form-item label="项目名（部署目录名）" required>
            <a-input v-model:value="form.projectName" placeholder="如 demo" />
          </a-form-item>
          <a-form-item label="compose 服务名" required>
            <a-input v-model:value="form.service" placeholder="如 migrate" />
          </a-form-item>
        </template>
        <a-form-item
          v-if="form.carrier === 'run'"
          label="docker 网络（可选，连业务网络查数据用）"
        >
          <a-input v-model:value="form.network" placeholder="留空 = 默认网络" />
        </a-form-item>
        <a-form-item label="附加参数（可选，追加到命令后）">
          <a-input v-model:value="form.command" placeholder="如 --dry-run" />
        </a-form-item>
        <a-form-item label="失败重试次数（0-3，间隔 5 分钟）">
          <a-input-number v-model:value="form.retry" :min="0" :max="3" />
        </a-form-item>
        <a-form-item label="超时（秒，超时强杀容器）">
          <a-input-number
            v-model:value="form.timeoutSecs"
            :min="10"
            :max="86400"
          />
        </a-form-item>
        <a-form-item>
          <a-switch v-model:checked="form.enabled" /> 启用调度
        </a-form-item>
      </a-form>
    </a-modal>

    <RunsDrawer
      v-model:open="runsOpen"
      :auto-open-run="runsAutoOpenRun"
      :job-id="runsJobId"
      :job-name="runsJobName"
      :nonce="runsNonce"
    />

    <a-modal
      v-model:open="quickOpen"
      title="快速执行（一次性脚本）"
      :width="640"
      :confirm-loading="quickRunning"
      ok-text="执行"
      @ok="quickExecute"
    >
      <a-form layout="vertical" style="padding-top: 0.5rem">
        <a-form-item label="目标主机" required>
          <a-select
            v-model:value="quick.serverId"
            :options="
              servers.map((s) => ({
                label: `${s.name}（${s.host}）`,
                value: s.id,
              }))
            "
            placeholder="选择执行主机"
          />
        </a-form-item>
        <a-form-item label="执行载体" required>
          <a-radio-group v-model:value="quick.carrier">
            <a-radio value="run">docker run（独立镜像）</a-radio>
            <a-radio value="compose-run">docker compose run（业务项目服务）</a-radio>
          </a-radio-group>
        </a-form-item>
        <a-form-item v-if="quick.carrier === 'run'" label="镜像" required>
          <a-input v-model:value="quick.image" placeholder="如 alpine:3 / python:3.12" />
        </a-form-item>
        <template v-else>
          <a-form-item label="项目名（蓝绿项目填基础名）" required>
            <a-input v-model:value="quick.projectName" />
          </a-form-item>
          <a-form-item label="compose 服务名" required>
            <a-input v-model:value="quick.service" />
          </a-form-item>
        </template>
        <a-form-item label="脚本内容" required>
          <a-textarea v-model:value="quick.content" :rows="10" />
        </a-form-item>
        <a-form-item label="超时（秒）">
          <a-input-number v-model:value="quick.timeoutSecs" :min="10" :max="86400" />
        </a-form-item>
      </a-form>
    </a-modal>
  </div>
</template>
