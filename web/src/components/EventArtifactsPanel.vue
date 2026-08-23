<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import Message from 'primevue/message'
import type { components } from '@/api/generated/management'
import { getArtifact } from '@/api/management'
import ArtifactContentPanel from '@/components/ArtifactContentPanel.vue'
import { formatDateTime, formatNumber } from '@/utils/format'

type Artifact = components['schemas']['Artifact']

const props = defineProps<{
  artifactIds?: string[] | null
}>()

const artifacts = ref<Artifact[]>([])
const isLoading = ref(false)
const errorMessage = ref<string | null>(null)
let activeRequestId = 0

const normalizedArtifactIds = computed(() => {
  const ids = props.artifactIds ?? []
  return Array.from(new Set(ids.filter((id) => id.trim().length > 0)))
})

async function loadArtifacts() {
  const requestId = ++activeRequestId
  artifacts.value = []
  errorMessage.value = null

  if (!normalizedArtifactIds.value.length) {
    isLoading.value = false
    return
  }

  isLoading.value = true

  try {
    const results = await Promise.all(normalizedArtifactIds.value.map((id) => getArtifact(id)))

    if (requestId !== activeRequestId) {
      return
    }

    artifacts.value = results
  } catch (error) {
    if (requestId !== activeRequestId) {
      return
    }

    errorMessage.value =
      error instanceof Error ? error.message : 'Unable to load linked artifacts.'
  } finally {
    if (requestId === activeRequestId) {
      isLoading.value = false
    }
  }
}

watch(normalizedArtifactIds, loadArtifacts, { immediate: true })
</script>

<template>
  <section class="rounded-2xl border border-slate-200 bg-white p-4">
    <div class="flex items-center justify-between gap-3">
      <p class="text-xs uppercase tracking-[0.2em] text-slate-500">Artifacts</p>
      <p v-if="normalizedArtifactIds.length" class="text-xs text-slate-500">
        {{ formatNumber(normalizedArtifactIds.length) }} linked
      </p>
    </div>

    <p v-if="!normalizedArtifactIds.length" class="mt-4 text-sm text-slate-500">
      No artifacts linked to this event.
    </p>

    <div v-else-if="isLoading" class="mt-4 text-sm text-slate-500">
      Loading linked artifacts...
    </div>

    <Message v-else-if="errorMessage" class="mt-4" severity="error" :closable="false">
      {{ errorMessage }}
    </Message>

    <div v-else class="mt-4 space-y-4">
      <article
        v-for="artifact in artifacts"
        :key="artifact.ID"
        class="rounded-2xl border border-slate-200 bg-slate-50 p-4"
      >
        <div class="flex flex-wrap items-start justify-between gap-3">
          <div class="space-y-2">
            <div class="flex flex-wrap gap-2">
              <span class="rounded-full border border-slate-200 bg-white px-3 py-1 text-xs text-slate-700">
                {{ artifact.Kind }}
              </span>
              <span class="rounded-full border border-slate-200 bg-white px-3 py-1 text-xs text-slate-700">
                Event #{{ formatNumber(artifact.EventID) }}
              </span>
            </div>
            <p class="text-sm font-semibold text-slate-900">{{ artifact.ID }}</p>
            <p class="text-xs uppercase tracking-[0.18em] text-slate-500">
              Created {{ formatDateTime(artifact.CreatedAt) }}
            </p>
          </div>

          <RouterLink
            :to="`/artifacts/${artifact.ID}`"
            class="inline-flex items-center rounded-full border border-slate-200 bg-white px-4 py-2 text-sm text-slate-700 transition hover:border-sky-300 hover:text-sky-700"
          >
            Open full page
          </RouterLink>
        </div>

        <ArtifactContentPanel :content="artifact.Content" />
      </article>
    </div>
  </section>
</template>
