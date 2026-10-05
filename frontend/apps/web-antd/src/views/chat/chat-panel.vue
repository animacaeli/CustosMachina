<script lang="ts" setup>
import type { ChatAttachment, Conversation } from '#/api/chat';
import type { AiSkill } from '#/api/chat/skills';

import {
  computed,
  nextTick,
  onMounted,
  ref,
  watch,
} from 'vue';

import { PaperClipOutlined } from '@ant-design/icons-vue';
import { message as antMessage } from 'ant-design-vue';
import DOMPurify from 'dompurify';
import { marked } from 'marked';

import {
  chatStreamApi,
  createConversationApi,
  deleteConversationApi,
  listMessagesApi,
} from '#/api/chat';
import { listSkillsApi } from '#/api/chat/skills';

/**
 * ChatPanel 对话面板（P6-M10 组件化）：消息区 + 输入区 + / 命令面板 + 附件 +
 * 工具调用标签 + 操作建议卡 + 流式/停止。/chat 页与全站悬浮抽屉共用。
 * 外壳负责会话列表与切换：conv=null 为新对话草稿（initialMode 决定模式），
 * 首条消息发送时惰性建会话并 emit('used')。
 */

defineOptions({ name: 'ChatPanel' });

const props = defineProps<{
  conv?: Conversation | null;
  initialMode?: 'general' | 'platform';
  /** 页面上下文：用户当前所在页面（标题+路径），AI 感知后回答更贴合场景 */
  pageContext?: string;
  readonly?: boolean;
}>();

const emit = defineEmits<{
  (e: 'used', conv: Conversation): void;
}>();

marked.setOptions({ breaks: true, gfm: true });

// ---- 会话状态（外壳切换经 conv prop；草稿经 currentId=null） ----
const currentId = ref<null | number>(props.conv?.id ?? null);
const convMode = ref<'general' | 'platform' | undefined>(props.conv?.mode);
const activeMode = computed(() => convMode.value ?? props.initialMode ?? 'platform');

watch(
  () => props.conv,
  async (c) => {
    if (streaming.value) return; // 流式中不切（外壳也有守卫）
    const id = c?.id ?? null;
    if (id === currentId.value) return;
    currentId.value = id;
    convMode.value = c?.mode;
    if (id === null) {
      messages.value = [];
      return;
    }
    await loadMessages(id);
  },
);

// ---- 消息与流式 ----
// 操作建议卡（用户定调：AI 只建议不执行——不论身份）：展示建议 + 「去处理」
// 跳转平台对应页面，用户自行操作
interface UiAction {
  route?: string;
  summary: string;
  type: string;
}

interface UiMessage {
  actions?: UiAction[];
  attachments?: ChatAttachment[];
  content: string;
  role: string;
  skill?: string;
  status?: string;
  streaming?: boolean;
  tools?: { arguments?: string; name: string }[];
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

async function loadMessages(id: number) {
  messages.value = (await listMessagesApi(id)).map((m) => ({
    ...m,
    actions: m.actions ?? undefined,
    attachments: m.attachments ?? undefined,
    skill: m.skill ?? undefined,
    tools: m.tools ?? undefined,
  }));
  await nextTick();
  scrollToBottom();
}

async function send() {
  if (props.readonly) {
    antMessage.warning('只读会话（已删除留档或他人会话），不能发送消息');
    return;
  }
  let content = input.value.trim();
  if ((!content && pendingFiles.value.length === 0) || streaming.value) return;
  if (!currentId.value && !activeMode.value) return;
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
  // 惰性建会话：首条消息发送才创建（空对话不保存）
  let freshMode: 'general' | 'platform' | null = null;
  if (!currentId.value) {
    freshMode = activeMode.value ?? 'platform';
    const c = await createConversationApi({ mode: freshMode });
    currentId.value = c.id;
    convMode.value = c.mode;
    emit('used', c);
  }
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
      onTool: (name, args) => {
        // function calling：模型调用平台工具，气泡顶部实时追加工具标签
        assistant!.tools = [...(assistant!.tools ?? []), { arguments: args, name }];
        scrollToBottom();
      },
      onAction: (a) => {
        // 操作建议卡：AI 只建议不执行，「去处理」由用户自行跳转操作
        assistant!.actions = [...(assistant!.actions ?? []), a];
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
        // 新建的会话首条发送就失败且无产出：回收空壳会话（不留无效历史），
        // 退回草稿态并把内容退回输入框供重试
        if (freshMode && !assistant!.content) {
          const failId = currentId.value;
          currentId.value = null;
          convMode.value = undefined;
          input.value = content;
          messages.value = [];
          if (failId) {
            deleteConversationApi(failId).then(() => undefined);
          }
        }
      },
    },
    atts,
    skill,
    props.pageContext,
  );
}

