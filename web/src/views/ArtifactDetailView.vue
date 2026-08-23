<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import Button from 'primevue/button'
import Message from 'primevue/message'
import { useRoute } from 'vue-router'
import type { components } from '@/api/generated/management'
import { getArtifact } from '@/api/management'
import ArtifactContentPanel from '@/components/ArtifactContentPanel.vue'
import { formatDateTime, formatNumber } from '@/utils/format'

const route = useRoute()

type Artifact = components['schemas']['Artifact']

const artifact = ref<Artifact | null>(null)
const isLoading = ref(false)
const errorMessage = ref<string | null>(null)
const copyState = ref<'idle' | 'copied' | 'failed'>('idle')

const artifactId = computed(() => String(route.params.artifactId ?? ''))

async function loadArtifact() {
  if (!artifactId.value) {
    return
  }

  isLoading.value = true
  errorMessage.value = null
  copyState.value = 'idle'

  try {
    artifact.value = await getArtifact(artifactId.value)
  } catch (error) {
    artifact.value = null
    errorMessage.value = error instanceof Error ? error.message : 'Unable to load artifact.'
  } finally {
    isLoading.value = false
  }
}

async function copyContent() {
  if (!artifact.value) {
    return
  }

  try {
    await navigator.clipboard.writeText(artifact.value.Content)
    copyState.value = 'copied'
  } catch {
    copyState.value = 'failed'
  }
}

watch(
  () => route.params.artifactId,
  async () => {
    await loadArtifact()
  },
  { immediate: true },
)
</script>

<template>
  <main class="px-6 py-8 text-slate-900 lg:px-10 lg:py-10">
    <div class="mx-auto max-w-6xl space-y-6">
      <section class="rounded-3xl border border-slate-200 bg-white/80 p-8 shadow-xl shadow-slate-200/70">
        <div class="flex flex-col gap-5 lg:flex-row lg:items-end lg:justify-between">
          <div>
            <p class="text-sm uppercase tracking-[0.3em] text-slate-400">Artifact Detail</p>
            <h1 class="mt-3 text-3xl font-semibold">{{ artifactId }}</h1>
            <p class="mt-4 max-w-2xl text-slate-600">
              Inspect the original artifact payload referenced by one or more management events.
            </p>
          </div>

          <div class="flex flex-wrap gap-3">
            <Button label="Refresh" icon="pi pi-refresh" :loading="isLoading" @click="loadArtifact" />
            <Button
              label="Copy content"
              severity="secondary"
              outlined
              :disabled="!artifact"
              @click="copyContent"
            />
          </div>
        </div>
      </section>

      <Message v-if="errorMessage" severity="error" :closable="false">
        {{ errorMessage }}
      </Message>
      <Message v-else-if="copyState === 'copied'" severity="success" :closable="false">
        Artifact content copied to clipboard.
      </Message>
      <Message v-else-if="copyState === 'failed'" severity="warn" :closable="false">
        Clipboard write failed in this browser context.
      </Message>

      <section
        v-if="isLoading && !artifact"
        class="rounded-3xl border border-slate-200 bg-white/80 p-8 text-slate-500"
      >
        Loading artifact...
      </section>

      <template v-else-if="artifact">
        <section class="grid gap-4 md:grid-cols-2 xl:grid-cols-4">
          <article class="rounded-2xl border border-slate-200 bg-white p-5 shadow-sm shadow-slate-200/80">
            <p class="text-sm uppercase tracking-[0.2em] text-slate-500">Artifact ID</p>
            <p class="mt-4 text-lg font-semibold text-sky-300">{{ artifact.ID }}</p>
          </article>
          <article class="rounded-2xl border border-slate-200 bg-white p-5 shadow-sm shadow-slate-200/80">
            <p class="text-sm uppercase tracking-[0.2em] text-slate-500">Event ID</p>
            <p class="mt-4 text-3xl font-semibold text-emerald-300">{{ formatNumber(artifact.EventID) }}</p>
          </article>
          <article class="rounded-2xl border border-slate-200 bg-white p-5 shadow-sm shadow-slate-200/80">
            <p class="text-sm uppercase tracking-[0.2em] text-slate-500">Kind</p>
            <p class="mt-4 text-xl font-semibold text-amber-300">{{ artifact.Kind }}</p>
          </article>
          <article class="rounded-2xl border border-slate-200 bg-white p-5 shadow-sm shadow-slate-200/80">
            <p class="text-sm uppercase tracking-[0.2em] text-slate-500">Created at</p>
            <p class="mt-4 text-sm font-semibold text-violet-300">{{ formatDateTime(artifact.CreatedAt) }}</p>
          </article>
        </section>

        <section class="rounded-3xl border border-slate-200 bg-white/85 p-6 shadow-sm shadow-slate-200/80">
          <p class="text-sm uppercase tracking-[0.2em] text-slate-500">Content</p>
          <ArtifactContentPanel :content="artifact.Content" />
        </section>
      </template>

      <section
        v-else
        class="rounded-3xl border border-dashed border-slate-300 bg-white/60 px-6 py-10 text-center text-slate-500"
      >
        Artifact data is unavailable.
      </section>
    </div>
  </main>
</template>
