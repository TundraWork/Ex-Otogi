<script setup lang="ts">
import { reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import Button from 'primevue/button'
import Drawer from 'primevue/drawer'
import FloatLabel from 'primevue/floatlabel'
import InputText from 'primevue/inputtext'
import Message from 'primevue/message'
import type { components } from '@/api/generated/management'
import JsonPanel from '@/components/JsonPanel.vue'
import { getSnapshots } from '@/api/management'
import { formatDateTime } from '@/utils/format'

type Snapshot = components['schemas']['Snapshot']
type SnapshotList = components['schemas']['SnapshotList']

const route = useRoute()
const router = useRouter()

const filterForm = reactive({
  namespace: '',
  key: '',
  module: '',
})

const snapshotList = ref<SnapshotList | null>(null)
const isLoading = ref(false)
const errorMessage = ref<string | null>(null)
const selectedSnapshot = ref<Snapshot | null>(null)
const isDrawerOpen = ref(false)

function updateFormFromRoute() {
  filterForm.namespace = typeof route.query.namespace === 'string' ? route.query.namespace : ''
  filterForm.key = typeof route.query.key === 'string' ? route.query.key : ''
  filterForm.module = typeof route.query.module === 'string' ? route.query.module : ''
}

async function loadSnapshots() {
  isLoading.value = true
  errorMessage.value = null

  try {
    snapshotList.value = await getSnapshots({
      namespace: filterForm.namespace.trim() || undefined,
      key: filterForm.key.trim() || undefined,
      module: filterForm.module.trim() || undefined,
    })
  } catch (error) {
    snapshotList.value = null
    errorMessage.value = error instanceof Error ? error.message : 'Unable to load snapshots.'
  } finally {
    isLoading.value = false
  }
}

async function applyFilters() {
  const query = Object.fromEntries(
    Object.entries({
      namespace: filterForm.namespace.trim() || undefined,
      key: filterForm.key.trim() || undefined,
      module: filterForm.module.trim() || undefined,
    }).filter(([, value]) => value !== undefined),
  )

  await router.replace({
    path: '/snapshots',
    query,
  })
}

async function clearFilters() {
  filterForm.namespace = ''
  filterForm.key = ''
  filterForm.module = ''
  await applyFilters()
}

function openPayload(snapshot: Snapshot) {
  selectedSnapshot.value = snapshot
  isDrawerOpen.value = true
}

watch(
  () => route.fullPath,
  async () => {
    updateFormFromRoute()
    await loadSnapshots()
  },
  { immediate: true },
)
</script>

<template>
  <main class="px-6 py-8 text-slate-900 lg:px-10 lg:py-10">
    <div class="mx-auto max-w-7xl space-y-6">
      <section class="rounded-3xl border border-slate-200 bg-white/80 p-8 shadow-xl shadow-slate-200/70">
        <div class="flex flex-col gap-5 lg:flex-row lg:items-end lg:justify-between">
          <div>
            <p class="text-sm uppercase tracking-[0.3em] text-slate-400">Snapshots</p>
            <h1 class="mt-3 text-3xl font-semibold">Snapshot explorer</h1>
            <p class="mt-4 max-w-2xl text-slate-600">
              Filter current snapshot state by namespace, key, or module and inspect full payload structure on demand.
            </p>
          </div>

          <Button label="Refresh" icon="pi pi-refresh" :loading="isLoading" @click="loadSnapshots" />
        </div>
      </section>

      <section class="rounded-3xl border border-slate-200 bg-white/80 p-6 shadow-sm shadow-slate-200/70">
        <form class="grid gap-4 md:grid-cols-4" @submit.prevent="applyFilters">
          <div>
            <FloatLabel variant="on">
              <InputText id="snapshot-namespace" v-model="filterForm.namespace" placeholder="session" />
              <label for="snapshot-namespace">Namespace</label>
            </FloatLabel>
          </div>
          <div>
            <FloatLabel variant="on">
              <InputText id="snapshot-key" v-model="filterForm.key" placeholder="conversation" />
              <label for="snapshot-key">Key</label>
            </FloatLabel>
          </div>
          <div>
            <FloatLabel variant="on">
              <InputText id="snapshot-module" v-model="filterForm.module" placeholder="telegram" />
              <label for="snapshot-module">Module</label>
            </FloatLabel>
          </div>
          <div class="flex items-end gap-3">
            <Button class="flex-1" type="submit" label="Apply filters" />
            <Button type="button" label="Clear" severity="secondary" outlined @click="clearFilters" />
          </div>
        </form>
      </section>

      <Message v-if="errorMessage" severity="error" :closable="false">
        {{ errorMessage }}
      </Message>

      <section
        v-if="isLoading && !snapshotList"
        class="rounded-3xl border border-slate-200 bg-white/80 p-8 text-slate-500"
      >
        Loading snapshots...
      </section>

      <section
        v-else-if="snapshotList && !(snapshotList.Items?.length)"
        class="rounded-3xl border border-dashed border-slate-300 bg-white/60 px-6 py-10 text-center text-slate-500"
      >
        No snapshots matched the current filters.
      </section>

      <section v-else-if="snapshotList" class="space-y-3">
        <article
          v-for="snapshot in snapshotList.Items"
          :key="`${snapshot.Namespace}:${snapshot.Key}:${snapshot.Module}`"
          class="rounded-3xl border border-slate-200 bg-white/85 p-5 shadow-sm shadow-slate-200/70 transition hover:border-sky-300 hover:bg-white"
        >
          <div class="flex flex-col gap-4 lg:flex-row lg:items-start lg:justify-between">
            <div class="space-y-3">
              <div class="flex flex-wrap gap-2">
                <span class="rounded-full border border-slate-200 bg-slate-50 px-3 py-1 text-xs text-slate-700">
                  {{ snapshot.Namespace }}
                </span>
                <span class="rounded-full border border-slate-200 bg-slate-50 px-3 py-1 text-xs text-slate-700">
                  {{ snapshot.Module }}
                </span>
              </div>
              <div>
                <h2 class="text-lg font-semibold text-slate-900">{{ snapshot.Key }}</h2>
                <p class="mt-2 max-w-3xl text-sm text-slate-600">{{ snapshot.Summary }}</p>
                <p class="mt-2 text-xs uppercase tracking-[0.22em] text-slate-500">
                  Updated {{ formatDateTime(snapshot.UpdatedAt) }}
                </p>
              </div>
            </div>

            <div class="flex shrink-0">
              <Button size="small" label="View payload" @click="openPayload(snapshot)" />
            </div>
          </div>
        </article>
      </section>
    </div>

    <Drawer v-model:visible="isDrawerOpen" position="right" class="w-full max-w-3xl bg-slate-50 text-slate-900">
      <template #header>
        <div v-if="selectedSnapshot">
          <p class="text-xs uppercase tracking-[0.25em] text-slate-500">Snapshot Payload</p>
          <p class="mt-2 text-xl font-semibold">{{ selectedSnapshot.Namespace }} / {{ selectedSnapshot.Key }}</p>
        </div>
      </template>

      <div v-if="selectedSnapshot" class="space-y-6">
        <section class="grid gap-4 md:grid-cols-2">
          <div class="rounded-2xl border border-slate-200 bg-white p-4">
            <p class="text-xs uppercase tracking-[0.2em] text-slate-500">Module</p>
            <p class="mt-3 text-sm text-slate-700">{{ selectedSnapshot.Module }}</p>
          </div>
          <div class="rounded-2xl border border-slate-200 bg-white p-4">
            <p class="text-xs uppercase tracking-[0.2em] text-slate-500">Updated at</p>
            <p class="mt-3 text-sm text-slate-700">{{ formatDateTime(selectedSnapshot.UpdatedAt) }}</p>
          </div>
        </section>

        <section class="rounded-2xl border border-slate-200 bg-white p-4">
          <p class="text-xs uppercase tracking-[0.2em] text-slate-500">Summary</p>
          <p class="mt-3 text-sm text-slate-700">{{ selectedSnapshot.Summary }}</p>
        </section>

        <section class="rounded-2xl border border-slate-200 bg-white p-4">
          <p class="text-xs uppercase tracking-[0.2em] text-slate-500">Payload</p>
          <div class="mt-4">
            <JsonPanel :value="selectedSnapshot.Payload" />
          </div>
        </section>
      </div>
    </Drawer>
  </main>
</template>
