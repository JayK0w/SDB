<script setup lang="ts">
import { onMounted, ref } from 'vue'

import { api } from '@/lib/api'
import { formatDate } from '@/lib/format'
import type { User } from '@/types'
import { useAuthStore } from '@/stores/auth'
import { useToastsStore } from '@/stores/toasts'

const auth = useAuthStore()
const toasts = useToastsStore()

const users = ref<User[]>([])
const loading = ref(true)
const error = ref('')

const newUsername = ref('')
const newPassword = ref('')
const newRole = ref('user')
const creating = ref(false)

const passwordTarget = ref<User | null>(null)
const passwordValue = ref('')

function errMsg(e: unknown): string {
  return e instanceof Error ? e.message : String(e)
}

async function load(): Promise<void> {
  loading.value = true
  try {
    users.value = await api.users.list()
    error.value = ''
  } catch (e) {
    error.value = errMsg(e)
  } finally {
    loading.value = false
  }
}

onMounted(load)

async function createUser(): Promise<void> {
  creating.value = true
  try {
    await api.users.create({ username: newUsername.value, password: newPassword.value, role: newRole.value })
    toasts.success(`Utilisateur « ${newUsername.value} » créé`)
    newUsername.value = ''
    newPassword.value = ''
    newRole.value = 'user'
    load()
  } catch (e) {
    toasts.error(errMsg(e))
  } finally {
    creating.value = false
  }
}

async function changeRole(user: User, role: string): Promise<void> {
  try {
    await api.users.updateRole(user.id, role)
    toasts.success(`Rôle de ${user.username} mis à jour`)
    load()
  } catch (e) {
    toasts.error(errMsg(e))
    load()
  }
}

async function submitPassword(): Promise<void> {
  if (!passwordTarget.value) return
  try {
    await api.users.updatePassword(passwordTarget.value.id, passwordValue.value)
    toasts.success(`Mot de passe de ${passwordTarget.value.username} mis à jour`)
    passwordTarget.value = null
    passwordValue.value = ''
  } catch (e) {
    toasts.error(errMsg(e))
  }
}

async function remove(user: User): Promise<void> {
  if (!window.confirm(`Supprimer l’utilisateur « ${user.username} » ?`)) return
  try {
    await api.users.remove(user.id)
    toasts.success(`Utilisateur « ${user.username} » supprimé`)
    load()
  } catch (e) {
    toasts.error(errMsg(e))
  }
}
</script>

<template>
  <div class="space-y-6">
    <header>
      <h1 class="text-xl font-semibold text-zinc-100">Utilisateurs</h1>
      <p class="mt-1 text-sm text-zinc-500">Comptes d’accès à SDB. Au moins un administrateur doit subsister.</p>
    </header>

    <form class="card flex flex-wrap items-end gap-3 p-4" @submit.prevent="createUser">
      <div class="min-w-40 flex-1">
        <label class="label" for="new-username">Nom d’utilisateur</label>
        <input id="new-username" v-model="newUsername" class="input" required />
      </div>
      <div class="min-w-40 flex-1">
        <label class="label" for="new-password">Mot de passe (12+ caractères)</label>
        <input id="new-password" v-model="newPassword" type="password" class="input" minlength="12" required />
      </div>
      <div>
        <label class="label" for="new-role">Rôle</label>
        <select id="new-role" v-model="newRole" class="input w-36">
          <option value="user">Utilisateur</option>
          <option value="admin">Administrateur</option>
        </select>
      </div>
      <button type="submit" class="btn btn-primary" :disabled="creating">Créer</button>
    </form>

    <p v-if="loading" class="text-sm text-zinc-500">Chargement…</p>
    <p v-else-if="error" class="text-sm text-red-400">{{ error }}</p>

    <div v-else class="card overflow-hidden">
      <table class="w-full text-sm">
        <thead>
          <tr class="border-b border-zinc-800 text-left text-xs uppercase tracking-wide text-zinc-500">
            <th class="px-4 py-3">Utilisateur</th>
            <th class="px-4 py-3">Rôle</th>
            <th class="px-4 py-3">Créé le</th>
            <th class="px-4 py-3 text-right">Actions</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="u in users" :key="u.id" class="border-b border-zinc-800/50 last:border-0">
            <td class="px-4 py-3 font-medium text-zinc-200">
              {{ u.username }}
              <span v-if="u.id === auth.user?.id" class="ml-1 text-xs text-zinc-500">(vous)</span>
            </td>
            <td class="px-4 py-3">
              <select
                class="input w-40 py-1"
                :value="u.role"
                @change="changeRole(u, ($event.target as HTMLSelectElement).value)"
              >
                <option value="user">Utilisateur</option>
                <option value="admin">Administrateur</option>
              </select>
            </td>
            <td class="px-4 py-3 text-zinc-400">{{ formatDate(u.created_at) }}</td>
            <td class="px-4 py-3 text-right">
              <div class="flex justify-end gap-2">
                <button class="btn btn-ghost" @click="passwordTarget = u">Mot de passe</button>
                <button class="btn btn-danger" :disabled="u.id === auth.user?.id" @click="remove(u)">
                  Supprimer
                </button>
              </div>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <form
      v-if="passwordTarget"
      class="card flex flex-wrap items-end gap-3 p-4"
      @submit.prevent="submitPassword"
    >
      <div class="min-w-48 flex-1">
        <label class="label" :for="'pw-' + passwordTarget.id">
          Nouveau mot de passe pour {{ passwordTarget.username }}
        </label>
        <input
          :id="'pw-' + passwordTarget.id"
          v-model="passwordValue"
          type="password"
          class="input"
          minlength="12"
          required
        />
      </div>
      <button type="submit" class="btn btn-primary">Mettre à jour</button>
      <button type="button" class="btn btn-ghost" @click="passwordTarget = null">Annuler</button>
    </form>
  </div>
</template>
