<script setup lang="ts">
import { onMounted, onUnmounted } from 'vue'
import { useRouter } from 'vue-router'

import { useAuthStore } from '@/stores/auth'
import ToastHost from '@/components/ToastHost.vue'

const router = useRouter()
const auth = useAuthStore()

function onUnauthorized(): void {
  auth.logout()
  router.push({ name: 'login' })
}

onMounted(() => window.addEventListener('sdb:unauthorized', onUnauthorized))
onUnmounted(() => window.removeEventListener('sdb:unauthorized', onUnauthorized))
</script>

<template>
  <RouterView />
  <ToastHost />
</template>
