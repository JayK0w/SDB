import { defineStore } from 'pinia'

const TOAST_TTL_MS = 6000

export type ToastKind = 'success' | 'warning' | 'error'

export interface Toast {
  id: number
  kind: ToastKind
  message: string
}

let nextId = 1

export const useToastsStore = defineStore('toasts', {
  state: () => ({ items: [] as Toast[] }),

  actions: {
    push(kind: ToastKind, message: string): void {
      const id = nextId++
      this.items.push({ id, kind, message })
      setTimeout(() => this.dismiss(id), TOAST_TTL_MS)
    },
    success(message: string): void {
      this.push('success', message)
    },
    warning(message: string): void {
      this.push('warning', message)
    },
    error(message: string): void {
      this.push('error', message)
    },
    dismiss(id: number): void {
      this.items = this.items.filter((t) => t.id !== id)
    },
  },
})
