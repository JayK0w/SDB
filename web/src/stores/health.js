import { defineStore } from 'pinia'

import { api } from '@/lib/api'

const POLL_INTERVAL_MS = 30000

let pollTimer = null

// Backend + Docker reachability, polled for the North Star indicator.
export const useHealthStore = defineStore('health', {
  state: () => ({
    status: 'unknown', // ok | degraded | down | unknown
    docker: false,
    version: '',
  }),

  actions: {
    async refresh() {
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

    startPolling() {
      if (pollTimer) return
      this.refresh()
      pollTimer = setInterval(() => this.refresh(), POLL_INTERVAL_MS)
    },

    stopPolling() {
      clearInterval(pollTimer)
      pollTimer = null
    },
  },
})
