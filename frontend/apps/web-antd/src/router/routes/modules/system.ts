import type { RouteRecordRaw } from 'vue-router';

import { BasicLayout } from '#/layouts';

const routes: RouteRecordRaw[] = [
  {
    component: BasicLayout,
    meta: {
      icon: 'lucide:users',
      order: 10,
      // authority 必须覆盖子路由并集：filterTree 对父级放行后空目录仍会显示
      authority: ['superadmin', 'admin'],
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
      {
        name: 'SystemCerts',
        path: 'certs',
        component: () => import('#/views/system/certs.vue'),
        meta: {
          icon: 'lucide:shield',
          authority: ['superadmin', 'admin', 'ops'],
          title: '证书续期',
        },
      },
      {
        name: 'SystemBackup',
        path: 'backup',
        component: () => import('#/views/system/backup.vue'),
        meta: {
          icon: 'lucide:archive',
          // admin 管理；ops 只读+手动触发（对齐 rbac v9）
          authority: ['superadmin', 'admin', 'ops'],
          title: '备份恢复',
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
          // 生效点必须在子路由：tabbar addTab 取最后一级 matched 记录
          // 自身的 meta（use-tabbar.ts），父级声明对它不可见
          fullPathKey: false,
        },
      },
    ],
  },
];

export default routes;
