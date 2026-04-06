<script setup lang="ts">
import { computed, ref } from 'vue'
import { RouterLink, RouterView, useRoute, useRouter } from 'vue-router'
import Button from 'primevue/button'
import Drawer from 'primevue/drawer'

import { useAuthStore } from '@/stores/auth'
import { primaryNavigation } from '@/router'

const authStore = useAuthStore()
const route = useRoute()
const router = useRouter()

const mobileNavOpen = ref(false)

const maskedToken = computed(() => {
  if (!authStore.token) {
    return 'No active Management API token'
  }

  const visibleStart = authStore.token.slice(0, 6)
  const visibleEnd = authStore.token.slice(-4)
  return `${visibleStart}...${visibleEnd}`
})

function isActive(path: string) {
  return route.path === path || route.path.startsWith(`${path}/`)
}

async function logout() {
  authStore.logout()
  mobileNavOpen.value = false
  await router.replace('/login')
}
</script>

<template>
  <div class="min-h-screen bg-slate-100 text-slate-900">
    <div class="grid min-h-screen lg:grid-cols-[18rem_minmax(0,1fr)]">
      <aside class="hidden border-r border-slate-200 bg-white lg:flex lg:flex-col">
        <div class="border-b border-slate-200 px-6 py-6">
          <p class="text-xs uppercase tracking-[0.35em] text-slate-500">Ex-Otogi</p>
          <h1 class="mt-3 text-xl font-semibold">Management Admin</h1>
          <p class="mt-3 text-sm text-slate-500">
            Management API token session active
          </p>
          <div class="mt-3 rounded-xl border border-slate-200 bg-slate-50 px-3 py-3 text-sm text-slate-700">
            {{ maskedToken }}
          </div>
        </div>

        <nav class="flex-1 px-4 py-5">
          <RouterLink
            v-for="item in primaryNavigation"
            :key="item.to"
            :to="item.to"
            class="mb-2 flex items-center gap-3 rounded-2xl px-4 py-3 transition"
            :class="isActive(item.to)
              ? 'bg-slate-900 text-white shadow-lg shadow-slate-200'
              : 'text-slate-600 hover:bg-slate-100 hover:text-slate-900'"
          >
            <i :class="[item.icon, 'text-base']" />
            <span class="font-medium">{{ item.label }}</span>
          </RouterLink>
        </nav>

        <div class="border-t border-slate-200 px-4 py-4">
          <Button class="w-full" severity="secondary" label="Log out" outlined @click="logout" />
        </div>
      </aside>

      <div class="flex min-h-screen flex-col">
        <header class="sticky top-0 z-20 border-b border-slate-200 bg-white/90 px-4 py-4 backdrop-blur lg:hidden">
          <div class="flex items-center justify-between gap-4">
            <div>
              <p class="text-xs uppercase tracking-[0.3em] text-slate-500">Ex-Otogi</p>
              <p class="mt-1 text-lg font-semibold">Management Admin</p>
            </div>
            <Button icon="pi pi-bars" text rounded severity="secondary" @click="mobileNavOpen = true" />
          </div>
        </header>

        <Drawer v-model:visible="mobileNavOpen" position="left" class="w-[18rem] bg-white text-slate-900">
          <template #header>
            <div>
              <p class="text-xs uppercase tracking-[0.3em] text-slate-500">Navigation</p>
              <p class="mt-1 text-lg font-semibold">Active Management API token</p>
            </div>
          </template>

          <div class="rounded-xl border border-slate-200 bg-slate-50 px-3 py-3 text-sm text-slate-700">
            {{ maskedToken }}
          </div>

          <nav class="mt-5">
            <RouterLink
              v-for="item in primaryNavigation"
              :key="item.to"
              :to="item.to"
              class="mb-2 flex items-center gap-3 rounded-2xl px-4 py-3 transition"
              :class="isActive(item.to)
                ? 'bg-slate-900 text-white shadow-lg shadow-slate-200'
                : 'text-slate-600 hover:bg-slate-100 hover:text-slate-900'"
              @click="mobileNavOpen = false"
            >
              <i :class="[item.icon, 'text-base']" />
              <span class="font-medium">{{ item.label }}</span>
            </RouterLink>
          </nav>

          <template #footer>
            <Button class="w-full" severity="secondary" label="Log out" outlined @click="logout" />
          </template>
        </Drawer>

        <main class="flex-1 bg-[radial-gradient(circle_at_top,_rgba(14,165,233,0.14),_transparent_30%),linear-gradient(180deg,_#f8fafc_0%,_#eef2ff_100%)]">
          <RouterView />
        </main>
      </div>
    </div>
  </div>
</template>
