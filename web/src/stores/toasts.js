import { defineStore } from 'pinia'

const TOAST_TTL_MS = 6000

let nextId = 1

export const useToastsStore = defineStore('toasts', {
  state: () => ({ items: [] }),

  actions: {
    push(kind, message) {
      const id = nextId++
      this.items.push({ id, kind, message })
      setTimeout(() => this.dismiss(id), TOAST_TTL_MS)
    },
    success(message) {
      this.push('success', message)
    },
    warning(message) {
      this.push('warning', message)
    },
    error(message) {
      this.push('error', message)
    },
    dismiss(id) {
      this.items = this.items.filter((t) => t.id !== id)
    },
  },
})
