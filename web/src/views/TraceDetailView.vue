<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import Button from 'primevue/button'
import Drawer from 'primevue/drawer'
import FloatLabel from 'primevue/floatlabel'
import InputText from 'primevue/inputtext'
import Message from 'primevue/message'
import type { components } from '@/api/generated/management'
import EventArtifactsPanel from '@/components/EventArtifactsPanel.vue'
import JsonPanel from '@/components/JsonPanel.vue'
import { getEventById, getTrace } from '@/api/management'
import { formatDateTime, formatNumber } from '@/utils/format'

const route = useRoute()
const router = useRouter()

type EventItem = components['schemas']['Event']
type TraceView = components['schemas']['TraceView']

const traceView = ref<TraceView | null>(null)
const isLoading = ref(false)
const errorMessage = ref<string | null>(null)
const limitInput = ref(typeof route.query.limit === 'string' ? route.query.limit : '100')
const selectedEventId = ref<number | null>(null)
const selectedEvent = ref<EventItem | null>(null)
const isDrawerOpen = ref(false)
const isLoadingEventDetail = ref(false)
const eventDetailError = ref<string | null>(null)

const traceId = computed(() => String(route.params.traceId ?? ''))

function parsedLimit() {
  const parsed = Number(limitInput.value)
  return Number.isFinite(parsed) && parsed > 0 ? parsed : undefined
}

async function loadTrace() {
  if (!traceId.value) {
    return
  }

  isLoading.value = true
  errorMessage.value = null

  try {
    traceView.value = await getTrace(traceId.value, {
      limit: parsedLimit(),
    })
  } catch (error) {
    traceView.value = null
    errorMessage.value = error instanceof Error ? error.message : 'Unable to load trace.'
  } finally {
    isLoading.value = false
  }
}

async function applyLimit() {
  await router.replace({
    query: parsedLimit() ? { limit: String(parsedLimit()) } : {},
  })
}

async function loadEventDetail(eventId: number) {
  selectedEventId.value = eventId
  selectedEvent.value = null
  eventDetailError.value = null
  isDrawerOpen.value = true
  isLoadingEventDetail.value = true

  try {
    selectedEvent.value = await getEventById(eventId)
  } catch (error) {
    eventDetailError.value =
      error instanceof Error ? error.message : 'Unable to load event detail.'
  } finally {
    isLoadingEventDetail.value = false
  }
}

watch(
  () => route.query.limit,
  (nextLimit) => {
    limitInput.value = typeof nextLimit === 'string' ? nextLimit : '100'
  },
)

