import { defineStore } from 'pinia'

import { api } from '@/lib/api'
import { useEventsStore } from './events'
import { useHealthStore } from './health'

function readStoredUser() {
  try {
    return JSON.parse(localStorage.getItem('sdb.user') || 'null')
  } catch {
    return null
  }
}

export const useAuthStore = defineStore('auth', {
  state: () => ({
    token: localStorage.getItem('sdb.token'),
    user: readStoredUser(),
  }),

  getters: {
    isAuthenticated: (s) => Boolean(s.token),
    isAdmin: (s) => s.user?.role === 'admin',
  },

  actions: {
    async login(username, password) {
      const res = await api.login(username, password)
      this.token = res.token
      this.user = res.user
      localStorage.setItem('sdb.token', res.token)
      localStorage.setItem('sdb.user', JSON.stringify(res.user))
    },

    logout() {
      useEventsStore().disconnect()
      useHealthStore().stopPolling()
      this.token = null
      this.user = null
      localStorage.removeItem('sdb.token')
      localStorage.removeItem('sdb.user')
    },
  },
})
