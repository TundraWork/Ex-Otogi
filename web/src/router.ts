import { createRouter, createWebHistory, type RouteRecordRaw } from 'vue-router'

import { pinia } from '@/app/pinia'
import { useAuthStore } from '@/stores/auth'

export const primaryNavigation = [
  {
    label: 'Overview',
    to: '/overview',
    icon: 'pi pi-chart-bar',
  },
  {
    label: 'Events',
    to: '/events',
    icon: 'pi pi-bolt',
  },
  {
    label: 'Snapshots',
    to: '/snapshots',
    icon: 'pi pi-database',
  },
  {
    label: 'Trace Lookup',
    to: '/traces',
    icon: 'pi pi-search',
  },
] as const

const routes: RouteRecordRaw[] = [
  {
    path: '/login',
    component: () => import('@/views/LoginView.vue'),
    meta: {
      guestOnly: true,
    },
  },
  {
    path: '/',
    component: () => import('@/layouts/AppShellLayout.vue'),
    meta: {
      requiresAuth: true,
    },
    children: [
      {
        path: '',
        redirect: '/overview',
      },
      {
        path: 'overview',
        component: () => import('@/views/OverviewView.vue'),
      },
      {
        path: 'events',
        component: () => import('@/views/EventsView.vue'),
      },
      {
        path: 'snapshots',
        component: () => import('@/views/SnapshotsView.vue'),
      },
      {
        path: 'traces',
        component: () => import('@/views/TraceLookupView.vue'),
      },
      {
        path: 'traces/:traceId',
        component: () => import('@/views/TraceDetailView.vue'),
      },
      {
        path: 'artifacts/:artifactId',
        component: () => import('@/views/ArtifactDetailView.vue'),
      },
    ],
  },
]

export const router = createRouter({
  history: createWebHistory(),
  routes,
})

router.beforeEach((to) => {
  const authStore = useAuthStore(pinia)

  if (to.meta.requiresAuth && !authStore.isAuthenticated) {
    return {
      path: '/login',
      query: to.fullPath === '/overview' ? {} : { redirect: to.fullPath },
    }
  }

  if (to.meta.guestOnly && authStore.isAuthenticated) {
    return '/overview'
  }

  return true
})
