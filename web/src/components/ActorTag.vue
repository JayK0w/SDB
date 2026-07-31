<script setup lang="ts">
import { computed } from 'vue'

const props = defineProps<{ actor?: string }>()

// Les runs planifies sont attribues "system:schedule:<nom>". Les distinguer
// visuellement d'un humain evite de croire qu'une personne a declenche une
// operation destructrice — et inversement.
const isSystem = computed(() => (props.actor || '').startsWith('system:'))

const label = computed(() => {
  const a = props.actor
  if (!a) return '—'
  if (!isSystem.value) return a
  const parts = a.split(':')
  // "system:schedule:nightly" -> "nightly"
  return parts.length > 2 ? parts.slice(2).join(':') : parts[1] || 'système'
})

const title = computed(() =>
  props.actor
    ? isSystem.value
      ? `Déclenché automatiquement (${props.actor})`
      : `Déclenché par ${props.actor}`
    : "Auteur inconnu : run antérieur à la traçabilité",
)
</script>

<template>
  <span
    v-if="isSystem"
    :title="title"
    class="inline-flex items-center gap-1 rounded-md border border-sky-500/30 bg-sky-500/10 px-1.5 py-0.5 text-xs text-sky-300"
  >
    <span aria-hidden="true">⏱</span>{{ label }}
  </span>
  <span v-else-if="actor" :title="title" class="text-sm text-zinc-300">{{ label }}</span>
  <span v-else :title="title" class="text-sm text-zinc-600">—</span>
</template>
