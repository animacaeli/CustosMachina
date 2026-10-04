<script lang="ts" setup>
import type { Conversation, Mount } from '#/api/chat';

import { computed, nextTick, onMounted, reactive, ref } from 'vue';

import { message as antMessage } from 'ant-design-vue';
import DOMPurify from 'dompurify';
import { marked } from 'marked';

import {
  chatStreamApi,
  createConversationApi,
  deleteConversationApi,
  listConversationsApi,
  listMessagesApi,
  updateMountApi,
} from '#/api/chat';
import { getProjectsApi } from '#/api/projects';
import { getServerListApi } from '#/api/resources/server';

defineOptions({ name: 'AiChat' });

marked.setOptions({ breaks: true, gfm: true });

// ---- 会话列表 ----
const conversations = ref<Conversation[]>([]);
const currentId = ref<null | number>(null);
const current = computed(() =>
  conversations.value.find((c) => c.id === currentId.value),
);

async function loadConversations() {
  conversations.value = await listConversationsApi();
}

async function newConversation(mode: 'general' | 'platform') {
  const c = await createConversationApi({ mode });
  conversations.value.unshift(c);
  currentId.value = c.id;
  messages.value = [];
  if (mode === 'platform') {
    mountOpen.value = true;
  }
}

async function openConversation(id: number) {
  if (streaming.value) {
    antMessage.warning('当前会话回复进行中，请先停止');
    return;
  }
  currentId.value = id;
  messages.value = await listMessagesApi(id);
  await nextTick();
  scrollToBottom();
}

async function removeConversation(id: number) {
  await deleteConversationApi(id);
  conversations.value = conversations.value.filter((c) => c.id !== id);
  if (currentId.value === id) {
    currentId.value = null;
    messages.value = [];
  }
}

// ---- 消息与流式 ----
interface UiMessage {
  content: string;
  role: string;
  status?: string;
  streaming?: boolean;
}
const messages = ref<UiMessage[]>([]);
const input = ref('');
const streaming = ref(false);
let stopFn: (() => void) | null = null;
const scrollBody = ref<HTMLElement>();

function scrollToBottom() {
  const el = scrollBody.value;
  if (el) {
    el.scrollTop = el.scrollHeight;
  }
}

async function send() {
  const content = input.value.trim();
  if (!content || streaming.value || !currentId.value) return;
  input.value = '';
  messages.value.push({ content, role: 'user' }, { content: '', role: 'assistant', streaming: true });
  streaming.value = true;
  await nextTick();
  scrollToBottom();
  const assistant = messages.value.at(-1);
  stopFn = await chatStreamApi(currentId.value, content, {
    onDelta: (text) => {
      assistant!.content += text;
      scrollToBottom();
    },
    onDone: (status) => {
      assistant!.streaming = false;
      assistant!.status = status;
      finish();
      if (status === 'aborted') {
        assistant!.content += '\n\n（已停止生成）';
      }
    },
    onError: (msg) => {
      // 无产出错误替换占位气泡；有部分内容则标注
      if (!assistant!.content) {
        messages.value = messages.value.filter((m) => m !== assistant);
      } else {
        assistant!.streaming = false;
        assistant!.status = 'error';
      }
      antMessage.error(msg);
      finish();
    },
  });
}

function finish() {
  streaming.value = false;
  stopFn = null;
  // 标题由后端首条消息生成，列表刷新拿新标题
  loadConversations();
}

function stop() {
  stopFn?.();
  // 后端落库 partial 后由 done(aborted) 或断连收尾；本地立即停止渲染
  const assistant = messages.value.at(-1);
  if (assistant?.streaming) {
    assistant.streaming = false;
    assistant.status = 'aborted';
  }
  finish();
}

// Markdown 渲染（消毒：AI 输出可能携带 pack 内不可信内容的 HTML）
function renderMd(md: string): string {
  return DOMPurify.sanitize(marked.parse(md) as string);
}

