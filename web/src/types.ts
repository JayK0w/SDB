// Types du contrat API — miroir des DTO Go (internal/api/http/dto.go)
// et du ProgressEvent WebSocket (internal/domain/progress.go).

export type Role = 'admin' | 'user'
export type JobStatus = 'pending' | 'running' | 'success' | 'warning' | 'failed' | 'canceled'
export type StorageType = 'local' | 's3' | 'sftp' | 'rest' | 'b2' | 'azure' | 'gs'
export type EventType = 'log' | 'progress' | 'status' | 'summary' | 'error'

export interface User {
  id: number
  username: string
  role: Role
  created_at: string
}

export interface LoginResponse {
  token: string
  expires_at: string
  user: User
}

export interface Health {
  status: 'ok' | 'degraded'
  docker: boolean
  version: string
}

export interface Mount {
  type: string
  name?: string
  source: string
  destination: string
  read_only: boolean
}

export interface Container {
  id: string
  name: string
  image: string
  state: string
  created: string
  mounts: Mount[]
}

export interface Storage {
  id: number
  name: string
  type: StorageType
  endpoint: string
  credential_keys: string[]
  /** depot protege : SDB refuse forget/prune et sa suppression */
  append_only: boolean
  created_at: string
  updated_at: string
}

export interface Snapshot {
  id: string
  short_id: string
  time: string
  hostname: string
  paths: string[]
  tags: string[]
}

export interface Hook {
  command: string[]
  timeout_seconds?: number
  on_failure?: 'abort' | 'continue'
}

export interface Retention {
  keep_last?: number
  keep_hourly?: number
  keep_daily?: number
  keep_weekly?: number
  keep_monthly?: number
  keep_yearly?: number
  prune?: boolean
}

export interface BackupRecord {
  id: number
  container_id: string
  container_name: string
  storage_id: number
  status: JobStatus
  bytes_processed: number
  snapshot_id?: string
  /** auteur du run ; "system:schedule:<nom>" pour un run planifie */
  triggered_by?: string
  triggered_by_id?: number
  start_time: string
  end_time?: string
  error_log?: string
}

export interface RestoreRecord {
  id: number
  storage_id: number
  snapshot_id: string
  /** volume tel qu'archivé ; absent = restauration en place */
  source_volume?: string
  target_volume: string
  /** true = restauré dans un volume neuf, l'original est intact */
  is_clone: boolean
  container_id?: string
  container_name?: string
  status: JobStatus
  /** auteur du run ; "system:schedule:<nom>" pour un run planifie */
  triggered_by?: string
  triggered_by_id?: number
  start_time: string
  end_time?: string
  error_log?: string
}

export interface Schedule {
  id: number
  name: string
  cron: string
  enabled: boolean
  container_name: string
  storage_id: number
  volumes: string[]
  stop_container: boolean
  pre_hook?: Hook
  post_hook?: Hook
  retention?: Retention
  tags: string[]
  last_run_at?: string
  created_at: string
  updated_at: string
}

export interface BackupPayload {
  container_id: string
  storage_id: number
  volumes?: string[]
  stop_container?: boolean
  pre_hook?: Hook
  post_hook?: Hook
  retention?: Retention
  tags?: string[]
}

export interface RestorePayload {
  storage_id: number
  snapshot_id: string
  /** volume tel qu'archivé ; omis = restauration en place */
  source_volume?: string
  target_volume: string
  stop_container?: string
}

export interface SchedulePayload {
  name: string
  cron: string
  enabled?: boolean
  container_name: string
  storage_id: number
  volumes?: string[]
  stop_container?: boolean
  pre_hook?: Hook
  post_hook?: Hook
  retention?: Retention
  tags?: string[]
}

export interface StoragePayload {
  name: string
  type: StorageType
  endpoint: string
  credentials?: Record<string, string>
  /** activable uniquement : l'API refuse de le repasser a false */
  append_only?: boolean
  /** optionnel : vide = SDB en genere un. Le fournir permet de le sequestrer. */
  restic_password?: string
}

/**
 * Reponse de CREATION d'un depot. Seule occasion ou le mot de passe du depot
 * est restitue : il n'existe aucune route de lecture, par conception.
 */
export interface StorageCreated extends Storage {
  restic_password: string
  restic_password_warning: string
}

export interface ProgressEvent {
  backup_id?: number
  restore_id?: number
  container?: string
  type: EventType
  time: string
  status?: JobStatus
  message?: string
  percent?: number
  bytes_done?: number
  total_bytes?: number
  files_done?: number
  total_files?: number
  snapshot_id?: string
}
