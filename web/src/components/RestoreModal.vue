<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'

import { api } from '@/lib/api'
import { formatDate, shortID } from '@/lib/format'
import type { Container, Snapshot, Storage } from '@/types'
import { useToastsStore } from '@/stores/toasts'
import Modal from './Modal.vue'

const props = defineProps<{ container: Container }>()
const emit = defineEmits<{ close: [] }>()

const toasts = useToastsStore()

const storages = ref<Storage[]>([])
const storageId = ref<number | null>(null)
const snapshots = ref<Snapshot[]>([])
const snapshotsLoading = ref(false)
const snapshotId = ref('')
const mode = ref<'in-place' | 'clone'>('in-place')
const sourceVolume = ref('')
const cloneVolume = ref('')
const stopContainer = ref(true)
const submitting = ref(false)
const error = ref('')
const composeYaml = ref('')

const volumeNames = computed(() =>
  (props.container.mounts || []).filter((m) => m.type === 'volume' && m.name).map((m) => m.name as string),
)

// nom de volume Docker : meme regle que domain.ValidVolumeName cote Go
const VOLUME_RE = /^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,254}$/

const isClone = computed(() => mode.value === 'clone')
const targetVolume = computed(() => (isClone.value ? cloneVolume.value.trim() : sourceVolume.value))

const targetError = computed(() => {
  if (!isClone.value) return ''
  const name = cloneVolume.value.trim()
  if (!name) return ''
  if (!VOLUME_RE.test(name)) return 'Nom de volume invalide (lettres, chiffres, puis . _ - autorises).'
  if (volumeNames.value.includes(name)) return 'Ce volume est deja monte par ce conteneur : il serait ecrase.'
  return ''
})

const canSubmit = computed(
  () => !submitting.value && !!snapshotId.value && !!targetVolume.value && !targetError.value,
)

// le clone n'ecrit pas dans le volume d'origine : arreter la source n'a pas
// de sens, c'est justement le but d'avoir les deux services actifs
watch(mode, (m) => {
  if (m === 'clone') {
    stopContainer.value = false
    if (!cloneVolume.value && sourceVolume.value) cloneVolume.value = `${sourceVolume.value}_clone`
  } else {
    stopContainer.value = true
  }
})

watch(sourceVolume, (v) => {
  if (isClone.value && v) cloneVolume.value = `${v}_clone`
})

onMounted(async () => {
  sourceVolume.value = volumeNames.value[0] || ''
  try {
    storages.value = await api.storage.list()
    if (storages.value.length > 0) storageId.value = storages.value[0]!.id
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
  }
})

watch(storageId, async (id) => {
  snapshots.value = []
  snapshotId.value = ''
  if (!id) return
  snapshotsLoading.value = true
  try {
    // seulement les snapshots de CE conteneur (tag container:<nom>)
    snapshots.value = await api.storage.snapshots(id, [`container:${props.container.name}`])
    if (snapshots.value.length > 0) snapshotId.value = snapshots.value[0]!.id
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
  } finally {
    snapshotsLoading.value = false
  }
})

async function submit(): Promise<void> {
  if (!storageId.value || !canSubmit.value) return
  error.value = ''
  submitting.value = true
  try {
    const rec = await api.restores.start({
      storage_id: storageId.value,
      snapshot_id: snapshotId.value,
      source_volume: isClone.value ? sourceVolume.value : '',
      target_volume: targetVolume.value,
      stop_container: stopContainer.value ? props.container.id : '',
    })
    if (isClone.value) {
      toasts.success(`Clonage #${rec.id} de ${sourceVolume.value} vers ${targetVolume.value} lance`)
      // la stack n'a de sens qu'apres le clone : on la montre sans fermer
      try {
        const res = await api.restores.cloneCompose(
          props.container.id,
          sourceVolume.value,
          targetVolume.value,
        )
        composeYaml.value = res.compose
        return
      } catch (e) {
        error.value = e instanceof Error ? e.message : String(e)
        return
      }
    }
    toasts.success(`Restauration #${rec.id} du volume ${targetVolume.value} lancee`)
    emit('close')
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
  } finally {
    submitting.value = false
  }
}

async function copyCompose(): Promise<void> {
  try {
    await navigator.clipboard.writeText(composeYaml.value)
    toasts.success('docker-compose.clone.yml copie')
  } catch {
    error.value = "Copie impossible : selectionne le texte et copie-le manuellement."
  }
}