// ---- 挂载（platform 模式） ----
const mountOpen = ref(false);
const projects = ref<{ id: number; name: string }[]>([]);
const servers = ref<{ host: string; id: number; name: string }[]>([]);
const mountForm = reactive<Mount>({ hours: 24, projectIds: [], serverIds: [] });

async function loadMountOptions() {
  const [ps, ss] = await Promise.all([getProjectsApi(), getServerListApi()]);
  projects.value = ps.map((p: any) => ({ id: p.id, name: p.name }));
  servers.value = (ss ?? []).map((s: any) => ({
    host: s.host,
    id: s.id,
    name: s.name,
  }));
}

function openMount() {
  const m = current.value?.mount;
  Object.assign(mountForm, {
    hours: m?.hours || 24,
    projectIds: m?.projectIds ? [...m.projectIds] : [],
    serverIds: m?.serverIds ? [...m.serverIds] : [],
  });
  mountOpen.value = true;
}

async function saveMount() {
  if (!currentId.value) return;
  await updateMountApi(currentId.value, { ...mountForm });
  const c = conversations.value.find((x) => x.id === currentId.value);
  if (c) {
    c.mount = JSON.parse(JSON.stringify(mountForm));
  }
  mountOpen.value = false;
  antMessage.success('挂载已更新（下轮对话生效）');
}

onMounted(async () => {
  await Promise.all([loadConversations(), loadMountOptions()]);
});
</script>

