<script setup lang="ts">
import { onMounted, ref, watch } from 'vue'

import { api } from '@/lib/api'
import { formatBytes, formatDate, formatDuration, shortID } from '@/lib/format'
import type { BackupRecord, RestoreRecord } from '@/types'
import { useEventsStore } from '@/stores/events'
import StatusBadge from '@/components/StatusBadge.vue'
import ActorTag from '@/components/ActorTag.vue'

const events = useEventsStore()

const tab = ref<'backups' | 'restores'>('backups')
const backups = ref<BackupRecord[]>([])
const restores = ref<RestoreRecord[]>([])
const loading = ref(true)
const error = ref('')
const statusFilter = ref('')
const expanded = ref<string | null>(null)

async function load(): Promise<void> {
  loading.value = true
  try {
    ;[backups.value, restores.value] = await Promise.all([
      api.backups.history({ status: statusFilter.value, limit: 100 }),
      api.restores.history({ status: statusFilter.value, limit: 100 }),
    ])
    error.value = ''
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
  } finally {
    loading.value = false
  }
}

onMounted(load)
watch(statusFilter, load)
watch(() => events.historyDirty, load)

function toggle(key: string): void {
  expanded.value = expanded.value === key ? null : key
}
</script>

<template>
  <div class="space-y-6">
    <header class="flex flex-wrap items-end justify-between gap-4">
      <div>
        <h1 class="text-xl font-semibold text-zinc-100">Historique</h1>
        <p class="mt-1 text-sm text-zinc-500">Les 100 dernières opérations.</p>
      </div>
      <div class="flex items-center gap-3">
        <div class="flex rounded-lg border border-zinc-700 p-0.5">
          <button
            class="rounded-md px-3 py-1 text-sm transition-colors"
            :class="tab === 'backups' ? 'bg-zinc-700 text-zinc-100' : 'text-zinc-400 hover:text-zinc-200'"
            @click="tab = 'backups'"
          >
            Sauvegardes
          </button>
          <button
            class="rounded-md px-3 py-1 text-sm transition-colors"
            :class="tab === 'restores' ? 'bg-zinc-700 text-zinc-100' : 'text-zinc-400 hover:text-zinc-200'"
            @click="tab = 'restores'"
          >
            Restaurations
          </button>
        </div>
        <select v-model="statusFilter" class="input w-44">
          <option value="">Tous les statuts</option>
          <option value="success">Succès</option>
          <option value="warning">Avertissement</option>
          <option value="failed">Échec</option>
          <option value="running">En cours</option>
          <option value="canceled">Annulé</option>
        </select>
      </div>
    </header>

    <p v-if="loading" class="text-sm text-zinc-500">Chargement…</p>
    <p v-else-if="error" class="text-sm text-red-400">{{ error }}</p>

    <!-- Backups -->
    <template v-else-if="tab === 'backups'">
      <p v-if="backups.length === 0" class="text-sm text-zinc-500">Aucune sauvegarde enregistrée.</p>
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
              <th class="px-4 py-3">Auteur</th>
              <th class="px-4 py-3">Snapshot</th>
            </tr>
          </thead>
          <tbody>
            <template v-for="rec in backups" :key="rec.id">
              <tr
                class="cursor-pointer border-b border-zinc-800/50 last:border-0 hover:bg-zinc-800/30"
                @click="toggle(`b${rec.id}`)"
              >
                <td class="px-4 py-3 text-zinc-500">{{ rec.id }}</td>
                <td class="px-4 py-3 font-medium text-zinc-200">{{ rec.container_name || shortID(rec.container_id, 12) }}</td>
                <td class="px-4 py-3"><StatusBadge :status="rec.status" /></td>
                <td class="px-4 py-3 text-zinc-400">{{ formatBytes(rec.bytes_processed) }}</td>
                <td class="px-4 py-3 text-zinc-400">{{ formatDate(rec.start_time) }}</td>
                <td class="px-4 py-3 text-zinc-400">{{ formatDuration(rec.start_time, rec.end_time) }}</td>
                <td class="px-4 py-3"><ActorTag :actor="rec.triggered_by" /></td>
                <td class="px-4 py-3 font-mono text-xs text-zinc-400">{{ shortID(rec.snapshot_id) }}</td>
              </tr>
              <tr v-if="expanded === `b${rec.id}` && rec.error_log" class="border-b border-zinc-800/50">
                <td colspan="8" class="bg-zinc-950/60 px-6 py-3">
                  <pre class="max-h-48 overflow-auto whitespace-pre-wrap text-xs text-red-300">{{ rec.error_log }}</pre>
                </td>
              </tr>
            </template>
          </tbody>
        </table>
      </div>
    </template>

    <!-- Restores -->
    <template v-else>
      <p v-if="restores.length === 0" class="text-sm text-zinc-500">Aucune restauration enregistrée.</p>
      <div v-else class="card overflow-hidden">
        <table class="w-full text-sm">
          <thead>
            <tr class="border-b border-zinc-800 text-left text-xs uppercase tracking-wide text-zinc-500">
              <th class="px-4 py-3">#</th>
              <th class="px-4 py-3">Volume</th>
              <th class="px-4 py-3">Conteneur</th>
              <th class="px-4 py-3">Statut</th>
              <th class="px-4 py-3">Snapshot</th>
              <th class="px-4 py-3">Début</th>
              <th class="px-4 py-3">Durée</th>
              <th class="px-4 py-3">Auteur</th>
            </tr>
          </thead>
          <tbody>
            <template v-for="rec in restores" :key="rec.id">
              <tr
                class="cursor-pointer border-b border-zinc-800/50 last:border-0 hover:bg-zinc-800/30"
                @click="toggle(`r${rec.id}`)"
              >
                <td class="px-4 py-3 text-zinc-500">{{ rec.id }}</td>
                <td class="px-4 py-3 font-medium text-zinc-200">{{ rec.target_volume }}</td>
                <td class="px-4 py-3 text-zinc-400">{{ rec.container_name || '—' }}</td>
                <td class="px-4 py-3"><StatusBadge :status="rec.status" /></td>
                <td class="px-4 py-3 font-mono text-xs text-zinc-400">{{ shortID(rec.snapshot_id) }}</td>
                <td class="px-4 py-3 text-zinc-400">{{ formatDate(rec.start_time) }}</td>
                <td class="px-4 py-3 text-zinc-400">{{ formatDuration(rec.start_time, rec.end_time) }}</td>
                <td class="px-4 py-3"><ActorTag :actor="rec.triggered_by" /></td>
              </tr>
              <tr v-if="expanded === `r${rec.id}` && rec.error_log" class="border-b border-zinc-800/50">
                <td colspan="8" class="bg-zinc-950/60 px-6 py-3">
                  <pre class="max-h-48 overflow-auto whitespace-pre-wrap text-xs text-red-300">{{ rec.error_log }}</pre>
                </td>
              </tr>
            </template>
          </tbody>
        </table>
      </div>
    </template>
  </div>
</template>
