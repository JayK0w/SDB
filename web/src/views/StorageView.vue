<script setup lang="ts">
import { onMounted, ref } from 'vue'

import { api } from '@/lib/api'
import { formatDate, shortID } from '@/lib/format'
import type { Snapshot, Storage } from '@/types'
import { useAuthStore } from '@/stores/auth'
import { useToastsStore } from '@/stores/toasts'
import StorageModal from '@/components/StorageModal.vue'

const auth = useAuthStore()
const toasts = useToastsStore()

const storages = ref<Storage[]>([])
const loading = ref(true)
const error = ref('')
const showCreate = ref(false)
const expanded = ref<number | null>(null)
const snapshots = ref<Snapshot[]>([])
const snapshotsLoading = ref(false)

function errMsg(e: unknown): string {
  return e instanceof Error ? e.message : String(e)
}

async function load(): Promise<void> {
  loading.value = true
  try {
    storages.value = await api.storage.list()
    error.value = ''
  } catch (e) {
    error.value = errMsg(e)
  } finally {
    loading.value = false
  }
}

onMounted(load)

async function toggleSnapshots(storage: Storage): Promise<void> {
  if (expanded.value === storage.id) {
    expanded.value = null
    return
  }
  expanded.value = storage.id
  snapshots.value = []
  snapshotsLoading.value = true
  try {
    snapshots.value = await api.storage.snapshots(storage.id)
  } catch (e) {
    toasts.error(errMsg(e))
  } finally {
    snapshotsLoading.value = false
  }
}

async function runCheck(storage: Storage): Promise<void> {
  try {
    await api.storage.check(storage.id)
    toasts.success(`Vérification d’intégrité de « ${storage.name} » lancée`)
  } catch (e) {
    toasts.error(errMsg(e))
  }
}

async function remove(storage: Storage): Promise<void> {
  if (!window.confirm(`Supprimer le stockage « ${storage.name} » ? Le dépôt restic n’est pas effacé.`)) return
  try {
    await api.storage.remove(storage.id)
    toasts.success(`Stockage « ${storage.name} » supprimé`)
    load()
  } catch (e) {
    toasts.error(errMsg(e))
  }
}
</script>

<template>
  <div class="space-y-6">
    <header class="flex flex-wrap items-end justify-between gap-4">
      <div>
        <h1 class="text-xl font-semibold text-zinc-100">Stockage</h1>
        <p class="mt-1 text-sm text-zinc-500">Dépôts restic chiffrés (AES-256) accueillant vos snapshots.</p>
      </div>
      <button v-if="auth.isAdmin" class="btn btn-primary" @click="showCreate = true">Nouveau stockage</button>
    </header>

    <p v-if="loading" class="text-sm text-zinc-500">Chargement…</p>
    <p v-else-if="error" class="text-sm text-red-400">{{ error }}</p>
    <p v-else-if="storages.length === 0" class="text-sm text-zinc-500">
      Aucun stockage configuré pour l’instant.
    </p>

    <div v-else class="space-y-3">
      <div v-for="s in storages" :key="s.id" class="card p-4">
        <div class="flex flex-wrap items-center justify-between gap-3">
          <div class="min-w-0">
            <p class="font-medium text-zinc-100">
              {{ s.name }}
              <span class="ml-2 rounded-full border border-zinc-700 px-2 py-0.5 text-xs uppercase text-zinc-400">{{ s.type }}</span>
            </p>
            <p class="mt-1 truncate font-mono text-xs text-zinc-500">{{ s.endpoint }}</p>
            <p v-if="s.credential_keys.length" class="mt-1 text-xs text-zinc-600">
              Identifiants : {{ s.credential_keys.join(', ') }}
            </p>
          </div>
          <div class="flex gap-2">
            <button class="btn btn-ghost" @click="toggleSnapshots(s)">
              {{ expanded === s.id ? 'Masquer' : 'Snapshots' }}
            </button>
            <template v-if="auth.isAdmin">
              <button class="btn btn-ghost" @click="runCheck(s)">Vérifier</button>
              <button class="btn btn-danger" @click="remove(s)">Supprimer</button>
            </template>
          </div>
        </div>

        <div v-if="expanded === s.id" class="mt-4 border-t border-zinc-800 pt-4">
          <p v-if="snapshotsLoading" class="text-sm text-zinc-500">Chargement des snapshots…</p>
          <p v-else-if="snapshots.length === 0" class="text-sm text-zinc-500">Dépôt vide.</p>
          <table v-else class="w-full text-sm">
            <thead>
              <tr class="text-left text-xs uppercase tracking-wide text-zinc-500">
                <th class="py-2 pr-4">ID</th>
                <th class="py-2 pr-4">Date</th>
                <th class="py-2 pr-4">Tags</th>
                <th class="py-2">Chemins</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="snap in snapshots" :key="snap.id" class="border-t border-zinc-800/50">
                <td class="py-2 pr-4 font-mono text-xs text-zinc-300">{{ shortID(snap.short_id || snap.id) }}</td>
                <td class="py-2 pr-4 text-zinc-400">{{ formatDate(snap.time) }}</td>
                <td class="py-2 pr-4 text-xs text-zinc-500">{{ (snap.tags || []).join(', ') || '—' }}</td>
                <td class="py-2 font-mono text-xs text-zinc-500">{{ (snap.paths || []).join(', ') }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
    </div>

    <StorageModal v-if="showCreate" @close="showCreate = false" @created="load" />
  </div>
</template>