<template>
  <div class="flex h-full gap-3 p-3">
    <!-- 会话侧栏 -->
    <div
      class="flex w-64 shrink-0 flex-col rounded-lg border border-border bg-card"
    >
      <div class="flex gap-2 p-3">
        <a-button block type="primary" @click="newConversation('general')">
          新对话
        </a-button>
        <a-button block @click="newConversation('platform')">
          平台上下文
        </a-button>
      </div>
      <div class="flex-1 overflow-y-auto px-2 pb-2">
        <div
          v-for="c in conversations"
          :key="c.id"
          class="group mb-1 flex cursor-pointer items-center gap-2 rounded-md px-2 py-2 hover:bg-accent"
          :class="c.id === currentId ? 'bg-accent' : ''"
          @click="openConversation(c.id)"
        >
          <div class="min-w-0 flex-1">
            <div class="truncate text-sm">{{ c.title || '新对话' }}</div>
            <div class="mt-0.5 flex items-center gap-1 text-xs text-muted-foreground">
              <span>{{ c.mode === 'platform' ? '平台上下文' : '通用' }}</span>
              <span>·</span>
              <span>{{ (c.updatedAt ?? '').slice(5, 16).replace('T', ' ') }}</span>
            </div>
          </div>
          <a-popconfirm title="删除该会话？" @confirm="removeConversation(c.id)">
            <a-button
              class="opacity-0 group-hover:opacity-100"
              danger
              size="small"
              type="text"
              @click.stop
            >
              删
            </a-button>
          </a-popconfirm>
        </div>
        <a-empty
          v-if="conversations.length === 0"
          :image-style="{ height: '48px' }"
          description="暂无会话"
        />
      </div>
    </div>

    <!-- 对话主区 -->
    <div
      class="flex min-w-0 flex-1 flex-col rounded-lg border border-border bg-card"
    >
      <template v-if="current">
        <div class="flex items-center gap-2 border-b border-border px-4 py-2.5">
          <span class="text-sm font-medium">{{ current.title || '新对话' }}</span>
          <a-tag v-if="current.mode === 'platform'" color="geekblue">
            平台上下文
          </a-tag>
          <div class="flex-1"></div>
          <a-button
            v-if="current.mode === 'platform'"
            size="small"
            @click="openMount"
          >
            ⛓ 挂载配置
          </a-button>
        </div>

        <div ref="scrollBody" class="flex-1 space-y-4 overflow-y-auto p-4">
          <div
            v-for="(m, i) in messages"
            :key="i"
            class="flex"
            :class="m.role === 'user' ? 'justify-end' : 'justify-start'"
          >
            <div
              class="max-w-[85%] rounded-lg px-3.5 py-2.5 text-sm"
              :class="
                m.role === 'user'
                  ? 'bg-primary text-primary-foreground'
                  : 'border border-border bg-background'
              "
            >
              <!-- eslint-disable-next-line vue/no-v-html 内容经 renderMd 内 DOMPurify 消毒（AI 输出属不可信输入） -->
              <div
                v-if="m.role === 'assistant'"
                class="prose prose-sm max-w-none break-words [&_pre]:overflow-x-auto [&_pre]:rounded [&_pre]:bg-muted [&_pre]:p-2 [&_code]:text-xs"
                v-html="renderMd(m.content || (m.streaming ? '' : '（无内容）'))"
              ></div>
              <template v-else>{{ m.content }}</template>
              <span
                v-if="m.streaming"
                class="ml-0.5 inline-block h-3.5 w-1.5 animate-pulse bg-foreground/60 align-middle"
              ></span>
              <div
                v-if="m.status === 'aborted'"
                class="mt-1 text-xs text-muted-foreground"
              >
                已停止
              </div>
            </div>
          </div>
        </div>

        <div class="border-t border-border p-3">
          <div class="flex items-end gap-2">
            <a-textarea
              v-model:value="input"
              :auto-size="{ minRows: 1, maxRows: 6 }"
              :disabled="streaming"
              placeholder="输入问题，Enter 发送（Shift+Enter 换行）"
              @keydown.enter.exact.prevent="send"
            />
            <a-button
              v-if="!streaming"
              :disabled="!input.trim()"
              type="primary"
              @click="send"
            >
              发送
            </a-button>
            <a-button v-else danger @click="stop">停止</a-button>
          </div>
        </div>
      </template>

      <div v-else class="flex flex-1 flex-col items-center justify-center gap-3">
        <div class="text-4xl">🤖</div>
        <div class="text-muted-foreground">
          新建对话开始——通用模式不接平台数据，平台上下文模式可挂载项目/主机
        </div>
        <div class="flex gap-2">
          <a-button type="primary" @click="newConversation('general')">
            新对话
          </a-button>
          <a-button @click="newConversation('platform')">平台上下文</a-button>
        </div>
      </div>
    </div>

    <!-- 挂载配置抽屉 -->
    <a-drawer
      v-model:open="mountOpen"
      title="平台上下文挂载"
      :width="420"
    >
      <a-form layout="vertical">
        <a-form-item
          label="挂载项目"
          extra="对话将携带项目概况、近期构建与发布记录"
        >
          <a-select
            v-model:value="mountForm.projectIds"
            :options="
              projects.map((p) => ({ label: p.name, value: p.id }))
            "
            mode="multiple"
            placeholder="不挂载"
            show-search
            option-filter-prop="label"
          />
        </a-form-item>
        <a-form-item
          label="挂载主机"
          extra="对话将携带主机清单（主机事件仅管理员视角可见）"
        >
          <a-select
            v-model:value="mountForm.serverIds"
            :options="
              servers.map((s) => ({
                label: `${s.name}（${s.host}）`,
                value: s.id,
              }))
            "
            mode="multiple"
            placeholder="不挂载"
            show-search
            option-filter-prop="label"
          />
        </a-form-item>
        <a-form-item label="时间窗" extra="事件与记录的回溯范围">
          <a-select
            v-model:value="mountForm.hours"
            :options="[
              { label: '近 6 小时', value: 6 },
              { label: '近 24 小时', value: 24 },
              { label: '近 3 天', value: 72 },
              { label: '近 7 天', value: 168 },
            ]"
            style="width: 180px"
          />
        </a-form-item>
        <a-button type="primary" @click="saveMount">保存挂载</a-button>
      </a-form>
    </a-drawer>
  </div>
</template>
