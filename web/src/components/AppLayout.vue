<script setup lang="ts">
import { onMounted, onUnmounted } from 'vue'
import { useRouter } from 'vue-router'

import { useAuthStore } from '@/stores/auth'
import { useEventsStore } from '@/stores/events'
import { useHealthStore } from '@/stores/health'
import NorthStar from './NorthStar.vue'

const router = useRouter()
const auth = useAuthStore()
const events = useEventsStore()
const health = useHealthStore()

onMounted(() => {
  health.startPolling()
  events.connect()
})
onUnmounted(() => {
  health.stopPolling()
})

function logout(): void {
  auth.logout()
  router.push({ name: 'login' })
}

const links = [
  { name: 'dashboard', label: 'Tableau de bord' },
  { name: 'schedules', label: 'Planifications' },
  { name: 'history', label: 'Historique' },
  { name: 'storage', label: 'Stockage' },
]
</script>

<template>
  <div class="flex min-h-screen">
    <aside class="flex w-60 shrink-0 flex-col border-r border-zinc-800 bg-zinc-900/50 p-4">
      <!-- North Star: global state, always top-left. -->
      <NorthStar class="mb-6" />

      <nav class="flex flex-1 flex-col gap-1">
        <RouterLink
          v-for="link in links"
          :key="link.name"
          :to="{ name: link.name }"
          class="rounded-lg px-3 py-2 text-sm text-zinc-400 transition-colors hover:bg-zinc-800 hover:text-zinc-100"
          exact-active-class="bg-zinc-800 text-zinc-100 font-medium"
        >
          {{ link.label }}
        </RouterLink>
        <RouterLink
          v-if="auth.isAdmin"
          :to="{ name: 'users' }"
          class="rounded-lg px-3 py-2 text-sm text-zinc-400 transition-colors hover:bg-zinc-800 hover:text-zinc-100"
          exact-active-class="bg-zinc-800 text-zinc-100 font-medium"
        >
          Utilisateurs
        </RouterLink>
      </nav>

      <div class="mt-4 border-t border-zinc-800 pt-4">
        <p class="truncate text-sm text-zinc-300">{{ auth.user?.username }}</p>
        <p class="mb-2 text-xs text-zinc-500">{{ auth.isAdmin ? 'Administrateur' : 'Utilisateur' }}</p>
        <button class="btn btn-ghost w-full" @click="logout">Se déconnecter</button>
      </div>
    </aside>

    <main class="min-w-0 flex-1 p-6 lg:p-8">
      <RouterView />
    </main>
  </div>
</template>
