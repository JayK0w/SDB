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
  /** 0 = depot principal ; sinon ce depot est la copie secondaire de celui-ci */
  copy_of_storage_id: number
  created_at: string
  updated_at: string
}

/**
 * Ecart entre un depot et sa copie secondaire (regle 3-2-1), MESURE dans les
 * deux depots au moment de la requete — jamais un etat memorise.
 */
export interface Replication {
  copy_id: number
  copy_name: string
  source_id: number
  source_name: string
  source_snapshots: number
  copied_snapshots: number
  /** snapshots presents dans le depot principal et absents de la copie */
  pending: number
  oldest_pending?: string
  /** anciennete du PLUS ANCIEN snapshot non copie, 0 si tout est copie */
  lag_seconds: number
  checked_at: string
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
  /** declare ce depot comme copie secondaire d'un autre ; fixe a la creation */
  copy_of_storage_id?: number
}

/**
 * Reponse de CREATION d'un depot. Seule occasion ou le mot de passe du depot
 * est restitue : il n'existe aucune route de lecture, par conception.
 */
export interface StorageCreated extends Storage {
  restic_password: string
  restic_password_warning: string
}

/** Une capacite eprouvee par la sonde de cible. */
export interface ProbeStep {
  name: string
  ok: boolean
  error?: string
}

/**
 * Compte rendu d'un test de cible (POST /storage/test), rendu SANS rien
 * persister. La sonde exerce lister, ecrire, relire et SUPPRIMER : la creation
 * ne teste que les deux premiers, une cle sans droit de suppression passe donc
 * la creation et ne casse qu'a la premiere purge.
 */
export interface ProbeResult {
  ok: boolean
  /** premiere etape en echec ; les suivantes n'ont pas ete tentees */
  failed_step?: string
  steps: ProbeStep[]
  /** chemin du depot de sonde laisse dans la cible ; absent si rien n'a ete cree */
  residue?: string
  residue_note?: string
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
