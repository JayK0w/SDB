const BASE_DELAY_MS = 1000
const MAX_DELAY_MS = 30000

export type SocketStatus = 'connecting' | 'open' | 'closed'

export interface ReconnectingSocketOptions {
  /** Called on every (re)connect so a fresh token is used. */
  url: () => string
  onMessage: (msg: unknown) => void
  onStatus?: (status: SocketStatus) => void
}

export interface ReconnectingSocket {
  close: () => void
}

/**
 * createReconnectingSocket opens a WebSocket that reconnects forever with
 * exponential backoff and jitter (1s, 2s, 4s ... capped at 30s; a
 * successful connection resets the sequence).
 */
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
    // Full jitter on the upper half avoids reconnection stampedes.
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
        /* ignore malformed frames */
      }
    }
    ws.onclose = () => {
      if (closed) return
      onStatus?.('closed')
      timer = setTimeout(connect, nextDelay())
      attempts += 1
    }
    ws.onerror = () => {
      // onclose follows and drives the retry; just make sure it fires.
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
