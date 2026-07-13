import { defineStore } from 'pinia'

import { api } from '@/lib/api'

const POLL_INTERVAL_MS = 30000

let pollTimer: ReturnType<typeof setInterval> | null = null

// Sante backend + Docker, sondee pour le North Star.
export const useHealthStore = defineStore('health', {
  state: () => ({
    status: 'unknown' as 'ok' | 'degraded' | 'down' | 'unknown',
    docker: false,
    version: '',
  }),

  actions: {
    async refresh(): Promise<void> {
      try {
        const h = await api.health()
        this.status = h.status
        this.docker = h.docker
        this.version = h.version
      } catch {
        this.status = 'down'
        this.docker = false
      }
    },

    startPolling(): void {
      if (pollTimer) return
      this.refresh()
      pollTimer = setInterval(() => this.refresh(), POLL_INTERVAL_MS)
    },

    stopPolling(): void {
      if (pollTimer) clearInterval(pollTimer)
      pollTimer = null
    },
  },
})
