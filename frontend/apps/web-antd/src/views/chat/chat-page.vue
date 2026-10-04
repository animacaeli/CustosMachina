<script lang="ts" setup>
import type { ChatAttachment, Conversation } from '#/api/chat';
import type { AiSkill } from '#/api/chat/skills';
import type { PlatformUser } from '#/api/system/user';

import { computed, nextTick, onMounted, ref } from 'vue';

import { useUserStore } from '@vben/stores';

import { DeleteOutlined, PaperClipOutlined, PlusOutlined } from '@ant-design/icons-vue';
import { message as antMessage } from 'ant-design-vue';
import DOMPurify from 'dompurify';
import { marked } from 'marked';

import {
  cancelActionApi,
  chatStreamApi,
  confirmActionApi,
  createConversationApi,
  deleteConversationApi,
  listActionsApi,
  listConversationsApi,
  listMessagesApi,
} from '#/api/chat';
import { listSkillsApi } from '#/api/chat/skills';
import { getUserListApi } from '#/api/system/user';

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
// admin 视图：按用户查看对话历史（默认当前用户）+ 可选显示已软删会话（留档）
// vben userInfo.userId 是 string，统一转 number（与后端 uint 对齐）
const myId = computed(() => Number(userStore.userInfo?.userId ?? 0) || 0);
// 只读视图：已删会话（留档）或他人的会话（跨用户仅查看，不能代发消息）
const readonlyView = computed(
  () =>
    !!current.value &&
    (current.value.deleted === true || current.value.userId !== myId.value),
);
const filterUserId = ref<number>(0);
const showDeleted = ref(false);
const users = ref<PlatformUser[]>([]);

async function loadConversations() {
  conversations.value = await listConversationsApi(
    isAdmin.value
      ? { deleted: showDeleted.value, userId: filterUserId.value || myId.value }
      : {},
  );
}

async function onFilterUser(val: number | undefined) {
  filterUserId.value = val ?? myId.value;
  currentId.value = null;
  messages.value = [];
  await loadConversations();
}

async function toggleDeleted(checked: any) {
  showDeleted.value = Boolean(checked);
  currentId.value = null;
  messages.value = [];
  await loadConversations();
}

// 惰性建会话：点「新对话/平台上下文」只进草稿态，首条消息发送才真正创建——
// 新建后没开口的对话不落库、不进历史（空会话直接丢弃）
const draftMode = ref<'general' | 'platform' | null>(null);
const activeMode = computed(() => current.value?.mode ?? draftMode.value);

function newConversation(mode: 'general' | 'platform') {
  draftMode.value = mode;
  currentId.value = null;
  messages.value = [];
  // 正在看别人的历史时新建：切回自己的视图（新会话归属当前管理员）
  if (isAdmin.value && filterUserId.value !== myId.value) {
    filterUserId.value = myId.value;
    loadConversations();
  }
}

async function openConversation(id: number) {
  if (streaming.value) {
    antMessage.warning('当前会话回复进行中，请先停止');
    return;
  }
  draftMode.value = null;
  currentId.value = id;
  messages.value = (await listMessagesApi(id)).map((m) => ({
    ...m,
    actions: (m.actions ?? []).map((a) => ({ ...a, status: 'pending' })),
    attachments: m.attachments ?? undefined,
    skill: m.skill ?? undefined,
    tools: m.tools ?? undefined,
  }));
  // 操作卡状态刷新（消息里只留了 id/type/summary，终态从服务端取）
  const ids = messages.value.flatMap((m) => (m.actions ?? []).map((a) => a.id));
  if (ids.length > 0) {
    try {
      const acts = await listActionsApi(ids);
      const byId = new Map(acts.map((a) => [a.id, a]));
      for (const m of messages.value) {
        if (!m.actions) continue;
        m.actions = m.actions.map((a) => {
          const fresh = byId.get(a.id);
          return fresh ? { ...a, result: fresh.result, status: fresh.status } : a;
        });
      }
    } catch {
      /* 状态刷新失败不阻塞会话打开 */
    }
  }
  await nextTick();
  scrollToBottom();
}

