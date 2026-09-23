import type { RouteRecordRaw } from 'vue-router';

import { BasicLayout } from '#/layouts';

const routes: RouteRecordRaw[] = [
  {
    component: BasicLayout,
    meta: {
      icon: 'lucide:server',
      order: 5,
      title: '资源管理',
    },
    name: 'Resources',
    path: '/resources',
    children: [
      {
        name: 'ResourcesServer',
        path: 'servers',
        component: () => import('#/views/resources/server.vue'),
        meta: {
          icon: 'lucide:server-cog',
          authority: ['superadmin', 'admin', 'ops', 'dev'],
          title: '服务器',
        },
      },
      {
        name: 'ResourcesGroup',
        path: 'groups',
        component: () => import('#/views/resources/group.vue'),
        meta: {
          icon: 'lucide:folder-tree',
          authority: ['superadmin', 'admin', 'ops'],
          title: '服务器分组',
        },
      },
    ],
  },
];

export default routes;
