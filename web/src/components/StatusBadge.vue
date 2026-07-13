<script setup lang="ts">
import { computed } from 'vue'

const props = defineProps<{ status: string }>()

// couleurs fonctionnelles : rouge critique, vert succes, ambre alerte
const MAP: Record<string, { label: string; classes: string }> = {
  success: { label: 'Succès', classes: 'bg-emerald-500/15 text-emerald-400 border-emerald-500/30' },
  warning: { label: 'Avertissement', classes: 'bg-amber-500/15 text-amber-400 border-amber-500/30' },
  failed: { label: 'Échec', classes: 'bg-red-500/15 text-red-400 border-red-500/30' },
  running: { label: 'En cours', classes: 'bg-sky-500/15 text-sky-400 border-sky-500/30 animate-pulse' },
  pending: { label: 'En attente', classes: 'bg-zinc-500/15 text-zinc-400 border-zinc-500/30' },
  canceled: { label: 'Annulé', classes: 'bg-zinc-500/15 text-zinc-400 border-zinc-500/30' },
}

const entry = computed(
  () => MAP[props.status] || { label: props.status, classes: 'bg-zinc-500/15 text-zinc-400 border-zinc-500/30' },
)
</script>

<template>
  <span
    class="inline-flex items-center rounded-full border px-2 py-0.5 text-xs font-medium"
    :class="entry.classes"
  >
    {{ entry.label }}
  </span>
</template>
