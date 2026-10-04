<script lang="ts" setup>
import type { ChatAttachment, Conversation } from '#/api/chat';
import type { AiSkill } from '#/api/chat/skills';

import { computed, nextTick, onMounted, ref } from 'vue';

import { useUserStore } from '@vben/stores';

import { DeleteOutlined, PaperClipOutlined, PlusOutlined } from '@ant-design/icons-vue';
import { message as antMessage } from 'ant-design-vue';
import DOMPurify from 'dompurify';
import { marked } from 'marked';

import {
  chatStreamApi,
  createConversationApi,
  deleteConversationApi,
  listConversationsApi,
  listMessagesApi,
} from '#/api/chat';
import { listSkillsApi } from '#/api/chat/skills';

defineOptions({ name: 'AiChat' });

marked.setOptions({ breaks: true, gfm: true });

// ---- 会话列表 ----
const conversations = ref<Conversation[]>([]);
const currentId = ref<null | number>(null);
const current = computed(() =>
  conversations.value.find((c) => c.id === currentId.value),
);
// 侧栏折叠（收起时只留展开按钮——会话历史收进侧栏，非树形层级）
const sidebarCollapsed = ref(false);
// admin 全量视图：查看全平台会话（含归属人）；普通用户恒 false
const userStore = useUserStore();
const isAdmin = computed(() => {
  const roles = userStore.userInfo?.roles ?? [];
  return roles.includes('superadmin') || roles.includes('admin');
});
const showAll = ref(false);

async function loadConversations() {
  conversations.value = await listConversationsApi(
    isAdmin.value && showAll.value,
  );
}

async function toggleAll(checked: any) {
  showAll.value = Boolean(checked);
  currentId.value = null;
  messages.value = [];
  await loadConversations();
}

async function newConversation(mode: 'general' | 'platform') {
  const c = await createConversationApi({ mode });
  conversations.value.unshift(c);
  currentId.value = c.id;
  messages.value = [];
}

async function openConversation(id: number) {
  if (streaming.value) {
    antMessage.warning('当前会话回复进行中，请先停止');
    return;
  }
  currentId.value = id;
  messages.value = (await listMessagesApi(id)).map((m) => ({
    ...m,
    attachments: m.attachments ?? undefined,
    skill: m.skill ?? undefined,
  }));
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
  attachments?: ChatAttachment[];
  content: string;
  role: string;
  skill?: string;
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
  let content = input.value.trim();
  if ((!content && pendingFiles.value.length === 0) || streaming.value || !currentId.value)
    return;
  // /命令解析：输入 /name 问题...（或已面板锁定）
  let skill = activeSkill.value?.name ?? '';
  const m = content.match(/^\/([a-z0-9_-]+)\s+([\s\S]*)$/i);
  if (m) {
    skill = m[1]!.toLowerCase();
    content = m[2]!.trim();
    const sk = skills.value.find((x) => x.name === skill);
    activeSkill.value = sk ?? null;
  } else if (activeSkill.value) {
    skill = activeSkill.value.name;
  }
  if (content === '' && skill === '') return;
  if (pendingFiles.value.some((f) => !f.data)) {
    antMessage.warning('附件仍在读取中');
    return;
  }
  input.value = '';
  activeSkill.value = null;
  slashOpen.value = false;
  const atts = [...pendingFiles.value];
  pendingFiles.value = [];
  messages.value.push(
    {
      content,
      role: 'user',
      skill: skill || undefined,
      attachments: atts.length > 0 ? atts : undefined,
    },
    { content: '', role: 'assistant', streaming: true },
  );
  streaming.value = true;
  await nextTick();
  scrollToBottom();
  const assistant = messages.value.at(-1);
  stopFn = await chatStreamApi(
    currentId.value,
    content,
    {
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
    },
    atts,
    skill,
  );
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

// ---- / 命令面板（Claude Code 风格）：首字符 / 弹出管理员安装的技能 ----
const skills = ref<AiSkill[]>([]);
const slashOpen = ref(false);
const slashFilter = ref('');
const slashIndex = ref(0);
// 当前选中的技能（输入框首段 /name 已匹配时锁定）
const activeSkill = ref<AiSkill | null>(null);

async function loadSkills() {
  skills.value = await listSkillsApi();
}

const slashMatches = computed(() => {
  const kw = slashFilter.value.toLowerCase();
  return skills.value.filter((sk) => kw === '' || sk.name.includes(kw));
});

function onInputForSlash() {
  const v = input.value;
  const m = v.match(/^\/([a-z0-9_-]*)(\s|$)/i);
  if (m === null) {
    // 不以 / 开头：关闭面板，清除锁定（除非此前已锁定并带空格——保留到发送）
    slashOpen.value = false;
    if (!activeSkill.value) return;
    return;
  }
  const namePart = m[1] ?? '';
  if (m[2] === ' ' || (namePart && activeSkill.value?.name === namePart.toLowerCase())) {
    // 已锁定（/name + 空格 后继续输入问题）
    slashOpen.value = false;
    return;
  }
  activeSkill.value = null;
  slashFilter.value = namePart;
  slashIndex.value = 0;
  slashOpen.value = skills.value.length > 0;
}

function pickSkill(sk: AiSkill) {
  activeSkill.value = sk;
  input.value = `/${sk.name} `;
  slashOpen.value = false;
  const box = document.querySelector<HTMLTextAreaElement>(
    'textarea[placeholder*="输入问题"]',
  );
  box?.focus();
}

// 单一 keydown 入口：面板开着时 Enter=选中技能（绝不能落到 send），
// 面板关着时 Enter=发送。此前 slashKeydown 与 @keydown.enter 两个监听器
// 同元素并存，pickSkill 先关面板导致 send 的守卫失效——Enter 直接把半截消息发了出去。
function inputKeydown(e: KeyboardEvent) {
  if (slashOpen.value && slashMatches.value.length > 0) {
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      slashIndex.value = (slashIndex.value + 1) % slashMatches.value.length;
      return;
    }
    if (e.key === 'ArrowUp') {
      e.preventDefault();
      slashIndex.value =
        (slashIndex.value - 1 + slashMatches.value.length) % slashMatches.value.length;
      return;
    }
    if (e.key === 'Tab' || e.key === 'Enter') {
      e.preventDefault();
      pickSkill(slashMatches.value[slashIndex.value]!);
      return;
    }
    if (e.key === 'Escape') {
      e.preventDefault();
      slashOpen.value = false;
      return;
    }
  }
  // 输入法组合中的 Enter 是选字确认，不是发送
  if (
    e.key === 'Enter' &&
    !e.shiftKey &&
    !e.ctrlKey &&
    !e.metaKey &&
    !e.altKey &&
    !e.isComposing
  ) {
    e.preventDefault();
    send();
  }
}

