import type { RouteRecordRaw } from 'vue-router';

import { BasicLayout } from '#/layouts';

const routes: RouteRecordRaw[] = [
  {
    component: BasicLayout,
    meta: {
      icon: 'lucide:users',
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
          icon: 'lucide:user-cog',
          authority: ['superadmin', 'admin'],
          title: '用户管理',
        },
      },
      {
        name: 'SystemRole',
        path: 'role',
        component: () => import('#/views/system/role.vue'),
        meta: {
          icon: 'lucide:shield-check',
          authority: ['superadmin', 'admin'],
          title: '角色权限',
        },
      },
    ],
  },
  // 管理后台：仅 admin，不进菜单，从头像下拉进入；平台级配置都在这里
  {
    component: BasicLayout,
    meta: {
      authority: ['superadmin', 'admin'],
      hideInMenu: true,
      title: '管理后台',
    },
    name: 'Admin',
    path: '/admin',
    children: [
      {
        name: 'AdminConsole',
        path: '',
        component: () => import('#/views/admin/index.vue'),
        meta: {
          authority: ['superadmin', 'admin'],
          hideInMenu: true,
          icon: 'lucide:wrench',
          title: '管理后台',
        },
      },
    ],
  },
];

export default routes;
