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
          authority: ['admin'],
          title: '用户管理',
        },
      },
      {
        name: 'SystemSetting',
        path: 'setting',
        component: () => import('#/views/system/setting.vue'),
        meta: {
          icon: 'lucide:sliders-horizontal',
          authority: ['admin'],
          title: '系统设置',
        },
      },
      {
        name: 'SystemIM',
        path: 'im',
        component: () => import('#/views/system/im.vue'),
        meta: {
          icon: 'lucide:scan-line',
          authority: ['admin'],
          title: '登录配置',
        },
      },
      {
        name: 'SystemRole',
        path: 'role',
        component: () => import('#/views/system/role.vue'),
        meta: {
          icon: 'lucide:shield-check',
          authority: ['admin'],
          title: '角色权限',
        },
      },
    ],
  },
];

export default routes;
