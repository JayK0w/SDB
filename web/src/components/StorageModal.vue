<script setup lang="ts">
import { computed, ref, watch } from 'vue'

import { api } from '@/lib/api'
import type { ProbeResult, Storage, StorageType } from '@/types'
import { useToastsStore } from '@/stores/toasts'
import Modal from './Modal.vue'

const emit = defineEmits<{ close: []; created: [] }>()

// depots principaux existants : seuls candidats comme source d'une copie
// secondaire (les chaines de copies sont refusees par l'API)
// defaultCopyOf : ouvre directement le formulaire en mode « copie secondaire
// de ce depot », quand l'utilisateur vient du conseil affiche sur la liste
const props = defineProps<{ sources?: Storage[]; defaultCopyOf?: number }>()

const copyCandidates = computed(() => (props.sources ?? []).filter((s) => !s.copy_of_storage_id))

const toasts = useToastsStore()

interface TypeInfo {
  value: StorageType
  label: string
  placeholder: string
  hint?: string
  credentials: { key: string; secret: boolean }[]
}

// chaque backend suggere ses cles d identifiants ; valeurs chiffrees
// au repos (AES-256-GCM) cote backend
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
const appendOnly = ref(false)
// 0 = depot principal. Fixe a la creation : c'est a l'init que le depot herite
// des parametres de decoupage de sa source, jamais apres.
const copyOf = ref(props.defaultCopyOf ?? 0)
// mot de passe restitue une seule fois par l'API : tant qu'il est affiche,
// la modale reste ouverte, sinon il est perdu pour toujours
const createdPassword = ref('')
const createdName = ref('')
const credentials = ref<{ key: string; value: string }[]>([])
const submitting = ref(false)
const error = ref('')

// sonde de cible : verdict par droit, rendu sans rien persister
const probing = ref(false)
const probe = ref<ProbeResult | null>(null)

// Ce que chaque etape prouve, dans l'ordre ou les droits cassent.
const STEP_LABELS: Record<string, string> = {
  'copy-pair': 'Paire copie/source compatible',
  init: 'Lister la cible et y écrire',
  write: 'Écrire des données, poser un verrou',
  read: 'Relire le snapshot qui vient d’être écrit',
  delete: 'Supprimer paquets, index et snapshot',
}

function stepLabel(name: string): string {
  return STEP_LABELS[name] ?? name
}

const typeInfo = computed(() => TYPES.find((t) => t.value === type.value) ?? TYPES[0]!)

// Un verdict ne vaut que pour la configuration qui l'a produit. Le laisser
// affiche apres une modification ferait valider une cible que personne n'a
// testee — exactement l'illusion de securite que cette sonde doit dissiper.
watch([type, endpoint, copyOf, credentials], () => {
  probe.value = null
}, { deep: true })

// payload partage par la sonde et la creation : les tester differemment
// reviendrait a valider autre chose que ce qu'on va creer
function payload() {
  const creds: Record<string, string> = {}
  for (const { key, value } of credentials.value) {
    if (key.trim() && value !== '') creds[key.trim()] = value
  }
  return {
    name: name.value,
    type: type.value,
    endpoint: endpoint.value,
    credentials: creds,
    append_only: appendOnly.value,
    copy_of_storage_id: Number(copyOf.value) || 0,
  }
}

async function runTest(): Promise<void> {
  error.value = ''
  probing.value = true
  try {
    probe.value = await api.storage.test(payload())
  } catch (e) {
    probe.value = null
    error.value = e instanceof Error ? e.message : String(e)
  } finally {
    probing.value = false
  }
}

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

async function copyPassword(): Promise<void> {
  try {
    await navigator.clipboard.writeText(createdPassword.value)
    toasts.success('Mot de passe copié')
  } catch {
    error.value = 'Copie impossible : sélectionne le texte et copie-le manuellement.'
  }
}

