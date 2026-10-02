import type { RouteRecordRaw } from 'vue-router';

import { BasicLayout } from '#/layouts';

const routes: RouteRecordRaw[] = [
  {
    component: BasicLayout,
    meta: {
      icon: 'lucide:calendar-clock',
      order: 6,
      authority: ['superadmin', 'admin', 'ops', 'dev'],
      title: '定时任务',
    },
    name: 'Cron',
    path: '/cron',
    children: [
      {
        name: 'CronJobs',
        path: 'jobs',
        component: () => import('#/views/cron/jobs.vue'),
        meta: {
          icon: 'lucide:list-todo',
          authority: ['superadmin', 'admin', 'ops', 'dev'],
          title: '任务',
        },
      },
      {
        name: 'CronScripts',
        path: 'scripts',
        component: () => import('#/views/cron/scripts.vue'),
        meta: {
          icon: 'lucide:scroll-text',
          authority: ['superadmin', 'admin', 'ops', 'dev'],
          title: '脚本库',
        },
      },
    ],
  },
];

export default routes;
