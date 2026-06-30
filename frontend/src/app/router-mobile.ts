import { createRouter, createWebHashHistory } from 'vue-router'

// 登录页
import LoginView from '../features/auth/LoginView.vue'

// 服务器选择页
import ServerSelectView from '../features/servers/ServerSelectView.vue'

// OpenCode 实例列表页
import InstanceListView from '../features/instances/InstanceListView.vue'

// 任务列表页（按分组）
import TasksView from '../features/tasks/TasksView.vue'

// 任务详情页
import TaskDetailView from '../features/tasks/TaskDetailView.vue'

// 会话列表页
import SessionListView from '../features/sessions/SessionListView.vue'

// 设置页
import SettingsView from '../features/settings/SettingsView.vue'

const router = createRouter({
  history: createWebHashHistory(),
  routes: [
    {
      path: '/',
      redirect: '/login'
    },
    {
      path: '/login',
      name: 'login',
      component: LoginView
    },
    {
      path: '/servers',
      name: 'servers',
      component: ServerSelectView,
      meta: { requiresAuth: true }
    },
    {
      path: '/instances',
      name: 'instances',
      component: InstanceListView,
      meta: { requiresAuth: true }
    },
    {
      path: '/tasks',
      name: 'tasks',
      component: TasksView,
      meta: { requiresAuth: true }
    },
    {
      path: '/tasks/:id',
      name: 'task-detail',
      component: TaskDetailView,
      meta: { requiresAuth: true }
    },
    {
      path: '/sessions',
      name: 'sessions',
      component: SessionListView,
      meta: { requiresAuth: true }
    },
    {
      path: '/settings',
      name: 'settings',
      component: SettingsView,
      meta: { requiresAuth: true }
    }
  ]
})

// 路由守卫
router.beforeEach((to, from, next) => {
  const isAuthenticated = localStorage.getItem('pocket_user')
  
  if (to.meta.requiresAuth && !isAuthenticated) {
    next('/login')
  } else {
    next()
  }
})

export default router
