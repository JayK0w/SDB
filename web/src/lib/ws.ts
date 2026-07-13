const BASE_DELAY_MS = 1000
const MAX_DELAY_MS = 30000

export type SocketStatus = 'connecting' | 'open' | 'closed'

export interface ReconnectingSocketOptions {
  /** appelée à chaque (re)connexion : token toujours frais */
  url: () => string
  onMessage: (msg: unknown) => void
  onStatus?: (status: SocketStatus) => void
}

export interface ReconnectingSocket {
  close: () => void
}

// WebSocket à reconnexion infinie : backoff exponentiel avec jitter
// (1s, 2s, 4s… plafonné à 30s), remis à zéro à chaque connexion réussie.
export function createReconnectingSocket({
  url,
  onMessage,
  onStatus,
}: ReconnectingSocketOptions): ReconnectingSocket {
  let ws: WebSocket | null = null
  let attempts = 0
  let closed = false
  let timer: ReturnType<typeof setTimeout> | undefined

  function nextDelay(): number {
    const exp = Math.min(MAX_DELAY_MS, BASE_DELAY_MS * 2 ** attempts)
    // jitter : evite les tempetes de reconnexion
    return exp / 2 + Math.random() * (exp / 2)
  }

  function connect(): void {
    if (closed) return
    onStatus?.('connecting')
    ws = new WebSocket(url())

    ws.onopen = () => {
      attempts = 0
      onStatus?.('open')
    }
    ws.onmessage = (event: MessageEvent) => {
      try {
        onMessage(JSON.parse(event.data as string))
      } catch {
        /* trame malformee ignoree */
      }
    }
    ws.onclose = () => {
      if (closed) return
      onStatus?.('closed')
      timer = setTimeout(connect, nextDelay())
      attempts += 1
    }
    ws.onerror = () => {
      // onclose suit et pilote le retry
      ws?.close()
    }
  }

  connect()

  return {
    close() {
      closed = true
      clearTimeout(timer)
      ws?.close()
    },
  }
}
