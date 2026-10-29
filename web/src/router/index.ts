import { createRouter, createWebHistory, type RouteRecordRaw } from 'vue-router'

import { useAuthStore } from '@/stores/auth'
import { safeRedirect } from '@/utils/display'

const routes: RouteRecordRaw[] = [
  {
    path: '/login',
    name: 'login',
    component: () => import('@/views/LoginView.vue'),
    meta: { title: '登录', layout: 'blank', public: true },
  },
  {
    path: '/',
    name: 'home',
    component: () => import('@/views/HomeView.vue'),
    meta: { title: '概览' },
  },
  {
    path: '/instances',
    name: 'instances',
    component: () => import('@/views/InstancesView.vue'),
    meta: { title: '实例' },
  },
  {
    path: '/runtimes',
    name: 'runtimes',
    component: () => import('@/views/RuntimesView.vue'),
    meta: { title: '运行时' },
  },
  {
    path: '/plugins',
    name: 'plugins',
    component: () => import('@/views/PluginsView.vue'),
    meta: { title: '插件' },
  },
  {
    path: '/packs',
    name: 'packs',
    component: () => import('@/views/PacksView.vue'),
    meta: { title: '整合包' },
  },
  {
    path: '/logs',
    name: 'logs',
    component: () => import('@/views/LogsView.vue'),
    meta: { title: '活动日志' },
  },
  {
    path: '/settings',
    name: 'settings',
    component: () => import('@/views/SettingsView.vue'),
    meta: { title: '设置' },
  },
  {
    path: '/:pathMatch(.*)*',
    redirect: { name: 'home' },
  },
]

const router = createRouter({
  history: createWebHistory(),
  routes,
  scrollBehavior: () => ({ left: 0, top: 0 }),
})

router.beforeEach(async (to) => {
  const auth = useAuthStore()

  if (to.meta.public) {
    // Visiting /login while a session is active goes straight to the app.
    if (to.name === 'login' && (await auth.ensureSession())) {
      return safeRedirect(to.query.redirect) ?? { name: 'home' }
    }
    return true
  }

  if (!(await auth.ensureSession())) {
    const query = to.fullPath && to.fullPath !== '/' ? { redirect: to.fullPath } : {}
    return { name: 'login', query }
  }
  return true
})

router.afterEach((to) => {
  const page = to.meta.title ?? '概览'
  document.title = page + ' · PCL-DSH-Go'
})

export default router
