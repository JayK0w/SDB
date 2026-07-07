<script setup>
import { computed } from 'vue'

import { useHealthStore } from '@/stores/health'
import { useEventsStore } from '@/stores/events'

const health = useHealthStore()
const events = useEventsStore()

// Single global state, worst signal wins: red = critical, amber =
// degraded (Docker down or live stream reconnecting), green = all good.
const level = computed(() => {
  if (health.status === 'down') return 'critical'
  if (health.status !== 'ok' || events.wsStatus !== 'open') return 'degraded'
  return 'ok'
})

const label = computed(
  () => ({ ok: 'Opérationnel', degraded: 'Dégradé', critical: 'Hors ligne' })[level.value],
)

const detail = computed(() => {
  if (level.value === 'critical') return 'API injoignable'
  if (health.status === 'degraded') return 'Docker injoignable'
  if (events.wsStatus !== 'open') return 'Flux temps réel en reconnexion…'
  return `SDB ${health.version || ''}`.trim()
})

const dotClass = computed(
  () =>
    ({
      ok: 'bg-emerald-400',
      degraded: 'bg-amber-400 animate-pulse',
      critical: 'bg-red-500 animate-pulse',
    })[level.value],
)
</script>

<template>
  <div class="flex items-center gap-3 px-1" :title="detail">
    <span class="relative flex h-3 w-3 shrink-0">
      <span class="absolute inline-flex h-full w-full rounded-full opacity-40" :class="dotClass" style="transform: scale(1.9)" />
      <span class="relative inline-flex h-3 w-3 rounded-full" :class="dotClass" />
    </span>
    <div class="min-w-0">
      <p class="truncate text-sm font-semibold text-zinc-100">{{ label }}</p>
      <p class="truncate text-xs text-zinc-500">{{ detail }}</p>
    </div>
  </div>
</template>
