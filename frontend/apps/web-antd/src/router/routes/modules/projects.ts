import type { RouteRecordRaw } from 'vue-router';

import { BasicLayout } from '#/layouts';

const routes: RouteRecordRaw[] = [
  {
    component: BasicLayout,
    meta: {
      icon: 'lucide:folder-git-2',
      order: 4,
      title: '项目管理',
    },
    name: 'Projects',
    path: '/projects',
    children: [
      {
        name: 'ProjectsList',
        path: '',
        component: () => import('#/views/projects/index.vue'),
        meta: {
          icon: 'lucide:folder-git-2',
          authority: ['superadmin', 'admin', 'ops', 'dev'],
          title: '项目列表',
        },
      },
      {
        name: 'ProjectDetail',
        path: ':id',
        component: () => import('#/views/projects/detail.vue'),
        meta: {
          hideInMenu: true,
          authority: ['superadmin', 'admin', 'ops', 'dev'],
          title: '项目详情',
        },
      },
    ],
  },
  {
    component: BasicLayout,
    meta: {
      icon: 'lucide:layers',
      order: 6,
      title: '环境管理',
    },
    name: 'Envs',
    path: '/envs',
    children: [
      {
        name: 'EnvsIndex',
        path: '',
        component: () => import('#/views/envs/index.vue'),
        meta: {
          icon: 'lucide:layers',
          authority: ['superadmin', 'admin', 'ops', 'dev'],
          title: '环境总览',
        },
      },
    ],
  },
];

export default routes;
