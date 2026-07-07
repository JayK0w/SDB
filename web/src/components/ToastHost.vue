<script setup lang="ts">
import { useToastsStore } from '@/stores/toasts'

const toasts = useToastsStore()

const KIND_CLASSES: Record<string, string> = {
  success: 'border-emerald-500/40 text-emerald-300',
  warning: 'border-amber-500/40 text-amber-300',
  error: 'border-red-500/40 text-red-300',
}
</script>

<template>
  <Teleport to="body">
    <div class="pointer-events-none fixed bottom-4 right-4 z-50 flex w-80 flex-col gap-2">
      <TransitionGroup name="toast">
        <div
          v-for="toast in toasts.items"
          :key="toast.id"
          class="pointer-events-auto flex items-start justify-between gap-3 rounded-xl border bg-zinc-900/95 px-4 py-3 text-sm shadow-lg backdrop-blur"
          :class="KIND_CLASSES[toast.kind] || KIND_CLASSES.error"
        >
          <p class="min-w-0 break-words">{{ toast.message }}</p>
          <button
            class="shrink-0 text-zinc-500 transition-colors hover:text-zinc-200"
            aria-label="Fermer"
            @click="toasts.dismiss(toast.id)"
          >
            ✕
          </button>
        </div>
      </TransitionGroup>
    </div>
  </Teleport>
</template>

<style scoped>
.toast-enter-active,
.toast-leave-active {
  transition: all 0.25s ease;
}
.toast-enter-from,
.toast-leave-to {
  opacity: 0;
  transform: translateY(8px);
}
</style>