// ---- M4 操作卡：确认/取消（仅发起人；执行走后端 casbin 判权） ----
async function confirmActionCard(a: UiAction) {
  try {
    const out = await confirmActionApi(a.id);
    a.status = out.status;
    a.result = out.result;
    if (out.status === 'done') {
      antMessage.success(out.result || '已执行');
    } else {
      antMessage.error(out.result || '执行失败');
    }
  } catch (e: any) {
    antMessage.error(e?.response?.data?.message ?? '确认失败');
  }
}

async function cancelActionCard(a: UiAction) {
  try {
    const out = await cancelActionApi(a.id);
    a.status = out.status;
  } catch (e: any) {
    antMessage.error(e?.response?.data?.message ?? '取消失败');
  }
}

async function removeConversation(id: number) {
  await deleteConversationApi(id);
  // 软删除：勾选「显示已删除」时会话仍在（带标记），刷新即可；否则直接移除
  if (isAdmin.value && showDeleted.value) {
    await loadConversations();
    return;
  }
  conversations.value = conversations.value.filter((c) => c.id !== id);
  if (currentId.value === id) {
    currentId.value = null;
    messages.value = [];
  }
}

// ---- 消息与流式 ----
// 操作卡（M4 确认层）：AI 生成意图 → 用户确认才执行；状态实时（pending 可操作，
// 终态只读展示）
interface UiAction {
  id: number;
  result?: string;
  status: string;
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
  tools?: { arguments?: string; name: string }[]; // assistant 调过的平台工具
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
  if (readonlyView.value) {
    antMessage.warning('只读会话（已删除留档或他人会话），不能发送消息');
    return;
  }
  let content = input.value.trim();
  if ((!content && pendingFiles.value.length === 0) || streaming.value) return;
  if (!currentId.value && !draftMode.value) return;
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
    freshMode = draftMode.value;
    const c = await createConversationApi({ mode: freshMode ?? 'general' });
    conversations.value.unshift(c);
    currentId.value = c.id;
    draftMode.value = null;
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
      // M4 确认层：生成待确认操作卡（不自动执行，用户点确认）
      assistant!.actions = [
        ...(assistant!.actions ?? []),
        { id: a.id, result: a.result, status: a.status, summary: a.summary, type: a.type },
      ];
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
        draftMode.value = freshMode;
        input.value = content;
        messages.value = [];
        if (failId) {
          deleteConversationApi(failId).then(() => loadConversations());
        }
      }
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
  filterUserId.value = myId.value;
  if (isAdmin.value) {
    try {
      users.value = await getUserListApi();
    } catch {
      users.value = [{ displayName: '我', id: myId.value, username: '我' } as PlatformUser];
    }
  }
  await Promise.all([loadConversations(), loadSkills()]);
});
</script>

