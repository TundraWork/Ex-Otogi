<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import Button from 'primevue/button'
import Drawer from 'primevue/drawer'
import FloatLabel from 'primevue/floatlabel'
import IconField from 'primevue/iconfield'
import InputIcon from 'primevue/inputicon'
import InputText from 'primevue/inputtext'
import Message from 'primevue/message'
import type { components } from '@/api/generated/management'
import EventArtifactsPanel from '@/components/EventArtifactsPanel.vue'
import JsonPanel from '@/components/JsonPanel.vue'
import { getEventById, getEvents } from '@/api/management'
import { formatDateTime, formatNumber } from '@/utils/format'

type EventItem = components['schemas']['Event']
type EventPage = components['schemas']['EventPage']

const route = useRoute()
const router = useRouter()

const filterForm = reactive({
  limit: '50',
  category: '',
  kind: '',
  trace_id: '',
  conversation_id: '',
  module: '',
  level: '',
})

const events = ref<EventItem[]>([])
const pageState = ref<EventPage | null>(null)
const isLoading = ref(false)
const isLoadingMore = ref(false)
const errorMessage = ref<string | null>(null)
const pollingEnabled = ref(true)
const pollingIntervalMs = ref(5000)
const cursorResetRequired = ref(false)
const selectedEventId = ref<number | null>(null)
const selectedEvent = ref<EventItem | null>(null)
const isEventDrawerOpen = ref(false)
const isLoadingEventDetail = ref(false)
const eventDetailError = ref<string | null>(null)

let pollTimer: number | null = null

const activeQuery = computed(() => buildQueryFromForm())

function buildQueryFromForm() {
  return {
    limit: normalizeNumberQuery(filterForm.limit),
    category: filterForm.category.trim() || undefined,
    kind: filterForm.kind.trim() || undefined,
    trace_id: filterForm.trace_id.trim() || undefined,
    conversation_id: filterForm.conversation_id.trim() || undefined,
    module: filterForm.module.trim() || undefined,
    level: filterForm.level.trim() || undefined,
  }
}

function normalizeNumberQuery(value: string) {
  if (!value.trim()) {
    return undefined
  }

  const parsed = Number(value)
  if (!Number.isFinite(parsed) || parsed <= 0) {
    return undefined
  }

  return parsed
}

function updateFormFromRoute() {
  filterForm.limit = typeof route.query.limit === 'string' ? route.query.limit : '50'
  filterForm.category = typeof route.query.category === 'string' ? route.query.category : ''
  filterForm.kind = typeof route.query.kind === 'string' ? route.query.kind : ''
  filterForm.trace_id = typeof route.query.trace_id === 'string' ? route.query.trace_id : ''
  filterForm.conversation_id =
    typeof route.query.conversation_id === 'string' ? route.query.conversation_id : ''
  filterForm.module = typeof route.query.module === 'string' ? route.query.module : ''
  filterForm.level = typeof route.query.level === 'string' ? route.query.level : ''
}

function mergeEvents(nextItems: EventItem[]) {
  const eventMap = new Map(events.value.map((item) => [item.ID, item]))
  for (const item of nextItems) {
    eventMap.set(item.ID, item)
  }

  events.value = Array.from(eventMap.values()).sort((left, right) => right.ID - left.ID)
}

async function fetchEvents(options?: { append?: boolean }) {
  const append = options?.append ?? false

  if (append) {
    if (!pageState.value?.LastID || isLoadingMore.value || cursorResetRequired.value) {
      return
    }
    isLoadingMore.value = true
  } else {
    isLoading.value = true
    cursorResetRequired.value = false
  }

  errorMessage.value = null

  try {
    const response = await getEvents({
      ...activeQuery.value,
      after_id: append ? pageState.value?.LastID : undefined,
    })

    if (response.CursorResetRequired) {
      cursorResetRequired.value = true
      return
    }

    pageState.value = response
    if (append) {
      mergeEvents(response.Items ?? [])
    } else {
      events.value = [...(response.Items ?? [])].sort((left, right) => right.ID - left.ID)
    }
  } catch (error) {
    errorMessage.value = error instanceof Error ? error.message : 'Unable to load events.'
  } finally {
    isLoading.value = false
    isLoadingMore.value = false
  }
}

