import type { RouteRecordRaw } from 'vue-router';

const routes: RouteRecordRaw[] = [
  {
    meta: {
      icon: 'lucide:layout-dashboard',
      order: -1,
      title: '工作台',
    },
    name: 'Dashboard',
    path: '/dashboard',
    children: [
      {
        name: 'DashboardHome',
        path: 'home',
        component: () => import('#/views/dashboard/index.vue'),
        meta: {
          affixTab: true,
          icon: 'lucide:home',
          title: '概览',
        },
      },
    ],
  },
];

export default routes;