// ---- 附件上传（多模态）：≤3 个、单文件 ≤4MB；图片随消息 image_url、文本类并入正文 ----
const pendingFiles = ref<ChatAttachment[]>([]);
const MAX_FILE_BYTES = 4 << 20;

function onPickFile(_file: any) {
  return false; // 阻止 a-upload 自动上传，手动读取
}

async function onFileChange(info: any) {
  const list = info.fileList ?? [];
  pendingFiles.value = list
    .filter((f: any) => f.originFileObj || f.data)
    .slice(0, 3)
    .map((f: any) => ({
      name: f.name,
      mime: f.type || 'application/octet-stream',
      data: f.data ?? '',
    }));
  // 异步读 base64
  for (const f of list) {
    const file = f.originFileObj;
    if (!file || f.data) continue;
    if (file.size > MAX_FILE_BYTES) {
      antMessage.error(`附件 ${f.name} 超过 4MB`);
      f.status = 'error';
      continue;
    }
    f.data = await readAsDataURL(file);
  }
}

function readAsDataURL(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const r = new FileReader();
    r.onload = () => {
      // "data:<mime>;base64,xxx" → 只要 base64 段
      const out = String(r.result ?? '');
      resolve(out.slice(out.indexOf(',') + 1));
    };
    r.onerror = reject;
    r.readAsDataURL(file);
  });
}

function attachmentSrc(a: ChatAttachment): string {
  return `data:${a.mime};base64,${a.data}`;
}

// Markdown 渲染（消毒：AI 输出可能携带 pack 内不可信内容的 HTML）
function renderMd(md: string): string {
  return DOMPurify.sanitize(marked.parse(md) as string);
}

onMounted(async () => {
  await Promise.all([loadConversations(), loadSkills()]);
});
</script>

