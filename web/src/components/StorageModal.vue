<script setup lang="ts">
import { computed, ref } from 'vue'

import { api } from '@/lib/api'
import type { StorageType } from '@/types'
import { useToastsStore } from '@/stores/toasts'
import Modal from './Modal.vue'

const emit = defineEmits<{ close: []; created: [] }>()

const toasts = useToastsStore()

interface TypeInfo {
  value: StorageType
  label: string
  placeholder: string
  hint?: string
  credentials: { key: string; secret: boolean }[]
}

// Each backend suggests the credential keys restic expects; values are
// encrypted at rest by the backend (AES-256-GCM).
const TYPES: TypeInfo[] = [
  {
    value: 'local',
    label: 'Local (chemin hôte)',
    placeholder: '/mnt/backups/sdb',
    credentials: [],
  },
  {
    value: 's3',
    label: 'S3 compatible (AWS, MinIO, Scaleway…)',
    placeholder: 's3.amazonaws.com/mon-bucket/sdb',
    credentials: [
      { key: 'AWS_ACCESS_KEY_ID', secret: false },
      { key: 'AWS_SECRET_ACCESS_KEY', secret: true },
    ],
  },
  {
    value: 'b2',
    label: 'Backblaze B2',
    placeholder: 'mon-bucket:/sdb',
    credentials: [
      { key: 'B2_ACCOUNT_ID', secret: false },
      { key: 'B2_ACCOUNT_KEY', secret: true },
    ],
  },
  {
    value: 'azure',
    label: 'Azure Blob Storage',
    placeholder: 'mon-conteneur:/sdb',
    credentials: [
      { key: 'AZURE_ACCOUNT_NAME', secret: false },
      { key: 'AZURE_ACCOUNT_KEY', secret: true },
    ],
  },
  {
    value: 'gs',
    label: 'Google Cloud Storage',
    placeholder: 'mon-bucket:/sdb',
    hint: 'Collez le JSON du compte de service dans GOOGLE_CREDENTIALS_JSON.',
    credentials: [
      { key: 'GOOGLE_PROJECT_ID', secret: false },
      { key: 'GOOGLE_CREDENTIALS_JSON', secret: true },
    ],
  },
  {
    value: 'sftp',
    label: 'SFTP (autre serveur via SSH)',
    placeholder: 'user@serveur:/backups/sdb',
    hint: 'Collez la clé privée SSH (OpenSSH) dans SSH_PRIVATE_KEY ; SSH_PORT optionnel.',
    credentials: [
      { key: 'SSH_PRIVATE_KEY', secret: true },
      { key: 'SSH_PORT', secret: false },
    ],
  },
  {
    value: 'rest',
    label: 'Serveur REST restic',
    placeholder: 'https://user:pass@backup.example.com/sdb',
    credentials: [],
  },
]

const name = ref('')
const type = ref<StorageType>('local')
const endpoint = ref('')
const credentials = ref<{ key: string; value: string }[]>([])
const submitting = ref(false)
const error = ref('')

const typeInfo = computed(() => TYPES.find((t) => t.value === type.value) ?? TYPES[0]!)

function onTypeChange(): void {
  credentials.value = typeInfo.value.credentials.map((c) => ({ key: c.key, value: '' }))
}

function isSecret(key: string): boolean {
  return typeInfo.value.credentials.find((c) => c.key === key)?.secret ?? true
}

function addCredential(): void {
  credentials.value.push({ key: '', value: '' })
}

function removeCredential(index: number): void {
  credentials.value.splice(index, 1)
}

async function submit(): Promise<void> {
  error.value = ''
  submitting.value = true
  try {
    const creds: Record<string, string> = {}
    for (const { key, value } of credentials.value) {
      if (key.trim() && value !== '') creds[key.trim()] = value
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
    error.value = e instanceof Error ? e.message : String(e)
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
        <select id="storage-type" v-model="type" class="input" @change="onTypeChange">
          <option v-for="t in TYPES" :key="t.value" :value="t.value">{{ t.label }}</option>
        </select>
        <p v-if="typeInfo.hint" class="mt-1 text-xs text-zinc-500">{{ typeInfo.hint }}</p>
      </div>

      <div>
        <label class="label" for="storage-endpoint">Point de terminaison</label>
        <input
          id="storage-endpoint"
          v-model="endpoint"
          class="input font-mono"
          :placeholder="typeInfo.placeholder"
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
          <textarea
            v-if="cred.key === 'SSH_PRIVATE_KEY' || cred.key === 'GOOGLE_CREDENTIALS_JSON'"
            v-model="cred.value"
            rows="3"
            class="input flex-1 font-mono text-xs"
            placeholder="valeur (multiligne)"
          />
          <input
            v-else
            v-model="cred.value"
            :type="isSecret(cred.key) ? 'password' : 'text'"
            class="input flex-1 font-mono text-xs"
            placeholder="valeur"
          />
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
