const BASE = '/api/v1'

export class ApiError extends Error {
  constructor(status, message) {
    super(message)
    this.name = 'ApiError'
    this.status = status
  }
}

export function getToken() {
  return localStorage.getItem('sdb.token')
}

async function request(method, path, body) {
  const headers = {}
  const token = getToken()
  if (token) headers.Authorization = `Bearer ${token}`
  if (body !== undefined) headers['Content-Type'] = 'application/json'

  let res
  try {
    res = await fetch(BASE + path, {
      method,
      headers,
      body: body !== undefined ? JSON.stringify(body) : undefined,
    })
  } catch {
    throw new ApiError(0, 'Serveur injoignable')
  }

  // Expired/invalid session: purge credentials and let the app react.
  if (res.status === 401 && path !== '/auth/login') {
    localStorage.removeItem('sdb.token')
    localStorage.removeItem('sdb.user')
    window.dispatchEvent(new CustomEvent('sdb:unauthorized'))
  }

  if (res.status === 204) return null
  let data = null
  try {
    data = await res.json()
  } catch {
    /* empty or non-JSON body */
  }
  if (!res.ok) {
    throw new ApiError(res.status, data?.error || `Erreur HTTP ${res.status}`)
  }
  return data
}

function qs(params = {}) {
  const search = new URLSearchParams()
  for (const [key, value] of Object.entries(params)) {
    if (value !== undefined && value !== null && value !== '') search.set(key, value)
  }
  const s = search.toString()
  return s ? `?${s}` : ''
}

export const api = {
  login: (username, password) => request('POST', '/auth/login', { username, password }),
  health: () => request('GET', '/health'),

  containers: {
    list: (all = true) => request('GET', `/containers${qs({ all })}`),
    get: (id) => request('GET', `/containers/${encodeURIComponent(id)}`),
  },

  storage: {
    list: () => request('GET', '/storage'),
    create: (payload) => request('POST', '/storage', payload),
    update: (id, payload) => request('PUT', `/storage/${id}`, payload),
    remove: (id) => request('DELETE', `/storage/${id}`),
    check: (id) => request('POST', `/storage/${id}/check`),
    snapshots: (id, tags = []) => {
      const search = new URLSearchParams()
      for (const t of tags) search.append('tag', t)
      const s = search.toString()
      return request('GET', `/storage/${id}/snapshots${s ? `?${s}` : ''}`)
    },
  },

  backups: {
    start: (payload) => request('POST', '/backups', payload),
    cancel: (id) => request('DELETE', `/backups/${id}`),
    history: (params = {}) => request('GET', `/backups/history${qs(params)}`),
    record: (id) => request('GET', `/backups/history/${id}`),
  },

  restores: {
    start: (payload) => request('POST', '/restores', payload),
  },

  users: {
    list: () => request('GET', '/users'),
    create: (payload) => request('POST', '/users', payload),
    updatePassword: (id, password) => request('PUT', `/users/${id}/password`, { password }),
    updateRole: (id, role) => request('PUT', `/users/${id}/role`, { role }),
    remove: (id) => request('DELETE', `/users/${id}`),
  },
}
