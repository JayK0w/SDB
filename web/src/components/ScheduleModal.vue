<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'

import { api } from '@/lib/api'
import type { Container, Hook, Schedule, Storage } from '@/types'
import { useToastsStore } from '@/stores/toasts'
import Modal from './Modal.vue'

const props = defineProps<{ schedule?: Schedule | null }>()
const emit = defineEmits<{ close: []; saved: [] }>()

const toasts = useToastsStore()
const editing = computed(() => Boolean(props.schedule))

const containers = ref<Container[]>([])
const storages = ref<Storage[]>([])
const submitting = ref(false)
const error = ref('')

const name = ref(props.schedule?.name ?? '')
const containerName = ref(props.schedule?.container_name ?? '')
const storageId = ref<number | null>(props.schedule?.storage_id ?? null)
const enabled = ref(props.schedule?.enabled ?? true)
const stopContainer = ref(props.schedule?.stop_container ?? false)

const PRESETS = [
  { label: 'Toutes les heures', cron: '0 * * * *' },
  { label: 'Chaque nuit à 3h (UTC)', cron: '0 3 * * *' },
  { label: 'Chaque dimanche à 4h (UTC)', cron: '0 4 * * 0' },
  { label: 'Personnalisé…', cron: '' },
]
const initialPreset = PRESETS.find((p) => p.cron === props.schedule?.cron)
const preset = ref(initialPreset?.cron ?? (props.schedule ? '' : '0 3 * * *'))
const customCron = ref(initialPreset ? '' : (props.schedule?.cron ?? ''))
const cron = computed(() => preset.value || customCron.value)

const showAdvanced = ref(Boolean(props.schedule?.pre_hook || props.schedule?.post_hook || props.schedule?.retention))
const preHookCmd = ref(props.schedule?.pre_hook?.command?.[2] ?? '')
const postHookCmd = ref(props.schedule?.post_hook?.command?.[2] ?? '')
const retentionEnabled = ref(Boolean(props.schedule?.retention))
const retention = ref({
  keep_last: props.schedule?.retention?.keep_last ?? 5,
  keep_daily: props.schedule?.retention?.keep_daily ?? 7,
  keep_weekly: props.schedule?.retention?.keep_weekly ?? 4,
  keep_monthly: props.schedule?.retention?.keep_monthly ?? 6,
  prune: props.schedule?.retention?.prune ?? true,
})

onMounted(async () => {
  try {
    const [allContainers, allStorages] = await Promise.all([api.containers.list(true), api.storage.list()])
    containers.value = allContainers
    // copies secondaires exclues : une planification ne peut pas y sauvegarder
    storages.value = allStorages.filter((s) => !s.copy_of_storage_id)
    if (!containerName.value && containers.value.length > 0) containerName.value = containers.value[0]!.name
    if (!storageId.value && storages.value.length > 0) storageId.value = storages.value[0]!.id
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
  if (!storageId.value || !cron.value) {
    error.value = 'Renseignez la planification cron et la destination.'
    return
  }
  error.value = ''
  submitting.value = true
  try {
    const payload = {
      name: name.value,
      cron: cron.value,
      enabled: enabled.value,
      container_name: containerName.value,
      storage_id: storageId.value,
      stop_container: stopContainer.value,
      pre_hook: buildHook(preHookCmd.value, 'abort'),
      post_hook: buildHook(postHookCmd.value, 'continue'),
      retention: retentionEnabled.value ? retention.value : undefined,
    }
    if (editing.value && props.schedule) {
      await api.schedules.update(props.schedule.id, payload)
      toasts.success(`Planification « ${name.value} » mise à jour`)
    } else {
      await api.schedules.create(payload)
      toasts.success(`Planification « ${name.value} » créée`)
    }
    emit('saved')
    emit('close')
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <Modal :title="editing ? `Modifier ${schedule?.name}` : 'Nouvelle planification'" @close="emit('close')">
    <form class="space-y-4" @submit.prevent="submit">
      <div>
        <label class="label" for="sched-name">Nom</label>
        <input id="sched-name" v-model="name" class="input" placeholder="postgres-nightly" required />
      </div>

      <div class="grid grid-cols-2 gap-3">
        <div>
          <label class="label" for="sched-container">Conteneur</label>
          <select id="sched-container" v-model="containerName" class="input">
            <option v-for="ct in containers" :key="ct.id" :value="ct.name">{{ ct.name }}</option>
          </select>
        </div>
        <div>
          <label class="label" for="sched-storage">Destination</label>
          <select id="sched-storage" v-model="storageId" class="input">
            <option v-for="s in storages" :key="s.id" :value="s.id">{{ s.name }}</option>
          </select>
        </div>
      </div>

      <div>
        <label class="label" for="sched-preset">Fréquence</label>
        <select id="sched-preset" v-model="preset" class="input">
          <option v-for="p in PRESETS" :key="p.label" :value="p.cron">{{ p.label }}</option>
        </select>
        <input
          v-if="!preset"
          v-model="customCron"
          class="input mt-2 font-mono"
          placeholder="minute heure jour mois jour-semaine (ex: 30 2 * * 1-5)"
          required
        />
        <p class="mt-1 text-xs text-zinc-500">Expression cron 5 champs, évaluée en heure UTC.</p>
      </div>

      <div class="flex gap-6">
        <label class="flex items-center gap-2 text-sm text-zinc-300">
          <input v-model="enabled" type="checkbox" class="accent-indigo-500" />
          Activée
        </label>
        <label class="flex items-center gap-2 text-sm text-zinc-300">
          <input v-model="stopContainer" type="checkbox" class="accent-indigo-500" />
          Sauvegarde à froid
        </label>
      </div>

      <button
        type="button"
        class="text-sm text-indigo-400 transition-colors hover:text-indigo-300"
        @click="showAdvanced = !showAdvanced"
      >
        {{ showAdvanced ? '▾' : '▸' }} Options avancées
      </button>

      <div v-if="showAdvanced" class="space-y-4 rounded-lg border border-zinc-800 p-4">
        <div>
          <label class="label" for="sched-pre-hook">Hook pré-sauvegarde</label>
          <input id="sched-pre-hook" v-model="preHookCmd" class="input font-mono" placeholder="pg_dumpall -U postgres > /var/lib/postgresql/data/dump.sql" />
        </div>
        <div>
          <label class="label" for="sched-post-hook">Hook post-sauvegarde</label>
          <input id="sched-post-hook" v-model="postHookCmd" class="input font-mono" placeholder="rm /var/lib/postgresql/data/dump.sql" />
        </div>
        <div>
          <label class="flex items-center gap-2 text-sm text-zinc-300">
            <input v-model="retentionEnabled" type="checkbox" class="accent-indigo-500" />
            Rétention après succès
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
              Purger l’espace immédiatement
            </label>
          </div>
        </div>
      </div>

      <p v-if="error" class="text-sm text-red-400">{{ error }}</p>

      <div class="flex justify-end gap-2 pt-2">
        <button type="button" class="btn btn-ghost" @click="emit('close')">Annuler</button>
        <button type="submit" class="btn btn-primary" :disabled="submitting">
          {{ submitting ? 'Enregistrement…' : editing ? 'Mettre à jour' : 'Créer' }}
        </button>
      </div>
    </form>
  </Modal>
</template>
