<script setup>
import { onMounted, ref, watch } from 'vue'

import { api } from '@/lib/api'
import { formatBytes } from '@/lib/format'
import { useEventsStore } from '@/stores/events'
import { useToastsStore } from '@/stores/toasts'
import ProgressBar from '@/components/ProgressBar.vue'
import StatusBadge from '@/components/StatusBadge.vue'
import BackupModal from '@/components/BackupModal.vue'
import RestoreModal from '@/components/RestoreModal.vue'

const events = useEventsStore()
const toasts = useToastsStore()

const containers = ref([])
const loading = ref(true)
const loadError = ref('')
const backupTarget = ref(null)
const restoreTarget = ref(null)

async function load() {
  try {
    containers.value = await api.containers.list(true)
    loadError.value = ''
  } catch (e) {
    loadError.value = e.message
  } finally {
    loading.value = false
  }
}

onMounted(load)
// Terminal backup events change container states (cold backups): refresh.
watch(() => events.historyDirty, load)

async function cancelBackup(id) {
  try {
    await api.backups.cancel(id)
    toasts.warning(`Annulation de la sauvegarde #${id} demandée`)
  } catch (e) {
    toasts.error(e.message)
  }
}

function stateDot(state) {
  if (state === 'running') return 'bg-emerald-400'
  if (state === 'paused') return 'bg-amber-400'
  return 'bg-zinc-600'
}
</script>

<template>
  <div class="space-y-8">
    <header>
      <h1 class="text-xl font-semibold text-zinc-100">Tableau de bord</h1>
      <p class="mt-1 text-sm text-zinc-500">Conteneurs détectés et sauvegardes en cours.</p>
    </header>

    <!-- Live backups -->
    <section v-if="events.runningBackups.length > 0" class="space-y-3">
      <h2 class="text-sm font-medium uppercase tracking-wide text-zinc-400">Sauvegardes en cours</h2>
      <div
        v-for="job in events.runningBackups"
        :key="job.backupId"
        class="card space-y-3 p-4"
      >
        <div class="flex items-center justify-between gap-4">
          <div class="flex items-center gap-3">
            <StatusBadge :status="job.status" />
            <span class="text-sm text-zinc-300">Sauvegarde #{{ job.backupId }}</span>
          </div>
          <button class="btn btn-ghost" @click="cancelBackup(job.backupId)">Annuler</button>
        </div>
        <ProgressBar :percent="job.percent || 0" />
        <div class="flex justify-between text-xs text-zinc-500">
          <span>{{ (job.percent || 0).toFixed(1) }} %</span>
          <span v-if="job.totalBytes">{{ formatBytes(job.bytesDone) }} / {{ formatBytes(job.totalBytes) }}</span>
        </div>
        <p v-if="job.lastError" class="text-xs text-red-400">{{ job.lastError }}</p>
      </div>
    </section>

    <!-- Containers -->
    <section class="space-y-3">
      <h2 class="text-sm font-medium uppercase tracking-wide text-zinc-400">Conteneurs</h2>

      <p v-if="loading" class="text-sm text-zinc-500">Chargement…</p>
      <p v-else-if="loadError" class="text-sm text-red-400">{{ loadError }}</p>
      <p v-else-if="containers.length === 0" class="text-sm text-zinc-500">
        Aucun conteneur visible par le démon Docker.
      </p>

      <div v-else class="card overflow-hidden">
        <table class="w-full text-sm">
          <thead>
            <tr class="border-b border-zinc-800 text-left text-xs uppercase tracking-wide text-zinc-500">
              <th class="px-4 py-3">Conteneur</th>
              <th class="px-4 py-3">Image</th>
              <th class="px-4 py-3">Volumes</th>
              <th class="px-4 py-3 text-right">Actions</th>
            </tr>
          </thead>
          <tbody>
            <tr
              v-for="ct in containers"
              :key="ct.id"
              class="border-b border-zinc-800/50 last:border-0 hover:bg-zinc-800/30"
            >
              <td class="px-4 py-3">
                <div class="flex items-center gap-2">
                  <span class="h-2 w-2 shrink-0 rounded-full" :class="stateDot(ct.state)" :title="ct.state" />
                  <span class="font-medium text-zinc-200">{{ ct.name }}</span>
                </div>
              </td>
              <td class="max-w-[16rem] truncate px-4 py-3 font-mono text-xs text-zinc-400">{{ ct.image }}</td>
              <td class="px-4 py-3 text-zinc-400">{{ ct.mounts.length }}</td>
              <td class="px-4 py-3 text-right">
                <div class="flex justify-end gap-2">
                  <button
                    class="btn btn-primary"
                    :disabled="ct.mounts.length === 0"
                    @click="backupTarget = ct"
                  >
                    Sauvegarder
                  </button>
                  <button class="btn btn-ghost" @click="restoreTarget = ct">Restaurer</button>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </section>

    <BackupModal v-if="backupTarget" :container="backupTarget" @close="backupTarget = null" />
    <RestoreModal v-if="restoreTarget" :container="restoreTarget" @close="restoreTarget = null" />
  </div>
</template>
