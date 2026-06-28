// 路由配置。
//
// 当前只有一个迁移页（首页）。后续可拆分：
//   /            迁移配置页（左右两栏表单 + 迁移模式选择）
//   /progress    迁移进度页（SSE 实时日志）
//   /reports     迁移报告/对账页
import { createRouter, createWebHistory, type RouteRecordRaw } from 'vue-router'

const routes: RouteRecordRaw[] = [
  {
    path: '/',
    name: 'migration',
    component: () => import('@/views/MigrationView.vue'),
    meta: { title: '数据迁移' },
  },
]

const router = createRouter({
  history: createWebHistory(),
  routes,
})

export default router
