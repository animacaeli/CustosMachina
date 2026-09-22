import type { RouteRecordRaw } from 'vue-router';

const routes: RouteRecordRaw[] = [
  {
    path: '/home',
    name: 'Home',
    component: () => import('#/views/home/index.vue'),
    meta: {
      icon: 'lucide:home',
      order: -1,
      title: '首页',
    },
  },
];

export default routes;
