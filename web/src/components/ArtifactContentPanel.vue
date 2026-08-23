<script setup lang="ts">
import { computed } from 'vue'
import JsonPanel from '@/components/JsonPanel.vue'

const props = defineProps<{
  content: string
}>()

const parsedContent = computed(() => {
  const rawContent = props.content ?? ''
  const trimmedContent = rawContent.trim()

  if (!trimmedContent) {
    return null
  }

  try {
    return JSON.parse(trimmedContent)
  } catch {
    return null
  }
})
</script>

<template>
  <JsonPanel v-if="parsedContent !== null" :value="parsedContent" />
  <pre
    v-else
    class="mt-4 overflow-auto whitespace-pre-wrap break-words rounded-2xl border border-slate-200 bg-white p-4 text-sm leading-6 text-slate-800"
  ><code>{{ content }}</code></pre>
</template>
