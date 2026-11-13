import { requestClient } from '#/api/request';

interface InboxItem {
  id: number;
  projectId: number;
  title: string;
  message: string;
  createdAt: string;
  isRead: boolean;
}

export type InboxAction = 'clear' | 'read' | 'read-all' | 'remove';

export function getInboxApi(before?: number) {
  const config = { params: { before }, silentError: before === undefined };
  return requestClient.get<{
    items: InboxItem[];
    next: number;
    unread: number;
  }>('/auth/inbox', config);
}

export function updateInboxApi(action: InboxAction, id?: number) {
  return requestClient.put('/auth/inbox', { action, id });
}
