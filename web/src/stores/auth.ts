import { defineStore } from 'pinia'

import { api } from '@/lib/api'
import type { User } from '@/types'
import { useEventsStore } from './events'
import { useHealthStore } from './health'

function readStoredUser(): User | null {
  try {
    return JSON.parse(localStorage.getItem('sdb.user') || 'null') as User | null
  } catch {
    return null
  }
}

export const useAuthStore = defineStore('auth', {
  state: () => ({
    token: localStorage.getItem('sdb.token') as string | null,
    user: readStoredUser(),
  }),

  getters: {
    isAuthenticated: (s) => Boolean(s.token),
    isAdmin: (s) => s.user?.role === 'admin',
  },

  actions: {
    async login(username: string, password: string): Promise<void> {
      const res = await api.login(username, password)
      this.token = res.token
      this.user = res.user
      localStorage.setItem('sdb.token', res.token)
      localStorage.setItem('sdb.user', JSON.stringify(res.user))
    },

    logout(): void {
      useEventsStore().disconnect()
      useHealthStore().stopPolling()
      this.token = null
      this.user = null
      localStorage.removeItem('sdb.token')
      localStorage.removeItem('sdb.user')
    },
  },
})
