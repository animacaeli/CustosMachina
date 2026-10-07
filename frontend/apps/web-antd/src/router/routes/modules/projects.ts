import type { RouteRecordRaw } from 'vue-router';

import { BasicLayout } from '#/layouts';

const routes: RouteRecordRaw[] = [
  {
    component: BasicLayout,
    meta: {
      icon: 'lucide:folder-kanban',
      order: 3,
      authority: ['superadmin', 'admin', 'ops', 'dev'],
      title: '项目',
    },
    name: 'Projects',
    path: '/projects',
    children: [
      {
        name: 'ProjectDetail',
        path: ':id',
        component: () => import('#/views/projects/detail.vue'),
        meta: {
          hideInMenu: true,
          icon: 'lucide:folder-kanban',
          authority: ['superadmin', 'admin', 'ops', 'dev'],
          title: '项目详情',
        },
      },
      {
        name: 'ProjectsList',
        path: '',
        component: () => import('#/views/projects/index.vue'),
        meta: {
          icon: 'lucide:folder-kanban',
          authority: ['superadmin', 'admin', 'ops', 'dev'],
          title: '项目总览',
          // 可深链/可分享：项目作为核心实体的独立入口（v0.12.0 审核 UI 项）
        },
      },
    ],
  },
];

export default routes;
