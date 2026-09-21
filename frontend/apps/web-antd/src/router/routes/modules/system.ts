import type { RouteRecordRaw } from 'vue-router';

import { BasicLayout } from '#/layouts';

const routes: RouteRecordRaw[] = [
  {
    component: BasicLayout,
    meta: {
      icon: 'lucide:settings',
      order: 10,
      title: '系统管理',
    },
    name: 'System',
    path: '/system',
    children: [
      {
        name: 'SystemUser',
        path: 'user',
        component: () => import('#/views/system/user.vue'),
        meta: {
          icon: 'lucide:users',
          roles: ['admin'],
          title: '用户管理',
        },
      },
      {
        name: 'SystemRole',
        path: 'role',
        component: () => import('#/views/system/role.vue'),
        meta: {
          icon: 'lucide:shield-check',
          roles: ['admin'],
          title: '角色权限',
        },
      },
    ],
  },
];

export default routes;