function finish() {
  streaming.value = false;
  stopFn = null;
}

function stop() {
  stopFn?.();
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
    slashOpen.value = false;
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

const inputRef = ref();

function pickSkill(sk: AiSkill) {
  activeSkill.value = sk;
  input.value = `/${sk.name} `;
  slashOpen.value = false;
  inputRef.value?.focus();
}

// 单一 keydown 入口：面板开着时 Enter=选中技能（绝不能落到 send），
// 面板关着时 Enter=发送。
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
  // antd Upload beforeUpload 返回 false 走手动读取
  return false;
}

async function onFileChange(info: any) {
  const file: File = info.file;
  if (pendingFiles.value.length >= 3) {
    antMessage.warning('每条消息最多 3 个附件');
    return;
  }
  if (file.size > MAX_FILE_BYTES) {
    antMessage.warning(`附件 ${file.name} 超过 4MB 限制`);
    return;
  }
  if (file.type.startsWith('image/') || file.size < 512 * 1024) {
    pendingFiles.value = [...pendingFiles.value, { data: '', mime: file.type || 'text/plain', name: file.name }];
    try {
      pendingFiles.value[pendingFiles.value.length - 1]!.data = await readAsDataURL(file);
    } catch {
      pendingFiles.value = pendingFiles.value.slice(0, -1);
      antMessage.error(`附件 ${file.name} 读取失败`);
    }
  } else {
    antMessage.warning('非图片附件不能超过 512KB');
  }
}

function readAsDataURL(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve((reader.result ?? '') as string);
    reader.onerror = reject;
    reader.readAsDataURL(file);
  });
}

function attachmentSrc(a: ChatAttachment): string {
  if (a.data.startsWith('data:')) return a.data;
  return `data:${a.mime};base64,${a.data}`;
}

// Markdown 渲染（消毒：AI 输出可能携带 pack 内不可信内容的 HTML）
function renderMd(md: string): string {
  return DOMPurify.sanitize(marked.parse(md) as string);
}

// 快捷提问卡片入口（外壳经 ref 调用）
async function sendText(text: string) {
  if (streaming.value || props.readonly) return;
  input.value = text;
  await send();
}

defineExpose({
  isStreaming: () => streaming.value,
  sendText,
});

// 初始会话加载放 onMounted（watch immediate 会踩 streaming 的 TDZ——watch 块在消息状态声明之前）
onMounted(async () => {
  await loadSkills();
  if (props.conv?.id) await loadMessages(props.conv.id);
});
</script>

