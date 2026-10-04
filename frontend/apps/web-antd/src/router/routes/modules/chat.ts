import type { RouteRecordRaw } from 'vue-router';

import { BasicLayout } from '#/layouts';

// AI 对话（P6 M1，旗舰体验）：通用模式 + 平台上下文挂载（D2 拍板：独立一级菜单，
// 参考"配置管理"独立先例）。会话归属本人，挂载上下文经角色过滤与 DLP。
const routes: RouteRecordRaw[] = [
  {
    component: BasicLayout,
    meta: {
      authority: ['superadmin', 'admin', 'ops', 'dev'],
      icon: 'lucide:bot-message-square',
      order: 7,
      title: 'AI 对话',
    },
    name: 'Chat',
    path: '/chat',
    children: [
      {
        name: 'ChatMain',
        path: '',
        component: () => import('#/views/chat/chat-page.vue'),
        meta: {
          icon: 'lucide:message-circle',
          authority: ['superadmin', 'admin', 'ops', 'dev'],
          title: 'AI 对话',
          hideChildrenInMenu: true,
        },
      },
    ],
  },
];

export default routes;
