const BASE_DELAY_MS = 1000
const MAX_DELAY_MS = 30000

/**
 * createReconnectingSocket opens a WebSocket that reconnects forever with
 * exponential backoff and jitter (1s, 2s, 4s ... capped at 30s; a
 * successful connection resets the sequence).
 *
 * @param {Object} opts
 * @param {() => string} opts.url      called on every (re)connect so a fresh token is used
 * @param {(msg: any) => void} opts.onMessage  parsed JSON payloads
 * @param {(status: 'connecting'|'open'|'closed') => void} [opts.onStatus]
 * @returns {{ close: () => void }}
 */
export function createReconnectingSocket({ url, onMessage, onStatus }) {
  let ws = null
  let attempts = 0
  let closed = false
  let timer = null

  function nextDelay() {
    const exp = Math.min(MAX_DELAY_MS, BASE_DELAY_MS * 2 ** attempts)
    // Full jitter on the upper half avoids reconnection stampedes.
    return exp / 2 + Math.random() * (exp / 2)
  }

  function connect() {
    if (closed) return
    onStatus?.('connecting')
    ws = new WebSocket(url())

    ws.onopen = () => {
      attempts = 0
      onStatus?.('open')
    }
    ws.onmessage = (event) => {
      try {
        onMessage(JSON.parse(event.data))
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
      ws.close()
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