async function submit(): Promise<void> {
  error.value = ''
  submitting.value = true
  try {
    const created = await api.storage.create(payload())
    toasts.success(`Stockage « ${created.name} » créé, dépôt restic initialisé`)
    emit('created')
    // on n'emet PAS close : le mot de passe doit etre sequestre avant
    createdName.value = created.name
    createdPassword.value = created.restic_password
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <Modal title="Nouveau stockage" @close="emit('close')">
    <!-- Unique restitution du mot de passe du depot. Sans sequestre externe,
         la perte de la base de SDB rend ce depot definitivement illisible. -->
    <div v-if="createdPassword" class="space-y-4">
      <p class="rounded-lg border border-amber-500/40 bg-amber-500/10 px-3 py-2 text-sm text-amber-200">
        <strong>Note ce mot de passe maintenant.</strong>
        Il n'est affiché qu'une seule fois — aucune route de l'API ne permet de le relire.
        Sans lui, la perte de la base de SDB rend le dépôt
        <span class="font-mono">{{ createdName }}</span> définitivement illisible.
      </p>

      <div>
        <label class="label">Mot de passe du dépôt restic</label>
        <pre class="overflow-x-auto rounded-lg border border-zinc-700 bg-zinc-900 p-3 font-mono text-sm text-zinc-200">{{ createdPassword }}</pre>
        <p class="mt-1 text-xs text-zinc-500">
          Range-le dans ton gestionnaire de secrets. Il permet d'ouvrir le dépôt avec
          <span class="font-mono">restic</span> directement, sans SDB.
        </p>
      </div>

      <p v-if="error" class="text-sm text-red-400">{{ error }}</p>

      <div class="flex justify-end gap-2 pt-2">
        <button type="button" class="btn btn-ghost" @click="copyPassword">Copier</button>
        <button type="button" class="btn btn-primary" @click="emit('close')">
          C'est séquestré, fermer
        </button>
      </div>
    </div>

    <form v-else class="space-y-4" @submit.prevent="submit">
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

      <div v-if="copyCandidates.length">
        <label class="label" for="storage-copy-of">Rôle</label>
        <select id="storage-copy-of" v-model="copyOf" class="input">
          <option :value="0">Dépôt principal (reçoit les sauvegardes)</option>
          <option v-for="src in copyCandidates" :key="src.id" :value="src.id">
            Copie secondaire de « {{ src.name }} »
          </option>
        </select>
        <p class="mt-1 text-xs text-zinc-500">
          Une copie secondaire n'est jamais sauvegardée directement : elle reçoit les snapshots de sa
          source après chaque sauvegarde, puis à chaque passe de réconciliation. Elle est initialisée
          depuis sa source — ce rattachement ne peut plus changer ensuite.
        </p>
        <p v-if="copyOf" class="mt-2 rounded-lg border border-sky-500/40 bg-sky-500/10 px-3 py-2 text-xs text-sky-200">
          Les snapshots <strong>déjà présents</strong> dans le dépôt source seront recopiés dès la
          création, en tâche de fond : activer la seconde copie ne protège pas que les sauvegardes à
          venir. Selon le volume et le lien, cette première recopie peut durer longtemps ; son
          avancement se lit dans « État des copies ».
        </p>
      </div>

      <label class="flex items-start gap-3 rounded-lg border border-zinc-800 px-3 py-2 text-sm text-zinc-300">
        <input v-model="appendOnly" type="checkbox" class="mt-0.5 accent-indigo-500" />
        <span>
          Dépôt append-only
          <span class="block text-xs text-zinc-500">
            SDB refusera d'y appliquer une rétention (<code>forget</code>/<code>prune</code>) et de
            supprimer cette cible. Irréversible depuis l'interface : la protection ne peut plus être
            levée par l'API une fois activée.
          </span>
        </span>
      </label>

      <!-- Verdict de la sonde. Rendu par DROIT, parce que la panne utile a
           connaitre n'est pas « ca marche / ca ne marche pas » mais « lequel
           manque » : une cle sans droit de suppression passe la creation et ne
           casse qu'a la premiere purge, sur la copie secondaire. -->
      <div
        v-if="probe"
        class="rounded-lg border px-3 py-2 text-xs"
        :class="probe.ok
          ? 'border-emerald-500/40 bg-emerald-500/10 text-emerald-200'
          : 'border-red-500/40 bg-red-500/10 text-red-200'"
      >
        <p class="mb-2 font-medium">
          {{ probe.ok ? 'Cible éprouvée : les quatre droits sont accordés.' : 'Cible inutilisable en l’état.' }}
        </p>
        <ul class="space-y-1">
          <li v-for="step in probe.steps" :key="step.name" class="flex gap-2">
            <span aria-hidden="true">{{ step.ok ? '✓' : '✕' }}</span>
            <span>
              {{ stepLabel(step.name) }}
              <span v-if="step.error" class="mt-0.5 block break-all font-mono text-[11px] opacity-90">
                {{ step.error }}
              </span>
            </span>
          </li>
        </ul>
        <p v-if="!probe.ok" class="mt-2 opacity-80">
          Les étapes suivantes n'ont pas été tentées : leur absence ne dit rien sur elles.
        </p>
        <p v-if="probe.residue" class="mt-2 break-all opacity-80">
          Résidu laissé dans la cible — restic ne sait pas détruire un dépôt, <code>config</code> et
          <code>keys/</code> restent (quelques centaines d'octets) :
          <span class="font-mono">{{ probe.residue }}</span>
        </p>
      </div>

      <p class="text-xs text-zinc-500">
        Le mot de passe du dépôt restic est généré automatiquement et stocké chiffré (AES-256-GCM).
      </p>

      <p v-if="error" class="text-sm text-red-400">{{ error }}</p>

      <div class="flex justify-end gap-2 pt-2">
        <button type="button" class="btn btn-ghost" @click="emit('close')">Annuler</button>
        <!-- Eprouver AVANT de creer : la creation lance `restic init`, qui
             n'exerce que lister et ecrire. -->
        <button
          type="button"
          class="btn btn-ghost"
          :disabled="probing || submitting || !endpoint"
          @click="runTest"
        >
          {{ probing ? 'Test en cours…' : 'Tester la cible' }}
        </button>
        <button type="submit" class="btn btn-primary" :disabled="submitting || probing">
          {{ submitting ? 'Initialisation du dépôt…' : 'Créer' }}
        </button>
      </div>
    </form>
  </Modal>
</template>
