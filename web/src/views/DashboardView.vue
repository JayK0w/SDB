<script setup lang="ts">
import { onMounted, ref, watch } from 'vue'

import { api } from '@/lib/api'
import { formatBytes } from '@/lib/format'
import type { Container } from '@/types'
import { useAuthStore } from '@/stores/auth'
import { useEventsStore, type JobProgress } from '@/stores/events'
import { useToastsStore } from '@/stores/toasts'
import ProgressBar from '@/components/ProgressBar.vue'
import StatusBadge from '@/components/StatusBadge.vue'
import BackupModal from '@/components/BackupModal.vue'
import RestoreModal from '@/components/RestoreModal.vue'

const auth = useAuthStore()
const events = useEventsStore()
const toasts = useToastsStore()

const containers = ref<Container[]>([])
const loading = ref(true)
const loadError = ref('')
const backupTarget = ref<Container | null>(null)
const restoreTarget = ref<Container | null>(null)

async function load(): Promise<void> {
  try {
    containers.value = await api.containers.list(true)
    loadError.value = ''
  } catch (e) {
    loadError.value = e instanceof Error ? e.message : String(e)
  } finally {
    loading.value = false
  }
}

onMounted(load)
// un evenement terminal change l etat des conteneurs : refresh
watch(() => events.historyDirty, load)

async function cancelJob(job: JobProgress): Promise<void> {
  try {
    if (job.kind === 'restore') {
      await api.restores.cancel(job.id)
      toasts.warning(`Annulation de la restauration #${job.id} demandée`)
    } else {
      await api.backups.cancel(job.id)
      toasts.warning(`Annulation de la sauvegarde #${job.id} demandée`)
    }
  } catch (e) {
    toasts.error(e instanceof Error ? e.message : String(e))
  }
}

function jobLabel(job: JobProgress): string {
  const kind = job.kind === 'restore' ? 'Restauration' : 'Sauvegarde'
  return job.container ? `${kind} #${job.id} — ${job.container}` : `${kind} #${job.id}`
}

function stateDot(state: string): string {
  if (state === 'running') return 'bg-emerald-400'
  if (state === 'paused') return 'bg-amber-400'
  return 'bg-zinc-600'
}
</script>

<template>
  <div class="space-y-8">
    <header>
      <h1 class="text-xl font-semibold text-zinc-100">Tableau de bord</h1>
      <p class="mt-1 text-sm text-zinc-500">Conteneurs détectés, sauvegardes et restaurations en cours.</p>
    </header>

    <!-- Live jobs -->
    <section v-if="events.runningJobs.length > 0" class="space-y-3">
      <h2 class="text-sm font-medium uppercase tracking-wide text-zinc-400">Opérations en cours</h2>
      <div
        v-for="job in events.runningJobs"
        :key="`${job.kind}:${job.id}`"
        class="card space-y-3 p-4"
      >
        <div class="flex items-center justify-between gap-4">
          <div class="flex items-center gap-3">
            <StatusBadge :status="job.status" />
            <span class="text-sm text-zinc-300">{{ jobLabel(job) }}</span>
          </div>
          <button class="btn btn-ghost" @click="cancelJob(job)">Annuler</button>
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
                  <!-- restauration = opération privilégiée (403 côté API
                       pour le rôle user) : ne pas proposer un bouton qui
                       échouerait -->
                  <button
                    v-if="auth.isAdmin"
                    class="btn btn-ghost"
                    @click="restoreTarget = ct"
                  >
                    Restaurer
                  </button>
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
