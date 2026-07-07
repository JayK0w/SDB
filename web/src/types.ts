// API wire types — mirrors the Go DTOs in internal/api/http/dto.go and
// the WebSocket ProgressEvent in internal/domain/progress.go.

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
  start_time: string
  end_time?: string
  error_log?: string
}

export interface RestoreRecord {
  id: number
  storage_id: number
  snapshot_id: string
  target_volume: string
  container_id?: string
  container_name?: string
  status: JobStatus
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
