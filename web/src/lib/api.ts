import type {
  BackupPayload,
  BackupRecord,
  Container,
  Health,
  LoginResponse,
  Replication,
  RestorePayload,
  RestoreRecord,
  Schedule,
  SchedulePayload,
  Snapshot,
  Storage,
  StorageCreated,
  StoragePayload,
  User,
} from '@/types'

const BASE = '/api/v1'

export class ApiError extends Error {
  status: number

  constructor(status: number, message: string) {
    super(message)
    this.name = 'ApiError'
    this.status = status
  }
}

export function getToken(): string | null {
  return localStorage.getItem('sdb.token')
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = {}
  const token = getToken()
  if (token) headers.Authorization = `Bearer ${token}`
  if (body !== undefined) headers['Content-Type'] = 'application/json'

  let res: Response
  try {
    res = await fetch(BASE + path, {
      method,
      headers,
      body: body !== undefined ? JSON.stringify(body) : undefined,
    })
  } catch {
    throw new ApiError(0, 'Serveur injoignable')
  }

  // session expiree/invalide : purge et signal global
  if (res.status === 401 && path !== '/auth/login') {
    localStorage.removeItem('sdb.token')
    localStorage.removeItem('sdb.user')
    window.dispatchEvent(new CustomEvent('sdb:unauthorized'))
  }

  if (res.status === 204) return undefined as T
  let data: unknown = null
  try {
    data = await res.json()
  } catch {
    /* corps vide ou non-JSON */
  }
  if (!res.ok) {
    const message = (data as { error?: string } | null)?.error || `Erreur HTTP ${res.status}`
    throw new ApiError(res.status, message)
  }
  return data as T
}

interface QueryParams {
  [key: string]: string | number | boolean | undefined
}

function qs(params: QueryParams = {}): string {
  const search = new URLSearchParams()
  for (const [key, value] of Object.entries(params)) {
    if (value !== undefined && value !== null && value !== '') search.set(key, String(value))
  }
  const s = search.toString()
  return s ? `?${s}` : ''
}

export const api = {
  login: (username: string, password: string) =>
    request<LoginResponse>('POST', '/auth/login', { username, password }),
  health: () => request<Health>('GET', '/health'),

  containers: {
    list: (all = true) => request<Container[]>('GET', `/containers${qs({ all })}`),
    get: (id: string) => request<Container>('GET', `/containers/${encodeURIComponent(id)}`),
  },

  storage: {
    list: () => request<Storage[]>('GET', '/storage'),
    create: (payload: StoragePayload) => request<StorageCreated>('POST', '/storage', payload),
    update: (id: number, payload: StoragePayload) => request<Storage>('PUT', `/storage/${id}`, payload),
    remove: (id: number) => request<void>('DELETE', `/storage/${id}`),
    check: (id: number) => request<{ status: string }>('POST', `/storage/${id}/check`),
    // restauration REELLE du dernier snapshot dans un volume jetable : la
    // seule preuve qu'une sauvegarde est restaurable
    verify: (id: number) => request<{ status: string }>('POST', `/storage/${id}/verify`),
    // interroge les deux depots de chaque paire : action a la demande, pas un
    // rafraichissement de fond
    replication: () =>
      request<{ replication: Replication[]; error?: string }>('GET', '/replication'),
    replicate: (id: number) => request<{ status: string }>('POST', `/storage/${id}/replicate`),
    snapshots: (id: number, tags: string[] = []) => {
      const search = new URLSearchParams()
      for (const t of tags) search.append('tag', t)
      const s = search.toString()
      return request<Snapshot[]>('GET', `/storage/${id}/snapshots${s ? `?${s}` : ''}`)
    },
  },

  backups: {
    start: (payload: BackupPayload) => request<BackupRecord>('POST', '/backups', payload),
    cancel: (id: number) => request<void>('DELETE', `/backups/${id}`),
    history: (params: QueryParams = {}) =>
      request<BackupRecord[]>('GET', `/backups/history${qs(params)}`),
    record: (id: number) => request<BackupRecord>('GET', `/backups/history/${id}`),
  },

  restores: {
    start: (payload: RestorePayload) => request<RestoreRecord>('POST', '/restores', payload),
    cancel: (id: number) => request<void>('DELETE', `/restores/${id}`),
    history: (params: QueryParams = {}) =>
      request<RestoreRecord[]>('GET', `/restores/history${qs(params)}`),
    cloneCompose: (containerId: string, sourceVolume: string, targetVolume: string) =>
      request<{ compose: string }>(
        'GET',
        `/restores/clone-compose${qs({
          container_id: containerId,
          source_volume: sourceVolume,
          target_volume: targetVolume,
        })}`,
      ),
  },

  schedules: {
    list: () => request<Schedule[]>('GET', '/schedules'),
    create: (payload: SchedulePayload) => request<Schedule>('POST', '/schedules', payload),
    update: (id: number, payload: SchedulePayload) =>
      request<Schedule>('PUT', `/schedules/${id}`, payload),
    remove: (id: number) => request<void>('DELETE', `/schedules/${id}`),
    run: (id: number) => request<BackupRecord>('POST', `/schedules/${id}/run`),
  },

  users: {
    list: () => request<User[]>('GET', '/users'),
    create: (payload: { username: string; password: string; role: string }) =>
      request<User>('POST', '/users', payload),
    updatePassword: (id: number, password: string) =>
      request<void>('PUT', `/users/${id}/password`, { password }),
    updateRole: (id: number, role: string) => request<void>('PUT', `/users/${id}/role`, { role }),
    remove: (id: number) => request<void>('DELETE', `/users/${id}`),
  },
}
