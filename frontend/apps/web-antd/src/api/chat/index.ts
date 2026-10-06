import { useAccessStore } from '@vben/stores';

import { apiURL, requestClient } from '#/api/request';

/** AI 对话（P6 M1）：会话 CRUD + SSE 流式 */

export type ChatMode = 'general' | 'platform';

export interface Mount {
  hours: number;
  projectIds: number[];
  serverIds: number[];
}

export interface Conversation {
  createdAt: string;
  deleted?: boolean; // 软删除标记（admin 全量视图返回）
  id: number;
  mode: ChatMode;
  mount?: Mount | null;
  owner?: string; // 仅 admin 全量视图返回
  title: string;
  updatedAt: string;
  userId: number;
}

export interface ChatAttachment {
  data: string; // base64
  mime: string;
  name: string;
}

export interface ChatToolTrace {
  arguments?: string;
  name: string;
}

export interface ChatMessage {
  actions?: null | { route?: string; summary: string; type: string }[];
  attachments?: ChatAttachment[] | null;
  content: string;
  conversationId: number;
  createdAt: string;
  id: number;
  packRedactions: number;
  role: 'assistant' | 'user';
  skill?: null | string;
  status: 'aborted' | 'done' | 'error';
  tools?: ChatToolTrace[] | null; // assistant 本轮调过的平台工具（function calling）
}

/** admin 可查看指定用户（userId）并可选包含已软删会话（deleted）；普通用户忽略 */
export async function listConversationsApi(
  opts: { deleted?: boolean; userId?: number } = {},
) {
  const params: Record<string, any> = {};
  if (opts.userId) params.user_id = opts.userId;
  if (opts.deleted) params.deleted = 1;
  return requestClient.get<Conversation[]>('/ai/chat/conversations', {
    params: Object.keys(params).length > 0 ? params : undefined,
  });
}

export async function createConversationApi(data: {
  mode?: ChatMode;
  mount?: Mount;
}) {
  return requestClient.post<Conversation>('/ai/chat/conversations', data);
}

export async function deleteConversationApi(id: number) {
  return requestClient.delete(`/ai/chat/conversations/${id}`);
}

export async function updateMountApi(id: number, mount: Mount) {
  return requestClient.put(`/ai/chat/conversations/${id}/mount`, { mount });
}

export async function listMessagesApi(id: number) {
  return requestClient.get<ChatMessage[]>(
    `/ai/chat/conversations/${id}/messages`,
  );
}

export interface ChatStreamHandlers {
  onDelta: (text: string) => void;
  onDone: (status: string, messageId?: number) => void;
  onError: (message: string) => void;
  onTool?: (name: string, args: string) => void; // 模型发起工具调用（function calling）
  onAction?: (a: { route?: string; summary: string; type: string }) => void; // 生成操作建议卡（AI 只建议不执行）
}

/**
 * SSE 流式对话：POST + ReadableStream 逐行解析（EventSource 不支持
 * POST/自定义头）。事件：delta / done / error / ping（心跳，忽略）。
 * 返回 abort 函数（前端"停止生成"= 断连，已生成部分由后端落库）。
 */
export async function chatStreamApi(
  conversationId: number,
  content: string,
  handlers: ChatStreamHandlers,
  attachments: ChatAttachment[] = [],
  skill = '',
  page = '',
): Promise<() => void> {
  const token = useAccessStore().accessToken;
  const controller = new AbortController();
  try {
    const resp = await fetch(
      `${apiURL}/ai/chat/conversations/${conversationId}/messages`,
      {
        method: 'POST',
        headers: {
          Authorization: `Bearer ${token}`,
          'Content-Type': 'application/json',
        },
        body: JSON.stringify({
          content,
          skill: skill || undefined,
          ...(page ? { page } : {}),
          ...(attachments.length > 0 ? { attachments } : {}),
        }),
        signal: controller.signal,
      },
    );
    if (!resp.ok || !resp.body) {
      let msg = `HTTP ${resp.status}`;
      try {
        const j = await resp.json();
        msg = j?.message ?? msg;
      } catch {
        /* 非 JSON 错误体 */
      }
      handlers.onError(msg);
      return () => controller.abort();
    }
    const reader = resp.body.getReader();
    const decoder = new TextDecoder();
    let buf = '';
    // 后台解析循环：按空行分帧，每帧解析 event:/data: 两行
    (async () => {
      try {
        for (;;) {
          const { done, value } = await reader.read();
          if (done) break;
          buf += decoder.decode(value, { stream: true });
          let idx: number;
          while ((idx = buf.indexOf('\n\n')) >= 0) {
            const frame = buf.slice(0, idx);
            buf = buf.slice(idx + 2);
            let event = 'message';
            let data = '';
            for (const line of frame.split('\n')) {
              if (line.startsWith('event:')) {
                event = line.slice(6).trim();
              } else if (line.startsWith('data:')) {
                data += line.slice(5).trim();
              }
            }
            if (event === 'ping' || event === 'message') continue;
            let payload: Record<string, any> = {};
            try {
              payload = JSON.parse(data) as Record<string, any>;
            } catch {
              continue;
            }
            if (event === 'delta') {
              handlers.onDelta(String(payload.text ?? ''));
            } else if (event === 'tool') {
              handlers.onTool?.(
                String(payload.name ?? ''),
                String(payload.args ?? ''),
              );
            } else if (event === 'action') {
              handlers.onAction?.(
                payload as unknown as {
                  route?: string;
                  summary: string;
                  type: string;
                },
              );
            } else if (event === 'done') {
              handlers.onDone(
                String(payload.status ?? 'done'),
                payload.messageId,
              );
              return;
            } else if (event === 'error') {
              handlers.onError(String(payload.message ?? '未知错误'));
              return;
            }
          }
        }
        // 流关闭但无 done 事件（异常断开）
        handlers.onError('连接已断开');
      } catch {
        // AbortError：用户停止生成，静默（后端已落库 partial）
      }
    })();
  } catch (error: any) {
    handlers.onError(error?.message ?? '请求失败');
  }
  return () => controller.abort();
}

/** P7-M3 编辑器 AI 助手：无会话一次性（三场景 advisory，结果由用户确认插入） */
export async function assistApi(data: {
  content?: string;
  fileName?: string;
  fileType?: string;
  question: string;
  scene: 'alert_rule' | 'cron' | 'editor' | 'release_check';
}) {
  const out = await requestClient.post<{ result: string }>('/ai/assist', data);
  return out.result;
}
