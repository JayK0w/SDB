import { defineStore } from 'pinia'

import { getToken } from '@/lib/api'
import { createReconnectingSocket } from '@/lib/ws'
import { useToastsStore } from './toasts'

const MAX_EVENTS = 200
const TERMINAL_LINGER_MS = 10000
const TERMINAL = new Set(['success', 'warning', 'failed', 'canceled'])

let socket = null

function wsURL() {
  const scheme = window.location.protocol === 'https:' ? 'wss' : 'ws'
  return `${scheme}://${window.location.host}/api/v1/ws/metrics?token=${encodeURIComponent(getToken() || '')}`
}

// Live ProgressEvent stream from the backend hub: the event log, plus a
// per-backup aggregate the dashboard renders as progress cards.
export const useEventsStore = defineStore('events', {
  state: () => ({
    wsStatus: 'idle', // idle | connecting | open | closed
    events: [],
    progress: {}, // backup_id -> { status, percent, bytesDone, totalBytes, message, snapshotId }
    historyDirty: 0, // bumped on terminal events so views can refresh
  }),

  getters: {
    runningBackups: (s) =>
      Object.values(s.progress)
        .filter((p) => !TERMINAL.has(p.status))
        .sort((a, b) => b.backupId - a.backupId),
  },

  actions: {
    connect() {
      if (socket) return
      socket = createReconnectingSocket({
        url: wsURL,
        onMessage: (ev) => this.handle(ev),
        onStatus: (status) => {
          this.wsStatus = status
        },
      })
    },

    disconnect() {
      socket?.close()
      socket = null
      this.wsStatus = 'idle'
      this.progress = {}
      this.events = []
    },

    handle(ev) {
      this.events.unshift(ev)
      if (this.events.length > MAX_EVENTS) this.events.length = MAX_EVENTS

      const id = ev.backup_id
      if (!id) return
      const cur = this.progress[id] || { backupId: id, status: 'running', percent: 0 }

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
        case 'log':
        default:
          break
      }
      this.progress[id] = { ...cur }

      if (ev.type === 'status' && TERMINAL.has(ev.status)) {
        this.historyDirty += 1
        this.notifyTerminal(id, ev.status)
        setTimeout(() => {
          delete this.progress[id]
        }, TERMINAL_LINGER_MS)
      }
    },

    notifyTerminal(id, status) {
      const toasts = useToastsStore()
      switch (status) {
        case 'success':
          toasts.success(`Sauvegarde #${id} terminée avec succès`)
          break
        case 'warning':
          toasts.warning(`Sauvegarde #${id} terminée avec avertissements`)
          break
        case 'canceled':
          toasts.warning(`Sauvegarde #${id} annulée`)
          break
        default:
          toasts.error(`Sauvegarde #${id} échouée`)
      }
    },
  },
})
