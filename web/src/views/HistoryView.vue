<script setup>
import { onMounted, ref, watch } from 'vue'

import { api } from '@/lib/api'
import { formatBytes, formatDate, formatDuration, shortID } from '@/lib/format'
import { useEventsStore } from '@/stores/events'
import StatusBadge from '@/components/StatusBadge.vue'

const events = useEventsStore()

const records = ref([])
const loading = ref(true)
const error = ref('')
const statusFilter = ref('')
const expanded = ref(null)

async function load() {
  loading.value = true
  try {
    records.value = await api.backups.history({ status: statusFilter.value, limit: 100 })
    error.value = ''
  } catch (e) {
    error.value = e.message
  } finally {
    loading.value = false
  }
}

onMounted(load)
watch(statusFilter, load)
watch(() => events.historyDirty, load)

function toggle(id) {
  expanded.value = expanded.value === id ? null : id
}
</script>

<template>
  <div class="space-y-6">
    <header class="flex flex-wrap items-end justify-between gap-4">
      <div>
        <h1 class="text-xl font-semibold text-zinc-100">Historique</h1>
        <p class="mt-1 text-sm text-zinc-500">Les 100 dernières sauvegardes.</p>
      </div>
      <select v-model="statusFilter" class="input w-48">
        <option value="">Tous les statuts</option>
        <option value="success">Succès</option>
        <option value="warning">Avertissement</option>
        <option value="failed">Échec</option>
        <option value="running">En cours</option>
        <option value="canceled">Annulé</option>
      </select>
    </header>

    <p v-if="loading" class="text-sm text-zinc-500">Chargement…</p>
    <p v-else-if="error" class="text-sm text-red-400">{{ error }}</p>
    <p v-else-if="records.length === 0" class="text-sm text-zinc-500">Aucune sauvegarde enregistrée.</p>

    <div v-else class="card overflow-hidden">
      <table class="w-full text-sm">
        <thead>
          <tr class="border-b border-zinc-800 text-left text-xs uppercase tracking-wide text-zinc-500">
            <th class="px-4 py-3">#</th>
            <th class="px-4 py-3">Conteneur</th>
            <th class="px-4 py-3">Statut</th>
            <th class="px-4 py-3">Données</th>
            <th class="px-4 py-3">Début</th>
            <th class="px-4 py-3">Durée</th>
            <th class="px-4 py-3">Snapshot</th>
          </tr>
        </thead>
        <tbody>
          <template v-for="rec in records" :key="rec.id">
            <tr
              class="cursor-pointer border-b border-zinc-800/50 last:border-0 hover:bg-zinc-800/30"
              @click="toggle(rec.id)"
            >
              <td class="px-4 py-3 text-zinc-500">{{ rec.id }}</td>
              <td class="px-4 py-3 font-medium text-zinc-200">{{ rec.container_name || shortID(rec.container_id, 12) }}</td>
              <td class="px-4 py-3"><StatusBadge :status="rec.status" /></td>
              <td class="px-4 py-3 text-zinc-400">{{ formatBytes(rec.bytes_processed) }}</td>
              <td class="px-4 py-3 text-zinc-400">{{ formatDate(rec.start_time) }}</td>
              <td class="px-4 py-3 text-zinc-400">{{ formatDuration(rec.start_time, rec.end_time) }}</td>
              <td class="px-4 py-3 font-mono text-xs text-zinc-400">{{ shortID(rec.snapshot_id) }}</td>
            </tr>
            <tr v-if="expanded === rec.id && rec.error_log" class="border-b border-zinc-800/50">
              <td colspan="7" class="bg-zinc-950/60 px-6 py-3">
                <pre class="max-h-48 overflow-auto whitespace-pre-wrap text-xs text-red-300">{{ rec.error_log }}</pre>
              </td>
            </tr>
          </template>
        </tbody>
      </table>
    </div>
  </div>
</template>
