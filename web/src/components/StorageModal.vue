<script setup>
import { computed, ref } from 'vue'

import { api } from '@/lib/api'
import { useToastsStore } from '@/stores/toasts'
import Modal from './Modal.vue'

const emit = defineEmits(['close', 'created'])

const toasts = useToastsStore()

const name = ref('')
const type = ref('local')
const endpoint = ref('')
const credentials = ref([]) // [{ key, value }]
const submitting = ref(false)
const error = ref('')

const TYPES = [
  { value: 'local', label: 'Local (chemin hôte)', placeholder: '/mnt/backups/sdb' },
  { value: 's3', label: 'S3 compatible', placeholder: 's3.amazonaws.com/mon-bucket/sdb' },
  { value: 'sftp', label: 'SFTP', placeholder: 'user@serveur:/backups/sdb' },
  { value: 'rest', label: 'Serveur REST restic', placeholder: 'https://backup.example.com/sdb' },
]

const endpointPlaceholder = computed(
  () => TYPES.find((t) => t.value === type.value)?.placeholder || '',
)

function suggestCredentials(newType) {
  if (newType === 's3' && credentials.value.length === 0) {
    credentials.value = [
      { key: 'AWS_ACCESS_KEY_ID', value: '' },
      { key: 'AWS_SECRET_ACCESS_KEY', value: '' },
    ]
  }
}

function addCredential() {
  credentials.value.push({ key: '', value: '' })
}

function removeCredential(index) {
  credentials.value.splice(index, 1)
}

async function submit() {
  error.value = ''
  submitting.value = true
  try {
    const creds = {}
    for (const { key, value } of credentials.value) {
      if (key.trim()) creds[key.trim()] = value
    }
    const created = await api.storage.create({
      name: name.value,
      type: type.value,
      endpoint: endpoint.value,
      credentials: creds,
    })
    toasts.success(`Stockage « ${created.name} » créé, dépôt restic initialisé`)
    emit('created')
    emit('close')
  } catch (e) {
    error.value = e.message
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <Modal title="Nouveau stockage" @close="emit('close')">
    <form class="space-y-4" @submit.prevent="submit">
      <div>
        <label class="label" for="storage-name">Nom</label>
        <input id="storage-name" v-model="name" class="input" placeholder="NAS hors-site" required />
      </div>

      <div>
        <label class="label" for="storage-type">Type</label>
        <select id="storage-type" v-model="type" class="input" @change="suggestCredentials(type)">
          <option v-for="t in TYPES" :key="t.value" :value="t.value">{{ t.label }}</option>
        </select>
      </div>

      <div>
        <label class="label" for="storage-endpoint">Point de terminaison</label>
        <input
          id="storage-endpoint"
          v-model="endpoint"
          class="input font-mono"
          :placeholder="endpointPlaceholder"
          required
        />
      </div>

      <div>
        <div class="mb-1 flex items-center justify-between">
          <span class="label mb-0">Identifiants (chiffrés au repos)</span>
          <button type="button" class="text-xs text-indigo-400 hover:text-indigo-300" @click="addCredential">
            + Ajouter
          </button>
        </div>
        <div v-for="(cred, i) in credentials" :key="i" class="mb-2 flex gap-2">
          <input v-model="cred.key" class="input flex-1 font-mono text-xs" placeholder="CLÉ" />
          <input v-model="cred.value" type="password" class="input flex-1 font-mono text-xs" placeholder="valeur" />
          <button type="button" class="btn btn-ghost px-2" aria-label="Retirer" @click="removeCredential(i)">✕</button>
        </div>
      </div>

      <p class="text-xs text-zinc-500">
        Le mot de passe du dépôt restic est généré automatiquement et stocké chiffré (AES-256-GCM).
      </p>

      <p v-if="error" class="text-sm text-red-400">{{ error }}</p>

      <div class="flex justify-end gap-2 pt-2">
        <button type="button" class="btn btn-ghost" @click="emit('close')">Annuler</button>
        <button type="submit" class="btn btn-primary" :disabled="submitting">
          {{ submitting ? 'Initialisation du dépôt…' : 'Créer' }}
        </button>
      </div>
    </form>
  </Modal>
</template>