<template>
  <div class="flex min-h-0 flex-1 flex-col">
    <!-- 消息区 -->
    <div ref="scrollBody" class="flex-1 space-y-4 overflow-y-auto p-4">
      <div
        v-for="(m, i) in messages"
        :key="i"
        class="flex"
        :class="m.role === 'user' ? 'justify-end' : 'justify-start'"
      >
        <div
          class="max-w-[85%] min-w-0 rounded-lg px-3.5 py-2.5 text-sm"
          :class="
            m.role === 'user'
              ? 'bg-primary text-primary-foreground'
              : 'border border-border bg-background'
          "
        >
          <!-- M4 操作建议卡：AI 只建议不执行，「去处理」跳转平台页面 -->
          <div
            v-if="m.role === 'assistant' && m.actions?.length"
            class="mb-2 space-y-1.5"
          >
            <div
              v-for="(a, ai) in m.actions"
              :key="ai"
              class="rounded-md border border-primary/40 bg-primary/5 px-3 py-2 text-xs"
            >
              <div class="flex items-center gap-2">
                <span class="font-medium text-foreground">
                  {{ a.type === 'restart_container' ? '🔄 容器重启建议' : a.type === 'trigger_cron' ? '⏱️ 定时任务触发建议' : a.type === 'deploy_config' ? '📤 配置下发建议' : a.type }}
                </span>
                <span class="text-primary">操作建议</span>
              </div>
              <div class="mt-1 text-muted-foreground">{{ a.summary }}</div>
              <div class="mt-2 flex items-center gap-2">
                <a-button v-if="a.route" size="small" type="primary" @click="$router.push(a.route)">
                  去处理
                </a-button>
                <span class="text-muted-foreground">由你手动操作，AI 不会执行变更</span>
              </div>
            </div>
          </div>
          <!-- eslint-disable-next-line vue/no-v-html 内容经 renderMd 内 DOMPurify 消毒（AI 输出属不可信输入） -->
          <div
            v-if="m.role === 'assistant'"
            class="prose prose-sm dark:prose-invert max-w-none break-words text-foreground [&_a]:text-primary [&_img]:max-w-full [&_pre]:overflow-x-auto [&_pre]:rounded [&_pre]:bg-muted [&_pre]:p-2 [&_pre]:text-foreground [&_code]:text-xs [&_code]:text-foreground [&_li]:text-foreground [&_p]:text-foreground [&_svg]:max-w-full [&_table]:block [&_table]:overflow-x-auto [&_table]:whitespace-nowrap"
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
      <div
        v-if="messages.length === 0"
        class="flex h-full flex-col items-center justify-center gap-2 text-muted-foreground"
      >
        <div class="text-3xl">🤖</div>
        <div class="text-xs">输入问题开始对话；「/」唤起技能命令</div>
      </div>
    </div>

    <!-- 输入区 -->
    <div class="border-t border-border p-3">
      <div v-if="pendingFiles.length > 0" class="mb-2 flex flex-wrap gap-1.5">
        <a-tag v-for="(f, fi) in pendingFiles" :key="fi" closable @close="pendingFiles = pendingFiles.filter((_, x) => x !== fi)">
          {{ f.name }}{{ f.data ? '' : '（读取中…）' }}
        </a-tag>
      </div>
      <div class="relative flex items-end gap-2">
        <div v-if="slashOpen" class="absolute bottom-full left-0 z-20 mb-1 w-full overflow-hidden rounded-md border border-border bg-popover shadow-lg">
          <div class="border-b border-border px-3 py-1.5 text-xs text-muted-foreground">
            技能命令（↑↓ 选择、Tab/Enter 确认、Esc 关闭）
          </div>
          <div
            v-for="(sk, i) in slashMatches"
            :key="sk.name"
            class="cursor-pointer px-3 py-2"
            :class="i === slashIndex ? 'bg-accent' : ''"
            @click="pickSkill(sk)"
            @mouseenter="slashIndex = i"
          >
            <span class="font-mono text-xs text-primary">/{{ sk.name }}</span>
            <span class="ml-2">{{ sk.title }}</span>
            <span class="ml-2 text-xs text-muted-foreground">{{
              sk.description
            }}</span>
          </div>
          <div v-if="slashMatches.length === 0" class="px-3 py-2 text-sm text-muted-foreground">
            没有匹配的技能
          </div>
        </div>
        <a-upload
          :before-upload="onPickFile"
          :max-count="3"
          :show-upload-list="false"
          accept="image/*,.txt,.log,.json,.yaml,.yml,.toml,.ini,.env,.md,.conf"
          @change="onFileChange"
        >
          <a-button :disabled="streaming || readonly" title="附件（≤3 个，单文件 ≤4MB）">
            <PaperClipOutlined />
          </a-button>
        </a-upload>
        <a-textarea
          ref="inputRef"
          v-model:value="input"
          :auto-size="{ minRows: 1, maxRows: 6 }"
          :disabled="streaming || readonly"
          class="min-w-0 flex-1"
          placeholder="输入问题，Enter 发送；/ 触发技能命令"
          @input="onInputForSlash"
          @keydown="inputKeydown"
        />
        <a-button
          v-if="!streaming"
          :disabled="readonly || (!input.trim() && pendingFiles.length === 0)"
          type="primary"
          @click="send"
        >
          发送
        </a-button>
        <a-button v-else danger @click="stop">停止</a-button>
      </div>
    </div>
  </div>
</template>
