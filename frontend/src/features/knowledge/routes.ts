// Knowledge Base Routes Configuration
// 知识库路由配置

import { RouteRecordRaw } from 'vue-router';

export const knowledgeRoutes: RouteRecordRaw[] = [
  {
    path: '/knowledge',
    name: 'KnowledgeHub',
    component: () => import('@/pages/KnowledgeHubPage.vue'),
    meta: {
      requiresAuth: true,
      title: '知识中心',
    },
  },
  {
    path: '/knowledge/search',
    name: 'KnowledgeSearch',
    component: () => import('@/pages/KnowledgeSearchPage.vue'),
    meta: {
      requiresAuth: true,
      title: '搜索',
    },
  },
  {
    path: '/knowledge/notebooks/:id',
    name: 'NotebookDetail',
    component: () => import('@/pages/NotebookDetailPage.vue'),
    meta: {
      requiresAuth: true,
      title: '笔记本',
    },
  },
  {
    path: '/knowledge/notes/:id',
    name: 'NoteDetail',
    component: () => import('@/pages/NoteDetailPage.vue'),
    meta: {
      requiresAuth: true,
      title: '笔记详情',
    },
  },
  {
    path: '/knowledge/notes/:id/edit',
    name: 'NoteEdit',
    component: () => import('@/pages/NoteEditPage.vue'),
    meta: {
      requiresAuth: true,
      title: '编辑笔记',
    },
  },
];