watch(
  () => route.fullPath,
  async () => {
    await loadTrace()
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
            <p class="text-sm uppercase tracking-[0.3em] text-slate-400">Trace Detail</p>
            <h1 class="mt-3 text-3xl font-semibold">{{ traceId }}</h1>
            <p class="mt-4 max-w-2xl text-slate-600">
              Follow the ordered event sequence for a single trace and open any item for full payload inspection.
            </p>
          </div>

          <form class="flex flex-wrap items-end gap-3" @submit.prevent="applyLimit">
            <div>
              <FloatLabel variant="on">
                <InputText id="trace-limit-inline" v-model="limitInput" placeholder="100" />
                <label for="trace-limit-inline">Limit</label>
              </FloatLabel>
            </div>
            <Button type="submit" label="Refresh trace" :loading="isLoading" />
          </form>
        </div>
      </section>

      <Message v-if="errorMessage" severity="error" :closable="false">
        {{ errorMessage }}
      </Message>

      <section
        v-if="isLoading && !traceView"
        class="rounded-3xl border border-slate-200 bg-white/80 p-8 text-slate-500"
      >
        Loading trace...
      </section>

      <section
        v-else-if="traceView && !(traceView.Items?.length)"
        class="rounded-3xl border border-dashed border-slate-300 bg-white/60 px-6 py-10 text-center text-slate-500"
      >
        No events were returned for this trace.
      </section>

      <template v-else-if="traceView">
        <section class="grid gap-4 md:grid-cols-2">
          <article class="rounded-2xl border border-slate-200 bg-white p-5 shadow-sm shadow-slate-200/80">
            <p class="text-sm uppercase tracking-[0.2em] text-slate-500">Trace ID</p>
            <p class="mt-4 text-lg font-semibold text-sky-300">{{ traceView.TraceID }}</p>
          </article>
          <article class="rounded-2xl border border-slate-200 bg-white p-5 shadow-sm shadow-slate-200/80">
            <p class="text-sm uppercase tracking-[0.2em] text-slate-500">Events returned</p>
            <p class="mt-4 text-3xl font-semibold text-emerald-300">{{ formatNumber(traceView.Items?.length) }}</p>
          </article>
        </section>

        <section class="space-y-3">
          <article
            v-for="event in traceView.Items"
            :key="event.ID"
            class="rounded-3xl border border-slate-200 bg-white/85 p-5 shadow-sm shadow-slate-200/70 transition hover:border-sky-300 hover:bg-white"
          >
            <div class="flex flex-col gap-4 lg:flex-row lg:items-start lg:justify-between">
              <div class="space-y-3">
                <div class="flex flex-wrap items-center gap-2 text-xs uppercase tracking-[0.22em] text-slate-500">
                  <span>#{{ formatNumber(event.ID) }}</span>
                  <span>{{ formatDateTime(event.OccurredAt) }}</span>
                  <span>{{ event.Level }}</span>
                </div>
                <div class="flex flex-wrap gap-2">
                  <span class="rounded-full border border-slate-200 bg-slate-50 px-3 py-1 text-xs text-slate-700">
                    {{ event.Category }}
                  </span>
                  <span class="rounded-full border border-slate-200 bg-slate-50 px-3 py-1 text-xs text-slate-700">
                    {{ event.Kind }}
                  </span>
                  <span class="rounded-full border border-slate-200 bg-slate-50 px-3 py-1 text-xs text-slate-700">
                    {{ event.Module }}
                  </span>
                </div>
                <div>
                  <h2 class="text-lg font-semibold text-slate-900">{{ event.Subject }}</h2>
                  <p class="mt-2 text-sm text-slate-600">{{ event.Component }}</p>
                </div>
              </div>

              <div class="flex shrink-0 flex-wrap gap-3">
                <Button size="small" label="View detail" @click="loadEventDetail(event.ID)" />
                <RouterLink
                  v-for="artifactId in event.ArtifactIDs ?? []"
                  :key="artifactId"
                  :to="`/artifacts/${artifactId}`"
                  class="inline-flex items-center rounded-full border border-slate-200 bg-slate-50 px-4 py-2 text-sm text-slate-700 transition hover:border-sky-300 hover:text-sky-700"
                >
                  {{ artifactId }}
                </RouterLink>
              </div>
            </div>
          </article>
        </section>
      </template>
    </div>

    <Drawer v-model:visible="isDrawerOpen" position="right" class="w-full max-w-3xl bg-slate-50 text-slate-900">
      <template #header>
        <div>
          <p class="text-xs uppercase tracking-[0.25em] text-slate-500">Event Detail</p>
          <p class="mt-2 text-xl font-semibold">#{{ selectedEventId ?? '...' }}</p>
        </div>
      </template>

      <div v-if="isLoadingEventDetail" class="text-slate-500">
        Loading event detail...
      </div>
      <Message v-else-if="eventDetailError" severity="error" :closable="false">
        {{ eventDetailError }}
      </Message>
      <div v-else-if="selectedEvent" class="space-y-6">
        <section class="grid gap-4 md:grid-cols-2">
          <div class="rounded-2xl border border-slate-200 bg-white p-4">
            <p class="text-xs uppercase tracking-[0.2em] text-slate-500">Occurred</p>
            <p class="mt-3 text-sm text-slate-700">{{ formatDateTime(selectedEvent.OccurredAt) }}</p>
          </div>
          <div class="rounded-2xl border border-slate-200 bg-white p-4">
            <p class="text-xs uppercase tracking-[0.2em] text-slate-500">Trace</p>
            <p class="mt-3 text-sm text-slate-700">{{ selectedEvent.TraceID }}</p>
          </div>
        </section>

        <section class="rounded-2xl border border-slate-200 bg-white p-4">
          <p class="text-xs uppercase tracking-[0.2em] text-slate-500">Payload</p>
          <div class="mt-4">
            <JsonPanel :value="selectedEvent.Payload" />
          </div>
        </section>

        <EventArtifactsPanel :artifact-ids="selectedEvent.ArtifactIDs" />
      </div>
    </Drawer>
  </main>
</template>
