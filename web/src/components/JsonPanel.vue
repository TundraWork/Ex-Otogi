<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import {
  Mode,
  createJSONEditor,
  type Content,
  type JsonEditor,
} from 'vanilla-jsoneditor'

const props = defineProps<{
  value: unknown
}>()

const containerRef = ref<HTMLDivElement | null>(null)
let editor: JsonEditor | null = null

const content = computed<Content>(() => ({
  json: props.value,
}))

function buildEditorProps() {
  return {
    content: content.value,
    mode: Mode.tree,
    readOnly: true,
    mainMenuBar: false,
    navigationBar: false,
    statusBar: false,
    askToFormat: false,
  }
}

onMounted(() => {
  if (!containerRef.value) {
    return
  }

  editor = createJSONEditor({
    target: containerRef.value,
    props: buildEditorProps(),
  })
})

watch(content, () => {
  editor?.updateProps(buildEditorProps())
})

onBeforeUnmount(() => {
  editor?.destroy()
  editor = null
})
</script>

<template>
  <div class="json-panel rounded-2xl border border-slate-200 bg-white">
    <div ref="containerRef" class="json-panel__editor" />
  </div>
</template>

<style scoped>
.json-panel {
  --jse-theme: light;
  --jse-theme-color: #0f172a;
  --jse-theme-color-highlight: #1e293b;
  --jse-background-color: #ffffff;
  --jse-text-color: #1e293b;
  --jse-text-color-inverse: #ffffff;
  --jse-main-border: 0;
  --jse-menu-color: #1e293b;
  --jse-modal-background: #ffffff;
  --jse-modal-overlay-background: rgba(15, 23, 42, 0.35);
  --jse-modal-code-background: #f8fafc;
  --jse-tooltip-color: #334155;
  --jse-tooltip-background: #ffffff;
  --jse-tooltip-border: 1px solid #cbd5e1;
  --jse-tooltip-action-button-color: #334155;
  --jse-tooltip-action-button-background: #e2e8f0;
  --jse-panel-background: #f8fafc;
  --jse-panel-background-border: 1px solid #e2e8f0;
  --jse-panel-color: #334155;
  --jse-panel-color-readonly: #64748b;
  --jse-panel-border: 1px solid #e2e8f0;
  --jse-panel-button-color-highlight: #0f172a;
  --jse-panel-button-background-highlight: #e2e8f0;
  --jse-navigation-bar-background: #f1f5f9;
  --jse-navigation-bar-background-highlight: #e2e8f0;
  --jse-navigation-bar-dropdown-color: #334155;
  --jse-context-menu-background: #ffffff;
  --jse-context-menu-background-highlight: #f8fafc;
  --jse-context-menu-separator-color: #e2e8f0;
  --jse-context-menu-color: #334155;
  --jse-context-menu-pointer-background: #e2e8f0;
  --jse-context-menu-pointer-background-highlight: #cbd5e1;
  --jse-context-menu-pointer-color: #334155;
  --jse-key-color: #0f766e;
  --jse-value-color: #1e293b;
  --jse-value-color-number: #1d4ed8;
  --jse-value-color-boolean: #7c3aed;
  --jse-value-color-null: #64748b;
  --jse-value-color-string: #b45309;
  --jse-value-color-url: #0369a1;
  --jse-delimiter-color: #94a3b8;
  --jse-edit-outline: 2px solid #0f172a;
  --jse-selection-background-color: #e2e8f0;
  --jse-selection-background-inactive-color: #f1f5f9;
  --jse-hover-background-color: #f8fafc;
  --jse-active-line-background-color: rgba(148, 163, 184, 0.12);
  --jse-search-match-background-color: #fef3c7;
  --jse-collapsed-items-background-color: #f8fafc;
  --jse-collapsed-items-selected-background-color: #e2e8f0;
  --jse-collapsed-items-link-color: #475569;
  --jse-collapsed-items-link-color-highlight: #0f172a;
  --jse-search-match-color: #fef3c7;
  --jse-search-match-outline: 1px solid #f59e0b;
  --jse-search-match-active-color: #fde68a;
  --jse-search-match-active-outline: 1px solid #d97706;
  --jse-tag-background: #e2e8f0;
  --jse-tag-color: #334155;
  --jse-table-header-background: #f8fafc;
  --jse-table-header-background-highlight: #f1f5f9;
  --jse-table-row-odd-background: rgba(148, 163, 184, 0.08);
  --jse-input-background: #ffffff;
  --jse-input-border: 1px solid #cbd5e1;
  --jse-button-background: #0f172a;
  --jse-button-background-highlight: #1e293b;
  --jse-button-color: #ffffff;
  --jse-button-secondary-background: #e2e8f0;
  --jse-button-secondary-background-highlight: #cbd5e1;
  --jse-button-secondary-background-disabled: #f1f5f9;
  --jse-button-secondary-color: #334155;
  --jse-a-color: #0369a1;
  --jse-a-color-highlight: #075985;
  --jse-svelte-select-background: #ffffff;
  --jse-svelte-select-border: 1px solid #cbd5e1;
  --list-background: #ffffff;
  --item-hover-bg: #f8fafc;
  --multi-item-bg: #e2e8f0;
  --input-color: #1e293b;
  --multi-clear-bg: #94a3b8;
  --multi-item-clear-icon-color: #1e293b;
  --multi-item-outline: 1px solid #cbd5e1;
  --list-shadow: 0 12px 24px rgba(15, 23, 42, 0.12);
  --jse-color-picker-background: #ffffff;
  --jse-color-picker-border-box-shadow: #cbd5e1 0 0 0 1px;
}

.json-panel__editor {
  min-height: 14rem;
}

.json-panel :deep(.jse-main) {
  border: 0;
  border-radius: 1rem;
}

.json-panel :deep(.jse-contents) {
  font-size: 0.875rem;
  line-height: 1.6;
}

.json-panel :deep(.cm-editor) {
  font-family:
    "JetBrains Mono", "SFMono-Regular", "SF Mono", Consolas, "Liberation Mono",
    Menlo, monospace;
}
</style>
