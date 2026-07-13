import { defineStore } from 'pinia'

import { getToken } from '@/lib/api'
import { createReconnectingSocket, type ReconnectingSocket, type SocketStatus } from '@/lib/ws'
import type { JobStatus, ProgressEvent } from '@/types'
import { useToastsStore } from './toasts'

const TERMINAL_LINGER_MS = 10000
const TERMINAL: ReadonlySet<string> = new Set(['success', 'warning', 'failed', 'canceled'])

export interface JobProgress {
  kind: 'backup' | 'restore'
  id: number
  container?: string
  status: JobStatus
  percent: number
  bytesDone?: number
  totalBytes?: number
  message?: string
  snapshotId?: string
  lastError?: string
}

let socket: ReconnectingSocket | null = null

function wsURL(): string {
  const scheme = window.location.protocol === 'https:' ? 'wss' : 'ws'
  return `${scheme}://${window.location.host}/api/v1/ws/metrics?token=${encodeURIComponent(getToken() || '')}`
}

// Flux ProgressEvent du hub : agrégat par job (cartes de progression) +
// compteur historyDirty que les vues observent pour se rafraîchir.
export const useEventsStore = defineStore('events', {
  state: () => ({
    wsStatus: 'idle' as SocketStatus | 'idle',
    progress: {} as Record<string, JobProgress>,
    historyDirty: 0, // incrémenté à chaque événement terminal
  }),

  getters: {
    runningJobs: (s): JobProgress[] =>
      Object.values(s.progress)
        .filter((p) => !TERMINAL.has(p.status))
        .sort((a, b) => b.id - a.id),
  },

  actions: {
    connect(): void {
      if (socket) return
      socket = createReconnectingSocket({
        url: wsURL,
        onMessage: (msg) => this.handle(msg as ProgressEvent),
        onStatus: (status) => {
          this.wsStatus = status
        },
      })
    },

    disconnect(): void {
      socket?.close()
      socket = null
      this.wsStatus = 'idle'
      this.progress = {}
    },

    handle(ev: ProgressEvent): void {
      const kind: JobProgress['kind'] = ev.restore_id ? 'restore' : 'backup'
      const id = ev.restore_id || ev.backup_id
      if (!id) return
      const key = `${kind}:${id}`
      const cur: JobProgress = this.progress[key] || { kind, id, status: 'running', percent: 0 }
      if (ev.container) cur.container = ev.container

      switch (ev.type) {
        case 'progress':
          cur.percent = ev.percent ?? cur.percent
          cur.bytesDone = ev.bytes_done ?? cur.bytesDone
          cur.totalBytes = ev.total_bytes ?? cur.totalBytes
          break
        case 'summary':
          cur.percent = 100
          cur.snapshotId = ev.snapshot_id || cur.snapshotId
          break
        case 'status':
          cur.status = ev.status || cur.status
          cur.message = ev.message || cur.message
          break
        case 'error':
          cur.lastError = ev.message
          break
        default:
          break
      }
      this.progress[key] = { ...cur }

      if (ev.type === 'status' && ev.status && TERMINAL.has(ev.status)) {
        this.historyDirty += 1
        this.notifyTerminal(kind, id, ev.status)
        setTimeout(() => {
          delete this.progress[key]
        }, TERMINAL_LINGER_MS)
      }
    },

    notifyTerminal(kind: JobProgress['kind'], id: number, status: JobStatus): void {
      const toasts = useToastsStore()
      const label = kind === 'restore' ? `Restauration #${id}` : `Sauvegarde #${id}`
      switch (status) {
        case 'success':
          toasts.success(`${label} terminée avec succès`)
          break
        case 'warning':
          toasts.warning(`${label} terminée avec avertissements`)
          break
        case 'canceled':
          toasts.warning(`${label} annulée`)
          break
        default:
          toasts.error(`${label} échouée`)
      }
    },
  },
})
