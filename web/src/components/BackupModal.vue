<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'

import { api } from '@/lib/api'
import type { Container, Hook, Storage } from '@/types'
import { useToastsStore } from '@/stores/toasts'
import Modal from './Modal.vue'

const props = defineProps<{ container: Container }>()
const emit = defineEmits<{ close: [] }>()

const toasts = useToastsStore()

const storages = ref<Storage[]>([])
const storageId = ref<number | null>(null)
const stopContainer = ref(false)
const submitting = ref(false)
const error = ref('')

// divulgation progressive : 2 champs essentiels, le reste derriere
// ce bouton
const showAdvanced = ref(false)
const selectedVolumes = ref<string[]>([])
const preHookCmd = ref('')
const preHookOnFailure = ref<'abort' | 'continue'>('abort')
const postHookCmd = ref('')
const retentionEnabled = ref(false)
const retention = ref({ keep_last: 5, keep_daily: 7, keep_weekly: 4, keep_monthly: 6, prune: true })
const tags = ref('')

const volumeMounts = computed(() =>
  (props.container.mounts || []).filter((m) => m.type === 'volume' || m.type === 'bind'),
)

onMounted(async () => {
  try {
    storages.value = await api.storage.list()
    if (storages.value.length > 0) storageId.value = storages.value[0]!.id
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
  }
})

function buildHook(cmd: string, onFailure: 'abort' | 'continue'): Hook | undefined {
  const trimmed = cmd.trim()
  if (!trimmed) return undefined
  return { command: ['sh', '-c', trimmed], on_failure: onFailure }
}

async function submit(): Promise<void> {
  if (!storageId.value) {
    error.value = 'Choisissez une destination de stockage.'
    return
  }
  error.value = ''
  submitting.value = true
  try {
    const rec = await api.backups.start({
      container_id: props.container.id,
      storage_id: storageId.value,
      stop_container: stopContainer.value,
      volumes: selectedVolumes.value.length > 0 ? selectedVolumes.value : undefined,
      pre_hook: buildHook(preHookCmd.value, preHookOnFailure.value),
      post_hook: buildHook(postHookCmd.value, 'continue'),
      retention: retentionEnabled.value ? retention.value : undefined,
      tags: tags.value ? tags.value.split(',').map((t) => t.trim()).filter(Boolean) : undefined,
    })
    toasts.success(`Sauvegarde #${rec.id} lancée pour ${props.container.name}`)
    emit('close')
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <Modal :title="`Sauvegarder ${container.name}`" @close="emit('close')">
    <form class="space-y-4" @submit.prevent="submit">
      <div>
        <label class="label" for="backup-storage">Destination</label>
        <select id="backup-storage" v-model="storageId" class="input">
          <option v-for="s in storages" :key="s.id" :value="s.id">{{ s.name }} ({{ s.type }})</option>
        </select>
        <p v-if="storages.length === 0" class="mt-1 text-xs text-amber-400">
          Aucun stockage configuré — créez-en un dans l’onglet Stockage.
        </p>
      </div>

      <label class="flex items-start gap-3 text-sm text-zinc-300">
        <input v-model="stopContainer" type="checkbox" class="mt-0.5 accent-indigo-500" />
        <span>
          Sauvegarde à froid (arrêter le conteneur pendant le snapshot)
          <span class="block text-xs text-zinc-500">Garantit la cohérence ; le conteneur est relancé automatiquement.</span>
        </span>
      </label>

      <button
        type="button"
        class="text-sm text-indigo-400 transition-colors hover:text-indigo-300"
        @click="showAdvanced = !showAdvanced"
      >
        {{ showAdvanced ? '▾' : '▸' }} Options avancées
      </button>

      <div v-if="showAdvanced" class="space-y-4 rounded-lg border border-zinc-800 p-4">
        <div v-if="volumeMounts.length > 1">
          <span class="label">Volumes à sauvegarder (tous par défaut)</span>
          <label
            v-for="m in volumeMounts"
            :key="m.destination"
            class="flex items-center gap-2 py-0.5 text-sm text-zinc-300"
          >
            <input
              v-model="selectedVolumes"
              type="checkbox"
              :value="m.name"
              :disabled="!m.name"
              class="accent-indigo-500"
            />
            <span class="font-mono text-xs">{{ m.name || m.source }} → {{ m.destination }}</span>
          </label>
        </div>

        <div>
          <label class="label" for="pre-hook">Hook pré-sauvegarde (dans le conteneur)</label>
          <input
            id="pre-hook"
            v-model="preHookCmd"
            class="input font-mono"
            placeholder="pg_dumpall -U postgres > /var/lib/postgresql/data/dump.sql"
          />
          <label class="mt-2 flex items-center gap-2 text-xs text-zinc-400">
            En cas d’échec :
            <select v-model="preHookOnFailure" class="input w-auto py-1">
              <option value="abort">annuler la sauvegarde</option>
              <option value="continue">continuer (avertissement)</option>
            </select>
          </label>
        </div>

        <div>
          <label class="label" for="post-hook">Hook post-sauvegarde</label>
          <input
            id="post-hook"
            v-model="postHookCmd"
            class="input font-mono"
            placeholder="rm /var/lib/postgresql/data/dump.sql"
          />
        </div>

        <div>
          <label class="flex items-center gap-2 text-sm text-zinc-300">
            <input v-model="retentionEnabled" type="checkbox" class="accent-indigo-500" />
            Appliquer une politique de rétention après succès
          </label>
          <div v-if="retentionEnabled" class="mt-3 grid grid-cols-2 gap-3">
            <div>
              <label class="label">Derniers</label>
              <input v-model.number="retention.keep_last" type="number" min="0" class="input" />
            </div>
            <div>
              <label class="label">Journaliers</label>
              <input v-model.number="retention.keep_daily" type="number" min="0" class="input" />
            </div>
            <div>
              <label class="label">Hebdomadaires</label>
              <input v-model.number="retention.keep_weekly" type="number" min="0" class="input" />
            </div>
            <div>
              <label class="label">Mensuels</label>
              <input v-model.number="retention.keep_monthly" type="number" min="0" class="input" />
            </div>
            <label class="col-span-2 flex items-center gap-2 text-sm text-zinc-300">
              <input v-model="retention.prune" type="checkbox" class="accent-indigo-500" />
              Purger l’espace immédiatement (<span class="font-mono text-xs">--prune</span>)
            </label>
          </div>
        </div>

        <div>
          <label class="label" for="backup-tags">Tags (séparés par des virgules)</label>
          <input id="backup-tags" v-model="tags" class="input" placeholder="prod, quotidien" />
        </div>
      </div>

      <p v-if="error" class="text-sm text-red-400">{{ error }}</p>

      <div class="flex justify-end gap-2 pt-2">
        <button type="button" class="btn btn-ghost" @click="emit('close')">Annuler</button>
        <button type="submit" class="btn btn-primary" :disabled="submitting || !storageId">
          {{ submitting ? 'Lancement…' : 'Lancer la sauvegarde' }}
        </button>
      </div>
    </form>
  </Modal>
</template>
