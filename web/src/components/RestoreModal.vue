<script setup>
import { computed, onMounted, ref, watch } from 'vue'

import { api } from '@/lib/api'
import { formatDate, shortID } from '@/lib/format'
import { useToastsStore } from '@/stores/toasts'
import Modal from './Modal.vue'

const props = defineProps({
  container: { type: Object, required: true },
})
const emit = defineEmits(['close'])

const toasts = useToastsStore()

const storages = ref([])
const storageId = ref(null)
const snapshots = ref([])
const snapshotsLoading = ref(false)
const snapshotId = ref('')
const targetVolume = ref('')
const stopContainer = ref(true)
const submitting = ref(false)
const error = ref('')

const volumeNames = computed(() =>
  (props.container.mounts || []).filter((m) => m.type === 'volume' && m.name).map((m) => m.name),
)

onMounted(async () => {
  targetVolume.value = volumeNames.value[0] || ''
  try {
    storages.value = await api.storage.list()
    if (storages.value.length > 0) storageId.value = storages.value[0].id
  } catch (e) {
    error.value = e.message
  }
})

watch(storageId, async (id) => {
  snapshots.value = []
  snapshotId.value = ''
  if (!id) return
  snapshotsLoading.value = true
  try {
    // Only this container's snapshots, thanks to the container:<name> tag.
    snapshots.value = await api.storage.snapshots(id, [`container:${props.container.name}`])
    if (snapshots.value.length > 0) snapshotId.value = snapshots.value[0].id
  } catch (e) {
    error.value = e.message
  } finally {
    snapshotsLoading.value = false
  }
})

async function submit() {
  error.value = ''
  submitting.value = true
  try {
    await api.restores.start({
      storage_id: storageId.value,
      snapshot_id: snapshotId.value,
      target_volume: targetVolume.value,
      stop_container: stopContainer.value ? props.container.id : '',
    })
    toasts.success(`Restauration du volume ${targetVolume.value} lancée`)
    emit('close')
  } catch (e) {
    error.value = e.message
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <Modal :title="`Restaurer ${container.name}`" @close="emit('close')">
    <form class="space-y-4" @submit.prevent="submit">
      <p class="rounded-lg border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-xs text-amber-300">
        La restauration écrase le contenu actuel du volume cible.
      </p>

      <div>
        <label class="label" for="restore-storage">Source</label>
        <select id="restore-storage" v-model="storageId" class="input">
          <option v-for="s in storages" :key="s.id" :value="s.id">{{ s.name }} ({{ s.type }})</option>
        </select>
      </div>

      <div>
        <label class="label" for="restore-snapshot">Snapshot</label>
        <select id="restore-snapshot" v-model="snapshotId" class="input" :disabled="snapshotsLoading">
          <option v-for="snap in snapshots" :key="snap.id" :value="snap.id">
            {{ shortID(snap.short_id || snap.id) }} — {{ formatDate(snap.time) }}
          </option>
        </select>
        <p v-if="snapshotsLoading" class="mt-1 text-xs text-zinc-500">Chargement des snapshots…</p>
        <p v-else-if="storageId && snapshots.length === 0" class="mt-1 text-xs text-amber-400">
          Aucun snapshot pour ce conteneur dans ce dépôt.
        </p>
      </div>

      <div>
        <label class="label" for="restore-volume">Volume cible</label>
        <select id="restore-volume" v-model="targetVolume" class="input">
          <option v-for="name in volumeNames" :key="name" :value="name">{{ name }}</option>
        </select>
      </div>

      <label class="flex items-start gap-3 text-sm text-zinc-300">
        <input v-model="stopContainer" type="checkbox" class="mt-0.5 accent-indigo-500" />
        <span>
          Arrêter le conteneur pendant la restauration (recommandé)
          <span class="block text-xs text-zinc-500">Évite qu’une application n’écrive pendant la réécriture du volume.</span>
        </span>
      </label>

      <p v-if="error" class="text-sm text-red-400">{{ error }}</p>

      <div class="flex justify-end gap-2 pt-2">
        <button type="button" class="btn btn-ghost" @click="emit('close')">Annuler</button>
        <button
          type="submit"
          class="btn btn-danger"
          :disabled="submitting || !snapshotId || !targetVolume"
        >
          {{ submitting ? 'Lancement…' : 'Restaurer' }}
        </button>
      </div>
    </form>
  </Modal>
</template>
