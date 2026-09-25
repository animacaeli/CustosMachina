import type { RouteRecordRaw } from 'vue-router';

import { BasicLayout } from '#/layouts';

const routes: RouteRecordRaw[] = [
  {
    component: BasicLayout,
    meta: {
      icon: 'lucide:layers',
      order: 4,
      title: '环境管理',
    },
    name: 'Envs',
    path: '/envs',
    redirect: '/envs/prod',
    children: [
      {
        name: 'EnvsProd',
        path: 'prod',
        component: () => import('#/views/envs/env-page.vue'),
        meta: {
          icon: 'lucide:rocket',
          authority: ['superadmin', 'admin', 'ops', 'dev'],
          title: '正式环境',
        },
        props: { env: 'prod' },
      },
      {
        name: 'EnvsCanary',
        path: 'canary',
        component: () => import('#/views/envs/env-page.vue'),
        meta: {
          icon: 'lucide:git-branch',
          authority: ['superadmin', 'admin', 'ops', 'dev'],
          title: '灰度环境',
        },
        props: { env: 'canary' },
      },
      {
        name: 'EnvsTest',
        path: 'test',
        component: () => import('#/views/envs/env-page.vue'),
        meta: {
          icon: 'lucide:flask-conical',
          authority: ['superadmin', 'admin', 'ops', 'dev'],
          title: '测试环境',
        },
        props: { env: 'test' },
      },
    ],
  },
];

export default routes;
