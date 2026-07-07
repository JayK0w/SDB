<script setup>
import { ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import { useAuthStore } from '@/stores/auth'

const router = useRouter()
const route = useRoute()
const auth = useAuthStore()

const username = ref('')
const password = ref('')
const error = ref('')
const submitting = ref(false)

async function submit() {
  error.value = ''
  submitting.value = true
  try {
    await auth.login(username.value, password.value)
    router.push(typeof route.query.redirect === 'string' ? route.query.redirect : { name: 'dashboard' })
  } catch (e) {
    error.value = e.status === 401 ? 'Identifiants invalides.' : e.message
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <div class="flex min-h-screen items-center justify-center p-4">
    <div class="card w-full max-w-sm p-8">
      <div class="mb-8 text-center">
        <h1 class="text-2xl font-bold text-zinc-100">SDB</h1>
        <p class="mt-1 text-sm text-zinc-500">Standalone Docker Backup</p>
      </div>

      <form class="space-y-4" @submit.prevent="submit">
        <div>
          <label class="label" for="username">Nom d’utilisateur</label>
          <input
            id="username"
            v-model="username"
            class="input"
            autocomplete="username"
            required
            autofocus
          />
        </div>
        <div>
          <label class="label" for="password">Mot de passe</label>
          <input
            id="password"
            v-model="password"
            type="password"
            class="input"
            autocomplete="current-password"
            required
          />
        </div>

        <p v-if="error" class="text-sm text-red-400">{{ error }}</p>

        <button type="submit" class="btn btn-primary w-full" :disabled="submitting">
          {{ submitting ? 'Connexion…' : 'Se connecter' }}
        </button>
      </form>
    </div>
  </div>
</template>