<template>
  <div class="flex h-full gap-3 p-3">
    <!-- 会话侧栏：折叠后只留展开按钮（会话历史整体收起，非树形层级） -->
    <div
      v-if="!sidebarCollapsed"
      class="flex w-64 shrink-0 flex-col rounded-lg border border-border bg-card"
    >
      <div class="flex items-center gap-2 px-3 pt-3">
        <a-button size="small" type="text" title="收起会话列表" @click="sidebarCollapsed = true">
          <span class="text-muted-foreground">⟨</span>
        </a-button>
        <div class="flex-1"></div>
        <a-checkbox
          v-if="isAdmin"
          :checked="showAll"
          @change="toggleAll($event.target.checked)"
        >
          <span class="text-xs">全部用户</span>
        </a-checkbox>
      </div>
      <div class="flex gap-2 p-3">
        <a-button block type="primary" @click="newConversation('general')">
          <PlusOutlined /> 新对话
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
              <span v-if="c.owner" class="text-primary">{{ c.owner }}</span>
              <span v-if="c.owner">·</span>
              <span>{{ c.mode === 'platform' ? '平台上下文' : '通用' }}</span>
              <span>·</span>
              <span>{{ (c.updatedAt ?? '').slice(5, 16).replace('T', ' ') }}</span>
            </div>
          </div>
          <a-popconfirm
            title="确认删除该会话及其全部消息？"
            ok-text="删除"
            ok-type="danger"
            @confirm="removeConversation(c.id)"
          >
            <a-button
              class="opacity-40 group-hover:opacity-100"
              danger
              size="small"
              title="删除会话"
              type="text"
              @click.stop
            >
              <DeleteOutlined />
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

    <!-- 折叠态：窄条展开按钮 -->
    <div
      v-if="sidebarCollapsed"
      class="flex w-9 shrink-0 flex-col items-center rounded-lg border border-border bg-card py-3"
    >
      <a-button size="small" type="text" title="展开会话列表" @click="sidebarCollapsed = false">
        <span class="text-muted-foreground">⟩</span>
      </a-button>
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
                class="prose prose-sm dark:prose-invert max-w-none break-words text-foreground [&_a]:text-primary [&_pre]:overflow-x-auto [&_pre]:rounded [&_pre]:bg-muted [&_pre]:p-2 [&_pre]:text-foreground [&_code]:text-xs [&_code]:text-foreground [&_li]:text-foreground [&_p]:text-foreground"
                v-html="renderMd(m.content || (m.streaming ? '' : '（无内容）'))"
              ></div>
              <template v-else>
                <a-tag v-if="m.skill" class="mb-1" color="purple">
                  ⚡ /{{ m.skill }}
                </a-tag>
                <div v-if="m.attachments?.length" class="mb-1.5 flex flex-wrap gap-1.5">
                  <img
                    v-for="(a, ai) in m.attachments"
                    :key="ai"
                    :alt="a.name"
                    :src="attachmentSrc(a)"
                    class="h-16 w-16 rounded border border-border object-cover"
                    :title="a.name"
                  />
                </div>
                {{ m.content }}
              </template>
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
          <div v-if="pendingFiles.length > 0" class="mb-2 flex flex-wrap gap-1.5">
            <span
              v-for="(f, fi) in pendingFiles"
              :key="fi"
              class="flex items-center gap-1 rounded border border-border bg-muted px-2 py-0.5 text-xs"
            >
              <PaperClipOutlined />
              {{ f.name }}
              <a-button size="small" type="text" @click="pendingFiles.splice(fi, 1)">
                <DeleteOutlined style="font-size: 10px" />
              </a-button>
            </span>
          </div>
          <div class="flex items-end gap-2">
            <a-upload
              :before-upload="onPickFile"
              :file-list="[]"
              :max-count="3"
              accept="image/*,.txt,.md,.json,.yaml,.yml,.csv,.log,.go,.py,.js,.ts"
              :show-upload-list="false"
              multiple
              @change="onFileChange"
            >
              <a-button :disabled="streaming || pendingFiles.length >= 3" title="附件（≤3 个，单个 ≤4MB）">
                <PaperClipOutlined />
              </a-button>
            </a-upload>
            <div class="relative min-w-0 flex-1">
              <!-- / 命令面板（Claude Code 风格） -->
              <div
                v-if="slashOpen"
                class="absolute bottom-full left-0 z-20 mb-1 w-full overflow-hidden rounded-md border border-border bg-popover shadow-lg"
              >
                <div class="border-b border-border px-3 py-1.5 text-xs text-muted-foreground">
                  技能命令（↑↓ 选择、Tab/Enter 确认、Esc 关闭）
                </div>
                <div
                  v-for="(sk, i) in slashMatches"
                  :key="sk.id"
                  class="cursor-pointer px-3 py-2 text-sm"
                  :class="i === slashIndex ? 'bg-accent' : ''"
                  @click="pickSkill(sk)"
                  @mouseenter="slashIndex = i"
                >
                  <span class="font-mono text-primary">/{{ sk.name }}</span>
                  <span class="ml-2">{{ sk.title }}</span>
                  <span class="ml-2 text-xs text-muted-foreground">{{
                    sk.description
                  }}</span>
                </div>
                <div v-if="slashMatches.length === 0" class="px-3 py-2 text-sm text-muted-foreground">
                  没有匹配的技能
                </div>
              </div>
              <a-textarea
                v-model:value="input"
                :auto-size="{ minRows: 1, maxRows: 6 }"
                :disabled="streaming"
                placeholder="输入问题，Enter 发送；/ 触发技能命令"
                @input="onInputForSlash"
                @keydown="inputKeydown"
              />
            </div>
            <a-button
              v-if="!streaming"
              :disabled="!input.trim() && pendingFiles.length === 0"
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
</div>
</template>
