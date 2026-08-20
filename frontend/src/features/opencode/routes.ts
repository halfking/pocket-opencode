// OpenCode 路由配置
// 将这些路由添加到你的 frontend/src/app/router-mobile.ts 或 router.ts 中

export const opencodeRoutes = [
  {
    path: '/opencode/hub',
    name: 'OpenCodeHub',
    component: () => import('@/features/opencode/OpenCodeHub.vue'),
    meta: {
      title: 'OpenCode 实例中心',
      requiresAuth: true,
      keepAlive: true
    }
  },
  {
    path: '/opencode/sessions',
    name: 'OpenCodeSessions',
    component: () => import('@/features/opencode/SessionListView.vue'),
    meta: {
      title: '会话列表',
      requiresAuth: true,
      keepAlive: true
    }
  },
  {
    path: '/opencode/sessions/:id',
    name: 'SessionDetail',
    component: () => import('@/features/opencode/SessionDetailView.vue'),
    meta: {
      title: '会话详情',
      requiresAuth: true,
      keepAlive: false // 详情页不缓存，每次进入都重新加载
    }
  }
]

// 使用示例：
// import { opencodeRoutes } from './opencode-routes'
// 
// const routes = [
//   ...existingRoutes,
//   ...opencodeRoutes
// ]
