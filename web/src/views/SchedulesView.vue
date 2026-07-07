<script setup lang="ts">
import { onMounted, ref } from 'vue'

import { api } from '@/lib/api'
import { formatDate } from '@/lib/format'
import type { Schedule } from '@/types'
import { useToastsStore } from '@/stores/toasts'
import ScheduleModal from '@/components/ScheduleModal.vue'

const toasts = useToastsStore()

const schedules = ref<Schedule[]>([])
const loading = ref(true)
const error = ref('')
const showModal = ref(false)
const editTarget = ref<Schedule | null>(null)

async function load(): Promise<void> {
  loading.value = true
  try {
    schedules.value = await api.schedules.list()
    error.value = ''
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
  } finally {
    loading.value = false
  }
}

onMounted(load)

function openCreate(): void {
  editTarget.value = null
  showModal.value = true
}

function openEdit(s: Schedule): void {
  editTarget.value = s
  showModal.value = true
}

async function toggleEnabled(s: Schedule): Promise<void> {
  try {
    await api.schedules.update(s.id, { ...s, enabled: !s.enabled })
    toasts.success(`Planification « ${s.name} » ${s.enabled ? 'désactivée' : 'activée'}`)
    load()
  } catch (e) {
    toasts.error(e instanceof Error ? e.message : String(e))
  }
}

async function runNow(s: Schedule): Promise<void> {
  try {
    const rec = await api.schedules.run(s.id)
    toasts.success(`Sauvegarde #${rec.id} lancée (planification « ${s.name} »)`)
    load()
  } catch (e) {
    toasts.error(e instanceof Error ? e.message : String(e))
  }
}

async function remove(s: Schedule): Promise<void> {
  if (!window.confirm(`Supprimer la planification « ${s.name} » ?`)) return
  try {
    await api.schedules.remove(s.id)
    toasts.success(`Planification « ${s.name} » supprimée`)
    load()
  } catch (e) {
    toasts.error(e instanceof Error ? e.message : String(e))
  }
}
</script>

<template>
  <div class="space-y-6">
    <header class="flex flex-wrap items-end justify-between gap-4">
      <div>
        <h1 class="text-xl font-semibold text-zinc-100">Planifications</h1>
        <p class="mt-1 text-sm text-zinc-500">Sauvegardes récurrentes (cron, heure UTC).</p>
      </div>
      <button class="btn btn-primary" @click="openCreate">Nouvelle planification</button>
    </header>

    <p v-if="loading" class="text-sm text-zinc-500">Chargement…</p>
    <p v-else-if="error" class="text-sm text-red-400">{{ error }}</p>
    <p v-else-if="schedules.length === 0" class="text-sm text-zinc-500">
      Aucune planification pour l’instant — vos sauvegardes ne tournent que manuellement.
    </p>

    <div v-else class="card overflow-hidden">
      <table class="w-full text-sm">
        <thead>
          <tr class="border-b border-zinc-800 text-left text-xs uppercase tracking-wide text-zinc-500">
            <th class="px-4 py-3">Nom</th>
            <th class="px-4 py-3">Conteneur</th>
            <th class="px-4 py-3">Cron</th>
            <th class="px-4 py-3">État</th>
            <th class="px-4 py-3">Dernière exécution</th>
            <th class="px-4 py-3 text-right">Actions</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="s in schedules" :key="s.id" class="border-b border-zinc-800/50 last:border-0 hover:bg-zinc-800/30">
            <td class="px-4 py-3 font-medium text-zinc-200">{{ s.name }}</td>
            <td class="px-4 py-3 text-zinc-400">{{ s.container_name }}</td>
            <td class="px-4 py-3 font-mono text-xs text-zinc-400">{{ s.cron }}</td>
            <td class="px-4 py-3">
              <button
                class="inline-flex items-center rounded-full border px-2 py-0.5 text-xs font-medium transition-colors"
                :class="s.enabled
                  ? 'border-emerald-500/30 bg-emerald-500/15 text-emerald-400'
                  : 'border-zinc-500/30 bg-zinc-500/15 text-zinc-400'"
                :title="s.enabled ? 'Cliquer pour désactiver' : 'Cliquer pour activer'"
                @click="toggleEnabled(s)"
              >
                {{ s.enabled ? 'Active' : 'Inactive' }}
              </button>
            </td>
            <td class="px-4 py-3 text-zinc-400">{{ formatDate(s.last_run_at) }}</td>
            <td class="px-4 py-3 text-right">
              <div class="flex justify-end gap-2">
                <button class="btn btn-ghost" @click="runNow(s)">Exécuter</button>
                <button class="btn btn-ghost" @click="openEdit(s)">Modifier</button>
                <button class="btn btn-danger" @click="remove(s)">Supprimer</button>
              </div>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <ScheduleModal v-if="showModal" :schedule="editTarget" @close="showModal = false" @saved="load" />
  </div>
</template>
