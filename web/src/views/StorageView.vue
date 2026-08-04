<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'

import { api } from '@/lib/api'
import { formatDate, shortID } from '@/lib/format'
import type { Replication, Snapshot, Storage } from '@/types'
import { useAuthStore } from '@/stores/auth'
import { useToastsStore } from '@/stores/toasts'
import StorageModal from '@/components/StorageModal.vue'

const auth = useAuthStore()
const toasts = useToastsStore()

const storages = ref<Storage[]>([])
const loading = ref(true)
const error = ref('')
const showCreate = ref(false)
const createAsCopyOf = ref(0)
const expanded = ref<number | null>(null)
const snapshots = ref<Snapshot[]>([])
const snapshotsLoading = ref(false)

// etat de la copie secondaire : mesure a la demande (deux `restic snapshots`
// par paire), jamais rafraichi en boucle
const replication = ref<Replication[]>([])
const replicationLoading = ref(false)
const replicationChecked = ref(false)

function errMsg(e: unknown): string {
  return e instanceof Error ? e.message : String(e)
}

function storageName(id: number): string {
  return storages.value.find((s) => s.id === id)?.name ?? `#${id}`
}

const hasCopy = computed(() => storages.value.some((s) => s.copy_of_storage_id))

// ouvre la creation deja positionnee sur « copie de <premier depot principal> » :
// le conseil doit mener au geste, pas a un formulaire vierge
function startCopyCreation(): void {
  createAsCopyOf.value = storages.value.find((s) => !s.copy_of_storage_id)?.id ?? 0
  showCreate.value = true
}

function startPlainCreation(): void {
  createAsCopyOf.value = 0
  showCreate.value = true
}

function statusOf(id: number): Replication | undefined {
  return replication.value.find((r) => r.copy_id === id)
}

// retard exprime en clair : « 3 j » parle a l'exploitant, 259200 non
function humanLag(seconds: number): string {
  if (seconds < 60) return `${Math.round(seconds)} s`
  if (seconds < 3600) return `${Math.round(seconds / 60)} min`
  if (seconds < 86400) return `${Math.round(seconds / 3600)} h`
  return `${Math.round(seconds / 86400)} j`
}

async function loadReplication(): Promise<void> {
  replicationLoading.value = true
  try {
    const res = await api.storage.replication()
    replication.value = res.replication ?? []
    replicationChecked.value = true
    if (res.error) toasts.error(res.error)
  } catch (e) {
    toasts.error(errMsg(e))
  } finally {
    replicationLoading.value = false
  }
}

async function replicate(storage: Storage): Promise<void> {
  try {
    await api.storage.replicate(storage.id)
    toasts.success(`Réplication vers « ${storage.name} » lancée`)
  } catch (e) {
    toasts.error(errMsg(e))
  }
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
      <div class="flex gap-2">
        <button class="btn btn-ghost" :disabled="replicationLoading" @click="loadReplication">
          {{ replicationLoading ? 'Mesure…' : 'État des copies' }}
        </button>
        <button v-if="auth.isAdmin" class="btn btn-primary" @click="startPlainCreation">Nouveau stockage</button>
      </div>
    </header>

    <!-- Sans seconde copie, chaque sauvegarde ne tient qu'a un seul support :
         le dire explicitement plutot que de laisser l'absence passer pour un
         etat normal. -->
    <div
      v-if="!loading && !error && storages.length > 0 && !hasCopy"
      class="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-amber-500/40 bg-amber-500/10 px-3 py-2 text-sm text-amber-200"
    >
      <p>
        <strong>Aucune copie secondaire.</strong>
        Toutes vos sauvegardes vivent sur un support unique : le perdre ou le corrompre les perd
        toutes. Le verrou append-only protège de la suppression, pas de la perte du support.
        <span class="block text-xs text-amber-200/80">
          SDB fonctionne sans, et vous pouvez l'activer à tout moment : les snapshots déjà présents
          sont recopiés dès la création du second dépôt.
        </span>
      </p>
      <button v-if="auth.isAdmin" class="btn btn-primary shrink-0" @click="startCopyCreation">
        Ajouter une copie secondaire
      </button>
    </div>

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
              <span
                v-if="s.append_only"
                title="Dépôt append-only : SDB refuse la rétention et la suppression de cette cible."
                class="ml-2 rounded-full border border-emerald-500/40 bg-emerald-500/10 px-2 py-0.5 text-xs text-emerald-300"
              >
                append-only
              </span>
              <span
                v-if="s.copy_of_storage_id"
                :title="`Copie secondaire de « ${storageName(s.copy_of_storage_id)} » : alimentée par réplication, jamais sauvegardée directement.`"
                class="ml-2 rounded-full border border-sky-500/40 bg-sky-500/10 px-2 py-0.5 text-xs text-sky-300"
              >
                copie de {{ storageName(s.copy_of_storage_id) }}
              </span>
            </p>
            <p class="mt-1 truncate font-mono text-xs text-zinc-500">{{ s.endpoint }}</p>
            <p v-if="s.credential_keys.length" class="mt-1 text-xs text-zinc-600">
              Identifiants : {{ s.credential_keys.join(', ') }}
            </p>
            <p
              v-if="s.copy_of_storage_id && statusOf(s.id)"
              class="mt-1 text-xs"
              :class="statusOf(s.id)!.pending > 0 ? 'text-amber-300' : 'text-emerald-300'"
            >
              <template v-if="statusOf(s.id)!.pending > 0">
                {{ statusOf(s.id)!.pending }} snapshot(s) non copié(s), le plus ancien remonte à
                {{ humanLag(statusOf(s.id)!.lag_seconds) }}
              </template>
              <template v-else>
                À jour — {{ statusOf(s.id)!.copied_snapshots }} snapshot(s) répliqué(s)
              </template>
            </p>
            <p
              v-else-if="s.copy_of_storage_id && replicationChecked"
              class="mt-1 text-xs text-zinc-600"
            >
              État de la copie indisponible.
            </p>
          </div>
          <div class="flex gap-2">
            <button class="btn btn-ghost" @click="toggleSnapshots(s)">
              {{ expanded === s.id ? 'Masquer' : 'Snapshots' }}
            </button>
            <template v-if="auth.isAdmin">
              <button class="btn btn-ghost" @click="runCheck(s)">Vérifier</button>
              <button v-if="s.copy_of_storage_id" class="btn btn-ghost" @click="replicate(s)">
                Répliquer
              </button>
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

    <StorageModal
      v-if="showCreate"
      :sources="storages"
      :default-copy-of="createAsCopyOf"
      @close="showCreate = false"
      @created="load"
    />
  </div>
</template>
