<script setup lang="ts">
import { onMounted, onUnmounted } from 'vue'

defineProps<{ title: string }>()

const emit = defineEmits<{ close: [] }>()

function onKeydown(e: KeyboardEvent): void {
  if (e.key === 'Escape') emit('close')
}

onMounted(() => window.addEventListener('keydown', onKeydown))
onUnmounted(() => window.removeEventListener('keydown', onKeydown))
</script>

<template>
  <Teleport to="body">
    <div class="fixed inset-0 z-40 flex items-center justify-center p-4">
      <div class="absolute inset-0 bg-black/60 backdrop-blur-sm" @click="emit('close')" />
      <div
        class="card relative z-10 max-h-[90vh] w-full max-w-lg overflow-y-auto p-6"
        role="dialog"
        aria-modal="true"
      >
        <div class="mb-4 flex items-center justify-between">
          <h2 class="text-lg font-semibold text-zinc-100">{{ title }}</h2>
          <button
            class="text-zinc-500 transition-colors hover:text-zinc-200"
            aria-label="Fermer"
            @click="emit('close')"
          >
            ✕
          </button>
        </div>
        <slot />
      </div>
    </div>
  </Teleport>
</template>