function downloadCompose(): void {
  const blob = new Blob([composeYaml.value], { type: 'text/yaml' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = 'docker-compose.clone.yml'
  a.click()
  URL.revokeObjectURL(url)
}
</script>

<template>
  <Modal :title="`Restaurer ${container.name}`" @close="emit('close')">
    <!-- clone lance : la stack a lancer soi-meme -->
    <div v-if="composeYaml" class="space-y-4">
      <p class="rounded-lg border border-emerald-500/30 bg-emerald-500/10 px-3 py-2 text-xs text-emerald-300">
        Clonage lance vers <strong>{{ targetVolume }}</strong
        >. Le volume d'origine n'est pas touche — {{ container.name }} continue de tourner.
      </p>

      <div>
        <label class="label">Stack a lancer une fois la restauration terminee</label>
        <pre
          class="max-h-72 overflow-auto rounded-lg border border-zinc-700 bg-zinc-900 p-3 text-xs leading-relaxed text-zinc-300"
        >{{ composeYaml }}</pre>
        <p class="mt-1 text-xs text-zinc-500">
          Ports et variables d'environnement sont a completer : SDB ne les recopie pas (les ports
          d'origine sont deja pris, et l'environnement contient souvent des secrets).
        </p>
      </div>

      <p v-if="error" class="text-sm text-red-400">{{ error }}</p>

      <div class="flex justify-end gap-2 pt-2">
        <button type="button" class="btn btn-ghost" @click="downloadCompose">Telecharger</button>
        <button type="button" class="btn btn-ghost" @click="copyCompose">Copier</button>
        <button type="button" class="btn btn-primary" @click="emit('close')">Fermer</button>
      </div>
    </div>

    <form v-else class="space-y-4" @submit.prevent="submit">
      <div class="grid grid-cols-2 gap-2">
        <button
          type="button"
          class="rounded-lg border px-3 py-2 text-left text-sm transition"
          :class="
            mode === 'in-place'
              ? 'border-indigo-500 bg-indigo-500/10 text-indigo-200'
              : 'border-zinc-700 text-zinc-400 hover:border-zinc-600'
          "
          @click="mode = 'in-place'"
        >
          En place
          <span class="block text-xs text-zinc-500">Ecrase le volume actuel</span>
        </button>
        <button
          type="button"
          class="rounded-lg border px-3 py-2 text-left text-sm transition"
          :class="
            mode === 'clone'
              ? 'border-indigo-500 bg-indigo-500/10 text-indigo-200'
              : 'border-zinc-700 text-zinc-400 hover:border-zinc-600'
          "
          @click="mode = 'clone'"
        >
          Cloner
          <span class="block text-xs text-zinc-500">Nouveau volume, les deux actifs</span>
        </button>
      </div>

      <p
        v-if="mode === 'in-place'"
        class="rounded-lg border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-xs text-amber-300"
      >
        La restauration ecrase le contenu actuel du volume cible.
      </p>
      <p
        v-else
        class="rounded-lg border border-sky-500/30 bg-sky-500/10 px-3 py-2 text-xs text-sky-300"
      >
        Le snapshot est restaure dans un volume neuf, cree par SDB. Le volume d'origine reste
        intact et {{ container.name }} n'est pas arrete.
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
          Aucun snapshot pour ce conteneur dans ce depot.
        </p>
      </div>

      <div>
        <label class="label" for="restore-volume">
          {{ mode === 'clone' ? 'Volume a cloner' : 'Volume cible' }}
        </label>
        <select id="restore-volume" v-model="sourceVolume" class="input">
          <option v-for="name in volumeNames" :key="name" :value="name">{{ name }}</option>
        </select>
      </div>

      <div v-if="mode === 'clone'">
        <label class="label" for="restore-clone-volume">Nouveau volume</label>
        <input
          id="restore-clone-volume"
          v-model="cloneVolume"
          class="input"
          placeholder="mon_volume_clone"
          autocomplete="off"
        />
        <p v-if="targetError" class="mt-1 text-xs text-red-400">{{ targetError }}</p>
        <p v-else class="mt-1 text-xs text-zinc-500">
          Cree par Docker au lancement de la restauration s'il n'existe pas.
        </p>
      </div>

      <label v-if="mode === 'in-place'" class="flex items-start gap-3 text-sm text-zinc-300">
        <input v-model="stopContainer" type="checkbox" class="mt-0.5 accent-indigo-500" />
        <span>
          Arreter le conteneur pendant la restauration (recommande)
          <span class="block text-xs text-zinc-500">Evite qu'une application n'ecrive pendant la reecriture du volume.</span>
        </span>
      </label>

      <p v-if="error" class="text-sm text-red-400">{{ error }}</p>

      <div class="flex justify-end gap-2 pt-2">
        <button type="button" class="btn btn-ghost" @click="emit('close')">Annuler</button>
        <button
          type="submit"
          class="btn"
          :class="mode === 'clone' ? 'btn-primary' : 'btn-danger'"
          :disabled="!canSubmit"
        >
          {{ submitting ? 'Lancement…' : mode === 'clone' ? 'Cloner' : 'Restaurer' }}
        </button>
      </div>
    </form>
  </Modal>
</template>
