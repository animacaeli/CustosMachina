import type { RouteRecordRaw } from 'vue-router';

import { BasicLayout } from '#/layouts';

// 配置文件管理（P5 M4）：文件级配置真相源，独立模块（不挂资源管理——
// 参考三阶段 projects 独立先例），仅复用资源管理的 SSH/SFTP 通道。
const routes: RouteRecordRaw[] = [
  {
    component: BasicLayout,
    meta: {
      // authority 必须覆盖子路由并集：filterTree 对父级放行后空目录仍会显示
      authority: ['superadmin', 'admin', 'ops', 'dev'],
      icon: 'lucide:file-cog',
      order: 6,
      title: '配置管理',
    },
    name: 'Configs',
    path: '/configs',
    children: [
      {
        name: 'ConfigsFiles',
        path: 'files',
        component: () => import('#/views/configs/files.vue'),
        meta: {
          icon: 'lucide:file-code-2',
          authority: ['superadmin', 'admin', 'ops', 'dev'],
          title: '配置文件',
        },
      },
    ],
  },
];

export default routes;