async function reloadEvents() {
  selectedEventId.value = null
  selectedEvent.value = null
  isEventDrawerOpen.value = false
  await fetchEvents()
}

async function applyFilters() {
  const query = Object.fromEntries(
    Object.entries(buildQueryFromForm()).filter(([, value]) => value !== undefined),
  )

  await router.replace({
    path: '/events',
    query,
  })
}

async function clearField(field: keyof typeof filterForm) {
  filterForm[field] = field === 'limit' ? '50' : ''
  await applyFilters()
}

async function filterByTag(field: 'category' | 'kind' | 'module', value: string) {
  if (!value) return
  filterForm[field] = value
  await applyFilters()
}

async function clearFilters() {
  filterForm.limit = '50'
  filterForm.category = ''
  filterForm.kind = ''
  filterForm.trace_id = ''
  filterForm.conversation_id = ''
  filterForm.module = ''
  filterForm.level = ''
  await applyFilters()
}

async function loadEventDetail(eventId: number) {
  selectedEventId.value = eventId
  isEventDrawerOpen.value = true
  isLoadingEventDetail.value = true
  eventDetailError.value = null

  try {
    selectedEvent.value = await getEventById(eventId)
  } catch (error) {
    selectedEvent.value = null
    eventDetailError.value =
      error instanceof Error ? error.message : 'Unable to load event detail.'
  } finally {
    isLoadingEventDetail.value = false
  }
}

function schedulePolling() {
  if (pollTimer !== null) {
    window.clearInterval(pollTimer)
    pollTimer = null
  }

  if (!pollingEnabled.value) {
    return
  }

  pollTimer = window.setInterval(async () => {
    if (isLoading.value || isLoadingMore.value || cursorResetRequired.value) {
      return
    }

    await fetchEvents({ append: true })
  }, pollingIntervalMs.value)
}

watch(
  () => route.fullPath,
  async () => {
    updateFormFromRoute()
    await reloadEvents()
  },
  { immediate: true },
)

watch([pollingEnabled, pollingIntervalMs], schedulePolling, { immediate: true })

onMounted(updateFormFromRoute)

onBeforeUnmount(() => {
  if (pollTimer !== null) {
    window.clearInterval(pollTimer)
  }
})
</script>

