<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import Button from 'primevue/button'
import Message from 'primevue/message'
import type { components } from '@/api/generated/management'
import { getOverview } from '@/api/management'

type Overview = components['schemas']['Overview']

const overview = ref<Overview | null>(null)
const isLoading = ref(false)
const errorMessage = ref<string | null>(null)

const summaryCards = computed(() => {
  if (!overview.value) {
    return []
  }

  return [
    {
      label: 'Window Start ID',
      value: overview.value.WindowStartID.toLocaleString(),
      tone: 'text-sky-300',
    },
    {
      label: 'Window End ID',
      value: overview.value.WindowEndID.toLocaleString(),
      tone: 'text-emerald-300',
    },
    {
      label: 'Total Events',
      value: overview.value.TotalEvents.toLocaleString(),
      tone: 'text-amber-300',
    },
    {
      label: 'Recent Errors',
      value: overview.value.RecentErrorCount.toLocaleString(),
      tone: 'text-rose-300',
    },
    {
      label: 'Recent Traces',
      value: overview.value.RecentTraceCount.toLocaleString(),
      tone: 'text-violet-300',
    },
  ]
})

const eventCountsByCategory = computed(() => {
  if (!overview.value) {
    return []
  }

  return Object.entries(overview.value.EventCountsByCategory).sort(([, left], [, right]) => right - left)
})

const activeInflightByComponent = computed(() => {
  if (!overview.value) {
    return []
  }

  return Object.entries(overview.value.ActiveInflightByComponent).sort(([, left], [, right]) => right - left)
})

const topCategories = computed(() => eventCountsByCategory.value.slice(0, 3))

async function loadOverview() {
  isLoading.value = true
  errorMessage.value = null

  try {
    overview.value = await getOverview()
  } catch (error) {
    errorMessage.value = error instanceof Error ? error.message : 'Unable to load overview.'
  } finally {
    isLoading.value = false
  }
}

onMounted(loadOverview)
</script>

<template>
  <main class="px-6 py-8 text-slate-900 lg:px-10 lg:py-10">
    <div class="mx-auto max-w-6xl space-y-6">
      <section class="rounded-3xl border border-slate-200 bg-white/80 p-8 shadow-xl shadow-slate-200/70">
        <div class="flex flex-col gap-5 lg:flex-row lg:items-end lg:justify-between">
          <div>
            <p class="text-sm uppercase tracking-[0.3em] text-slate-400">Overview</p>
            <h1 class="mt-3 text-3xl font-semibold">Management dashboard</h1>
            <p class="mt-4 max-w-2xl text-slate-600">
              Monitor the active event window, recent trace pressure, and category distribution from a single page.
            </p>
          </div>

          <div class="flex flex-wrap gap-3">
            <RouterLink
              v-for="entry in topCategories"
              :key="entry[0]"
              :to="{ path: '/events', query: { category: entry[0] } }"
              class="rounded-full border border-slate-200 bg-slate-50 px-4 py-2 text-sm text-slate-700 transition hover:bg-slate-100"
            >
              {{ entry[0] }} · {{ entry[1].toLocaleString() }}
            </RouterLink>
            <Button label="Refresh" icon="pi pi-refresh" :loading="isLoading" @click="loadOverview" />
          </div>
        </div>
      </section>

      <Message v-if="errorMessage" severity="error" :closable="false">
        <div class="flex items-center gap-3">
          <span>{{ errorMessage }}</span>
          <Button size="small" label="Retry" text @click="loadOverview" />
        </div>
      </Message>

      <section v-if="isLoading && !overview" class="grid gap-4 md:grid-cols-2 xl:grid-cols-5">
        <div
          v-for="index in 5"
          :key="index"
          class="h-32 animate-pulse rounded-3xl border border-slate-200 bg-white/80"
        />
      </section>

      <template v-else-if="overview">
        <section class="grid gap-4 md:grid-cols-2 xl:grid-cols-5">
          <article
            v-for="card in summaryCards"
            :key="card.label"
            class="rounded-3xl border border-slate-200 bg-white p-5 shadow-sm shadow-slate-200/80"
          >
            <p class="text-sm uppercase tracking-[0.2em] text-slate-500">{{ card.label }}</p>
            <p class="mt-5 text-3xl font-semibold" :class="card.tone">{{ card.value }}</p>
          </article>
        </section>

        <section class="grid gap-6 xl:grid-cols-2">
          <article class="rounded-3xl border border-slate-200 bg-white/85 p-6 shadow-sm shadow-slate-200/80">
            <div class="flex items-center justify-between gap-4">
              <div>
                <p class="text-sm uppercase tracking-[0.2em] text-slate-500">Event Counts</p>
                <h2 class="mt-2 text-xl font-semibold">By category</h2>
              </div>
              <RouterLink
                to="/events"
                class="text-sm font-medium text-sky-700 transition hover:text-sky-900"
              >
                Open events
              </RouterLink>
            </div>

            <div v-if="eventCountsByCategory.length" class="mt-6 space-y-3">
              <div
                v-for="[category, count] in eventCountsByCategory"
                :key="category"
                class="rounded-2xl border border-slate-200 bg-slate-50 px-4 py-4"
              >
                <div class="flex items-center justify-between gap-4">
                  <RouterLink
                    :to="{ path: '/events', query: { category } }"
                    class="font-medium text-slate-800 transition hover:text-sky-700"
                  >
                    {{ category }}
                  </RouterLink>
                  <span class="text-sm text-slate-500">{{ count.toLocaleString() }}</span>
                </div>
              </div>
            </div>
            <p v-else class="mt-6 text-sm text-slate-500">No category activity in the current window.</p>
          </article>

          <article class="rounded-3xl border border-slate-200 bg-white/85 p-6 shadow-sm shadow-slate-200/80">
            <div>
              <p class="text-sm uppercase tracking-[0.2em] text-slate-500">Active Inflight</p>
              <h2 class="mt-2 text-xl font-semibold">By component</h2>
            </div>

            <div v-if="activeInflightByComponent.length" class="mt-6 space-y-3">
              <div
                v-for="[component, count] in activeInflightByComponent"
                :key="component"
                class="rounded-2xl border border-slate-200 bg-slate-50 px-4 py-4"
              >
                <div class="flex items-center justify-between gap-4">
                  <span class="font-medium text-slate-800">{{ component }}</span>
                  <span class="text-sm text-slate-500">{{ count.toLocaleString() }}</span>
                </div>
              </div>
            </div>
            <p v-else class="mt-6 text-sm text-slate-500">No inflight activity is currently reported.</p>
          </article>
        </section>
      </template>

      <section
        v-else
        class="rounded-3xl border border-dashed border-slate-300 bg-white/60 px-6 py-10 text-center text-slate-500"
      >
        Overview data is unavailable.
      </section>
    </div>
  </main>
</template>
