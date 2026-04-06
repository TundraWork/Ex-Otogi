<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import Button from 'primevue/button'
import Card from 'primevue/card'
import FloatLabel from 'primevue/floatlabel'
import Message from 'primevue/message'
import Password from 'primevue/password'

import { useAuthStore } from '@/stores/auth'

const authStore = useAuthStore()
const route = useRoute()
const router = useRouter()

const apiBaseUrl = import.meta.env.VITE_MANAGEMENT_API_BASE_URL || '/'
const tokenInput = ref(authStore.token ?? '')

const canSubmit = computed(() => tokenInput.value.trim().length > 0 && !authStore.isSubmitting)

async function submit() {
  if (!canSubmit.value) {
    return
  }

  const success = await authStore.login(tokenInput.value)
  if (!success) {
    return
  }

  const redirectTarget =
    typeof route.query.redirect === 'string' && route.query.redirect.startsWith('/')
      ? route.query.redirect
      : '/overview'

  await router.replace(redirectTarget)
}
</script>

<template>
  <main class="min-h-screen bg-[linear-gradient(180deg,_#f8fafc_0%,_#e2e8f0_100%)] text-slate-900">
    <div
      class="mx-auto flex min-h-screen w-full max-w-6xl items-center px-6 py-12 lg:grid lg:grid-cols-[1.15fr_0.85fr] lg:gap-10"
    >
      <section class="hidden lg:block">
        <p class="text-sm uppercase tracking-[0.3em] text-slate-500">
          Ex-Otogi Management
        </p>
        <h1 class="mt-4 max-w-xl text-5xl font-semibold leading-tight">
          Secure access to runtime traces, events, and snapshots.
        </h1>
        <p class="mt-6 max-w-xl text-lg leading-8 text-slate-600">
          This panel authenticates with a Management API token and validates it
          against the Management API before opening the admin workspace.
        </p>
      </section>

      <Card class="w-full border border-slate-200 bg-white/95 text-slate-950 shadow-2xl shadow-slate-300/40">
        <template #title>
          <div class="text-2xl font-semibold">Sign in with Management API token</div>
        </template>
        <template #content>
          <form class="space-y-5" @submit.prevent="submit">
            <div>
              <FloatLabel variant="on">
              <Password
                id="token"
                v-model="tokenInput"
                fluid
                input-class="w-full"
                :feedback="false"
                toggle-mask
                placeholder="Paste Management API token"
                @update:model-value="authStore.clearError()"
              />
                <label for="token">Management API token</label>
              </FloatLabel>
            </div>

            <Message v-if="authStore.lastError" severity="error" :closable="false">
              {{ authStore.lastError }}
            </Message>

            <div class="rounded-xl border border-slate-200 bg-slate-50 p-4 text-sm text-slate-600">
              API base URL: <span class="font-medium text-slate-900">{{ apiBaseUrl }}</span>
            </div>

            <Button
              class="w-full"
              type="submit"
              label="Validate token"
              :loading="authStore.isSubmitting"
              :disabled="!canSubmit"
            />
          </form>
        </template>
      </Card>
    </div>
  </main>
</template>