<template>
  <main class="px-6 py-8 text-slate-900 lg:px-10 lg:py-10">
    <div class="mx-auto max-w-7xl space-y-6">
      <section class="rounded-3xl border border-slate-200 bg-white/80 p-8 shadow-xl shadow-slate-200/70">
        <div class="flex flex-col gap-5 lg:flex-row lg:items-end lg:justify-between">
          <div>
            <p class="text-sm uppercase tracking-[0.3em] text-slate-400">Events</p>
            <h1 class="mt-3 text-3xl font-semibold">Event explorer</h1>
            <p class="mt-4 max-w-2xl text-slate-600">
              Filter live event traffic, continue from the current cursor, and inspect payloads without leaving the workspace.
            </p>
          </div>

          <div class="flex flex-wrap items-center gap-3">
            <Button
              :label="pollingEnabled ? 'Polling on' : 'Polling off'"
              :severity="pollingEnabled ? 'contrast' : 'secondary'"
              :outlined="!pollingEnabled"
              @click="pollingEnabled = !pollingEnabled"
            />
            <Button label="Refresh" icon="pi pi-refresh" :loading="isLoading" @click="reloadEvents" />
          </div>
        </div>
      </section>

      <section class="rounded-3xl border border-slate-200 bg-white/80 p-6 shadow-sm shadow-slate-200/70">
        <form class="grid gap-3 md:grid-cols-2 xl:grid-cols-4" @submit.prevent="applyFilters">
          <FloatLabel variant="on">
            <IconField>
              <InputText fluid id="limit" v-model="filterForm.limit" placeholder="50" />
              <InputIcon v-if="filterForm.limit && filterForm.limit !== '50'" class="pi pi-times cursor-pointer" @click="clearField('limit')" />
              <InputIcon v-else class="pi" />
            </IconField>
            <label for="limit">Limit</label>
          </FloatLabel>
          <FloatLabel variant="on">
            <IconField>
              <InputText fluid id="category" v-model="filterForm.category" placeholder="message" />
              <InputIcon v-if="filterForm.category" class="pi pi-times cursor-pointer" @click="clearField('category')" />
              <InputIcon v-else class="pi" />
            </IconField>
            <label for="category">Category</label>
          </FloatLabel>
          <FloatLabel variant="on">
            <IconField>
              <InputText fluid id="kind" v-model="filterForm.kind" placeholder="dispatch" />
              <InputIcon v-if="filterForm.kind" class="pi pi-times cursor-pointer" @click="clearField('kind')" />
              <InputIcon v-else class="pi" />
            </IconField>
            <label for="kind">Kind</label>
          </FloatLabel>
          <FloatLabel variant="on">
            <IconField>
              <InputText fluid id="trace_id" v-model="filterForm.trace_id" placeholder="trace-..." />
              <InputIcon v-if="filterForm.trace_id" class="pi pi-times cursor-pointer" @click="clearField('trace_id')" />
              <InputIcon v-else class="pi" />
            </IconField>
            <label for="trace_id">Trace ID</label>
          </FloatLabel>
          <FloatLabel variant="on">
            <IconField>
              <InputText fluid id="conversation_id" v-model="filterForm.conversation_id" placeholder="conversation-..." />
              <InputIcon v-if="filterForm.conversation_id" class="pi pi-times cursor-pointer" @click="clearField('conversation_id')" />
              <InputIcon v-else class="pi" />
            </IconField>
            <label for="conversation_id">Conversation ID</label>
          </FloatLabel>
          <FloatLabel variant="on">
            <IconField>
              <InputText fluid id="module" v-model="filterForm.module" placeholder="telegram" />
              <InputIcon v-if="filterForm.module" class="pi pi-times cursor-pointer" @click="clearField('module')" />
              <InputIcon v-else class="pi" />
            </IconField>
            <label for="module">Module</label>
          </FloatLabel>
          <FloatLabel variant="on">
            <IconField>
              <InputText fluid id="level" v-model="filterForm.level" placeholder="info" />
              <InputIcon v-if="filterForm.level" class="pi pi-times cursor-pointer" @click="clearField('level')" />
              <InputIcon v-else class="pi" />
            </IconField>
            <label for="level">Level</label>
          </FloatLabel>
          <div class="flex items-end gap-3">
            <Button class="flex-1" type="submit" label="Apply filters" />
            <Button type="button" label="Clear" severity="secondary" outlined @click="clearFilters" />
          </div>
        </form>
      </section>

      <Message v-if="cursorResetRequired" severity="warn" :closable="false">
        The server requested a cursor reset. Reload the current filters to continue from a fresh window.
        <Button text label="Reset cursor" @click="reloadEvents" />
      </Message>

      <Message v-if="errorMessage" severity="error" :closable="false">
        {{ errorMessage }}
      </Message>

      <section class="grid gap-4 md:grid-cols-3">
        <article class="rounded-2xl border border-slate-200 bg-white p-5 shadow-sm shadow-slate-200/80">
          <p class="text-sm uppercase tracking-[0.2em] text-slate-500">Visible events</p>
          <p class="mt-4 text-3xl font-semibold text-sky-300">{{ formatNumber(events.length) }}</p>
        </article>
        <article class="rounded-2xl border border-slate-200 bg-white p-5 shadow-sm shadow-slate-200/80">
          <p class="text-sm uppercase tracking-[0.2em] text-slate-500">Last cursor</p>
          <p class="mt-4 text-3xl font-semibold text-emerald-300">{{ formatNumber(pageState?.LastID) }}</p>
        </article>
        <article class="rounded-2xl border border-slate-200 bg-white p-5 shadow-sm shadow-slate-200/80">
          <p class="text-sm uppercase tracking-[0.2em] text-slate-500">Window range</p>
          <p class="mt-4 text-xl font-semibold text-amber-300">
            {{ formatNumber(pageState?.WindowStartID) }} → {{ formatNumber(pageState?.WindowEndID) }}
          </p>
        </article>
      </section>

      <section
        v-if="isLoading && !events.length"
        class="rounded-3xl border border-slate-200 bg-white/80 p-8 text-slate-500"
      >
        Loading events...
      </section>

      <section
        v-else-if="!events.length"
        class="rounded-3xl border border-dashed border-slate-300 bg-white/60 px-6 py-10 text-center text-slate-500"
      >
        No events matched the current filters.
      </section>

      <section v-else class="space-y-3">
        <article
          v-for="event in events"
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
                  <button
                    class="rounded-full border border-slate-200 bg-slate-50 px-3 py-1 text-xs text-slate-700 transition hover:border-sky-300 hover:bg-sky-50 hover:text-sky-700 cursor-pointer"
                    @click="filterByTag('category', event.Category)"
                  >
                    {{ event.Category }}
                  </button>
                  <button
                    class="rounded-full border border-slate-200 bg-slate-50 px-3 py-1 text-xs text-slate-700 transition hover:border-sky-300 hover:bg-sky-50 hover:text-sky-700 cursor-pointer"
                    @click="filterByTag('kind', event.Kind)"
                  >
                    {{ event.Kind }}
                  </button>
                  <button
                    class="rounded-full border border-slate-200 bg-slate-50 px-3 py-1 text-xs text-slate-700 transition hover:border-sky-300 hover:bg-sky-50 hover:text-sky-700 cursor-pointer"
                    @click="filterByTag('module', event.Module)"
                  >
                    {{ event.Module }}
                  </button>
                  <span class="rounded-full border border-slate-200 bg-slate-50 px-3 py-1 text-xs text-slate-700">
                    {{ event.Component }}
                  </span>
                </div>

              <div>
                <h2 class="text-lg font-semibold text-slate-900">{{ event.Summary }}</h2>
                <p class="mt-2 text-sm text-slate-600">Trace {{ event.TraceID || 'N/A' }}</p>
              </div>
            </div>

            <div class="flex shrink-0 flex-wrap gap-3">
              <Button label="View detail" @click="loadEventDetail(event.ID)" />
              <RouterLink
                v-if="event.TraceID"
                :to="`/traces/${event.TraceID}`"
                class="inline-flex items-center rounded-full border border-slate-200 bg-slate-50 px-4 py-2 text-sm text-slate-700 transition hover:border-sky-300 hover:text-sky-700"
              >
                Open trace
              </RouterLink>
            </div>
          </div>
        </article>
      </section>

      <div class="flex justify-center">
        <Button
          v-if="pageState?.HasMore"
          label="Load more from cursor"
          severity="secondary"
          :loading="isLoadingMore"
          @click="fetchEvents({ append: true })"
        />
      </div>
    </div>

    <Drawer v-model:visible="isEventDrawerOpen" position="right" class="w-full max-w-3xl bg-slate-50 text-slate-900">
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
            <RouterLink
              :to="`/traces/${selectedEvent.TraceID}`"
              class="mt-3 block text-sm text-sky-700 transition hover:text-sky-900"
            >
              {{ selectedEvent.TraceID }}
            </RouterLink>
          </div>
          <div class="rounded-2xl border border-slate-200 bg-white p-4">
            <p class="text-xs uppercase tracking-[0.2em] text-slate-500">Conversation</p>
            <p class="mt-3 text-sm text-slate-700">{{ selectedEvent.ConversationID }}</p>
          </div>
          <div class="rounded-2xl border border-slate-200 bg-white p-4">
            <p class="text-xs uppercase tracking-[0.2em] text-slate-500">Actor</p>
            <p class="mt-3 text-sm text-slate-700">{{ selectedEvent.ActorID }}</p>
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