<template>
  <div class="flex h-full gap-3 p-3">
    <!-- 会话侧栏：折叠后只留展开按钮（会话历史整体收起，非树形层级） -->
    <div
      v-if="!sidebarCollapsed"
      class="flex w-72 shrink-0 flex-col rounded-lg border border-border bg-card"
    >
      <!-- 折叠图标同一行：用户下拉（默认当前用户）+「显示已删除会话历史」；均仅 admin 可见 -->
      <div class="flex items-center gap-2 px-3 pt-3">
        <a-button size="small" type="text" title="收起会话列表" @click="sidebarCollapsed = true">
          <span class="text-muted-foreground">⟨</span>
        </a-button>
        <a-select
          v-if="isAdmin"
          :value="filterUserId || myId"
          class="min-w-0 flex-1"
          size="small"
          @change="onFilterUser($event)"
        >
          <a-select-option v-for="u in users" :key="u.id" :value="u.id">
            {{ u.displayName || u.username }}（{{ u.username }}）
          </a-select-option>
        </a-select>
        <a-checkbox
          v-if="isAdmin"
          class="shrink-0"
          :checked="showDeleted"
          @change="toggleDeleted($event.target.checked)"
        >
          <span class="whitespace-nowrap text-xs">显示已删除会话历史</span>
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
            <div
              class="truncate text-sm"
              :class="c.deleted ? 'text-muted-foreground line-through' : ''"
            >
              {{ c.title || '新对话' }}
            </div>
            <div class="mt-0.5 flex items-center gap-1 text-xs text-muted-foreground">
              <span v-if="c.deleted" class="text-red-500">已删除</span>
              <span v-if="c.deleted">·</span>
              <span v-if="c.owner" class="text-primary">{{ c.owner }}</span>
              <span v-if="c.owner">·</span>
              <span>{{ c.mode === 'platform' ? '平台上下文' : '通用' }}</span>
              <span>·</span>
              <span>{{ (c.updatedAt ?? '').slice(5, 16).replace('T', ' ') }}</span>
            </div>
          </div>
          <a-popconfirm
            v-if="!c.deleted"
            title="删除后你不再可见（管理员可留档查看）"
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
      <template v-if="current || draftMode">
        <div class="flex items-center gap-2 border-b border-border px-4 py-2.5">
          <span class="text-sm font-medium">{{ current?.title || '新对话' }}</span>
          <a-tag v-if="activeMode === 'platform'" color="geekblue">
            平台上下文
          </a-tag>
          <a-tag v-if="current?.deleted" color="red">已删除 · 留档只读</a-tag>
          <a-tag v-else-if="current && current.userId !== myId" color="orange">
            他人会话 · 只读
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
              <!-- function calling：assistant 本轮调过的平台工具（实时 + 历史） -->
              <div
                v-if="m.role === 'assistant' && m.tools?.length"
                class="mb-1.5 flex flex-wrap gap-1"
              >
                <a-tag
                  v-for="(t, ti) in m.tools"
                  :key="ti"
                  class="font-mono"
                  color="cyan"
                  :title="t.arguments"
                >
                  🔧 {{ t.name }}
                </a-tag>
              </div>
              <!-- M4 操作确认卡：AI 生成意图 → 人确认才执行 -->
              <div
                v-if="m.role === 'assistant' && m.actions?.length"
                class="mb-2 space-y-1.5"
              >
                <div
                  v-for="a in m.actions"
                  :key="a.id"
                  class="rounded-md border px-3 py-2 text-xs"
                  :class="
                    a.status === 'pending'
                      ? 'border-amber-500/50 bg-amber-500/5'
                      : a.status === 'done'
                        ? 'border-green-500/50 bg-green-500/5'
                        : a.status === 'failed'
                          ? 'border-red-500/50 bg-red-500/5'
                          : 'border-border bg-muted/40'
                  "
                >
                  <div class="flex items-center gap-2">
                    <span class="font-medium text-foreground">
                      {{ a.type === 'restart_container' ? '🔄 重启容器' : a.type === 'trigger_cron' ? '⏱️ 触发定时任务' : a.type === 'deploy_config' ? '📤 下发配置' : a.type }}
                    </span>
                    <span v-if="a.status === 'pending'" class="text-amber-600">待确认</span>
                    <span v-else-if="a.status === 'done'" class="text-green-600">已执行</span>
                    <span v-else-if="a.status === 'failed'" class="text-red-500">执行失败</span>
                    <span v-else class="text-muted-foreground">
                      {{ a.status === 'cancelled' ? '已取消' : '已过期' }}
                    </span>
                  </div>
                  <div class="mt-1 text-muted-foreground">{{ a.summary }}</div>
                  <div v-if="a.result" class="mt-1 break-all text-muted-foreground">
                    {{ a.result }}
                  </div>
                  <div v-if="a.status === 'pending'" class="mt-2 flex gap-2">
                    <a-button danger size="small" type="primary" @click="confirmActionCard(a)">
                      确认执行
                    </a-button>
                    <a-button size="small" @click="cancelActionCard(a)">取消</a-button>
                    <span class="self-center text-muted-foreground">5 分钟内有效</span>
                  </div>
                </div>
              </div>
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
                :disabled="streaming || readonlyView"
                placeholder="输入问题，Enter 发送；/ 触发技能命令"
                @input="onInputForSlash"
                @keydown="inputKeydown"
              />
            </div>
            <a-button
              v-if="!streaming"
              :disabled="readonlyView || (!input.trim() && pendingFiles.length === 0)"
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
          新建对话开始——通用模式不接平台数据，平台上下文模式自动注入全平台实时数据
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
