<script setup lang="ts">
import { computed, reactive } from 'vue'
import { useRouter } from 'vue-router'
import Button from 'primevue/button'
import FloatLabel from 'primevue/floatlabel'
import InputText from 'primevue/inputtext'

const router = useRouter()

const form = reactive({
  traceId: '',
  limit: '100',
})

const canSubmit = computed(() => form.traceId.trim().length > 0)

async function submit() {
  const traceId = form.traceId.trim()
  if (!traceId) {
    return
  }

  await router.push({
    path: `/traces/${traceId}`,
    query: form.limit.trim() ? { limit: form.limit.trim() } : {},
  })
}
</script>

<template>
  <main class="px-6 py-8 text-slate-900 lg:px-10 lg:py-10">
    <div class="mx-auto max-w-4xl rounded-3xl border border-slate-200 bg-white/80 p-8 shadow-xl shadow-slate-200/70">
      <p class="text-sm uppercase tracking-[0.3em] text-slate-400">Traces</p>
      <h1 class="mt-3 text-3xl font-semibold">Trace lookup</h1>
      <p class="mt-4 max-w-2xl text-slate-600">
        Enter a trace ID to inspect the ordered event timeline exposed by the Management API.
      </p>

      <form class="mt-8 grid gap-4 md:grid-cols-[minmax(0,1fr)_12rem_auto]" @submit.prevent="submit">
        <div>
          <FloatLabel variant="on">
            <InputText id="trace-id" v-model="form.traceId" placeholder="trace-..." />
            <label for="trace-id">Trace ID</label>
          </FloatLabel>
        </div>
        <div>
          <FloatLabel variant="on">
            <InputText id="trace-limit" v-model="form.limit" placeholder="100" />
            <label for="trace-limit">Limit</label>
          </FloatLabel>
        </div>
        <div class="flex items-end">
          <Button class="w-full" type="submit" label="Open trace" :disabled="!canSubmit" />
        </div>
      </form>
    </div>
  </main>
</template>
