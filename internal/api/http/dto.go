package httpapi

import (
	"sort"
	"time"

	"github.com/standalone-docker-backup/sdb/internal/domain"
	"github.com/standalone-docker-backup/sdb/internal/usecase"
)

// ---------------------------------------------------------------------------
// Reponses
// ---------------------------------------------------------------------------

type userDTO struct {
	ID        int64     `json:"id"`
	Username  string    `json:"username"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
}

func toUserDTO(u domain.User) userDTO {
	return userDTO{ID: u.ID, Username: u.Username, Role: string(u.Role), CreatedAt: u.CreatedAt}
}

type mountDTO struct {
	Type        string `json:"type"`
	Name        string `json:"name,omitempty"`
	Source      string `json:"source"`
	Destination string `json:"destination"`
	ReadOnly    bool   `json:"read_only"`
}

type containerDTO struct {
	ID      string     `json:"id"`
	Name    string     `json:"name"`
	Image   string     `json:"image"`
	State   string     `json:"state"`
	Created time.Time  `json:"created"`
	Mounts  []mountDTO `json:"mounts"`
}

func toContainerDTO(c domain.Container) containerDTO {
	mounts := make([]mountDTO, 0, len(c.Mounts))
	for _, m := range c.Mounts {
		mounts = append(mounts, mountDTO{
			Type:        string(m.Type),
			Name:        m.Name,
			Source:      m.Source,
			Destination: m.Destination,
			ReadOnly:    m.ReadOnly,
		})
	}
	return containerDTO{
		ID: c.ID, Name: c.Name, Image: c.Image,
		State: string(c.State), Created: c.Created, Mounts: mounts,
	}
}

// storageDTO : jamais de valeurs secretes — seuls les noms de cles,
// pour que l UI montre ce qui est configure.
type storageDTO struct {
	ID             int64     `json:"id"`
	Name           string    `json:"name"`
	Type           string    `json:"type"`
	Endpoint       string    `json:"endpoint"`
	CredentialKeys []string  `json:"credential_keys"`
	AppendOnly     bool      `json:"append_only"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

func toStorageDTO(cfg domain.StorageConfig) storageDTO {
	red := cfg.Redacted()
	keys := make([]string, 0, len(red.Credentials))
	for k := range red.Credentials {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return storageDTO{
		ID: red.ID, Name: red.Name, Type: string(red.Type), Endpoint: red.Endpoint,
		CredentialKeys: keys, AppendOnly: red.AppendOnly,
		CreatedAt: red.CreatedAt, UpdatedAt: red.UpdatedAt,
	}
}

type snapshotDTO struct {
	ID       string    `json:"id"`
	ShortID  string    `json:"short_id"`
	Time     time.Time `json:"time"`
	Hostname string    `json:"hostname"`
	Paths    []string  `json:"paths"`
	Tags     []string  `json:"tags"`
}

func toSnapshotDTO(s domain.Snapshot) snapshotDTO {
	return snapshotDTO{ID: s.ID, ShortID: s.ShortID, Time: s.Time, Hostname: s.Hostname, Paths: s.Paths, Tags: s.Tags}
}

type backupRecordDTO struct {
	ID             int64      `json:"id"`
	ContainerID    string     `json:"container_id"`
	ContainerName  string     `json:"container_name"`
	StorageID      int64      `json:"storage_id"`
	Status         string     `json:"status"`
	BytesProcessed int64      `json:"bytes_processed"`
	SnapshotID     string     `json:"snapshot_id,omitempty"`
	TriggeredBy    string     `json:"triggered_by,omitempty"`
	TriggeredByID  int64      `json:"triggered_by_id,omitempty"`
	StartTime      time.Time  `json:"start_time"`
	EndTime        *time.Time `json:"end_time,omitempty"`
	ErrorLog       string     `json:"error_log,omitempty"`
}

func toRecordDTO(rec domain.BackupRecord) backupRecordDTO {
	return backupRecordDTO{
		ID: rec.ID, ContainerID: rec.ContainerID, ContainerName: rec.ContainerName,
		StorageID: rec.StorageID, Status: string(rec.Status), BytesProcessed: rec.BytesProcessed,
		SnapshotID: rec.SnapshotID,
		TriggeredBy: rec.TriggeredBy.Name, TriggeredByID: rec.TriggeredBy.UserID,
		StartTime: rec.StartTime, EndTime: rec.EndTime, ErrorLog: rec.ErrorLog,
	}
}

type restoreRecordDTO struct {
	ID            int64      `json:"id"`
	StorageID     int64      `json:"storage_id"`
	SnapshotID    string     `json:"snapshot_id"`
	SourceVolume  string     `json:"source_volume,omitempty"`
	TargetVolume  string     `json:"target_volume"`
	IsClone       bool       `json:"is_clone"`
	ContainerID   string     `json:"container_id,omitempty"`
	ContainerName string     `json:"container_name,omitempty"`
	Status        string     `json:"status"`
	TriggeredBy   string     `json:"triggered_by,omitempty"`
	TriggeredByID int64      `json:"triggered_by_id,omitempty"`
	StartTime     time.Time  `json:"start_time"`
	EndTime       *time.Time `json:"end_time,omitempty"`
	ErrorLog      string     `json:"error_log,omitempty"`
}

func toRestoreDTO(rec domain.RestoreRecord) restoreRecordDTO {
	return restoreRecordDTO{
		ID: rec.ID, StorageID: rec.StorageID, SnapshotID: rec.SnapshotID,
		SourceVolume: rec.SourceVolume, TargetVolume: rec.TargetVolume, IsClone: rec.IsClone(),
		ContainerID: rec.ContainerID, ContainerName: rec.ContainerName,
		Status: string(rec.Status),
		TriggeredBy: rec.TriggeredBy.Name, TriggeredByID: rec.TriggeredBy.UserID,
		StartTime: rec.StartTime, EndTime: rec.EndTime, ErrorLog: rec.ErrorLog,
	}
}

type scheduleDTO struct {
	ID            int64         `json:"id"`
	Name          string        `json:"name"`
	Cron          string        `json:"cron"`
	Enabled       bool          `json:"enabled"`
	ContainerName string        `json:"container_name"`
	StorageID     int64         `json:"storage_id"`
	Volumes       []string      `json:"volumes"`
	StopContainer bool          `json:"stop_container"`
	PreHook       *hookDTO      `json:"pre_hook,omitempty"`
	PostHook      *hookDTO      `json:"post_hook,omitempty"`
	Retention     *retentionDTO `json:"retention,omitempty"`
	Tags          []string      `json:"tags"`
	LastRunAt     *time.Time    `json:"last_run_at,omitempty"`
	CreatedAt     time.Time     `json:"created_at"`
	UpdatedAt     time.Time     `json:"updated_at"`
}

func toScheduleDTO(s domain.BackupSchedule) scheduleDTO {
	out := scheduleDTO{
		ID: s.ID, Name: s.Name, Cron: s.Cron, Enabled: s.Enabled,
		ContainerName: s.ContainerName, StorageID: s.StorageID,
		Volumes: emptyIfNil(s.Volumes), StopContainer: s.StopContainer,
		Tags: emptyIfNil(s.Tags), LastRunAt: s.LastRunAt,
		CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt,
	}
	out.PreHook = fromDomainHook(s.PreHook)
	out.PostHook = fromDomainHook(s.PostHook)
	out.Retention = fromDomainRetention(s.Retention)
	return out
}

func emptyIfNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func fromDomainHook(h *domain.Hook) *hookDTO {
	if h == nil {
		return nil
	}
	return &hookDTO{
		Command:        h.Command,
		TimeoutSeconds: int(h.Timeout.Seconds()),
		OnFailure:      string(h.OnFailure),
	}
}

func fromDomainRetention(r *domain.RetentionPolicy) *retentionDTO {
	if r == nil {
		return nil
	}
	return &retentionDTO{
		KeepLast: r.KeepLast, KeepHourly: r.KeepHourly, KeepDaily: r.KeepDaily,
		KeepWeekly: r.KeepWeekly, KeepMonthly: r.KeepMonthly, KeepYearly: r.KeepYearly,
		Prune: r.Prune,
	}
}

// ---------------------------------------------------------------------------
// Requetes
// ---------------------------------------------------------------------------

type loginRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

type createUserRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
	Role     string `json:"role" binding:"required"`
}

type updatePasswordRequest struct {
	Password string `json:"password" binding:"required"`
}

type updateRoleRequest struct {
	Role string `json:"role" binding:"required"`
}

type storageRequest struct {
	Name        string            `json:"name" binding:"required"`
	Type        string            `json:"type" binding:"required"`
	Endpoint    string            `json:"endpoint" binding:"required"`
	Credentials map[string]string `json:"credentials"`
	// AppendOnly : cliquet anti-destruction. Activable, jamais desactivable
	// par l API (cf. StorageService.Update).
	AppendOnly bool `json:"append_only"`
	// ResticPassword : optionnel. Vide = SDB en genere un. Le fournir
	// permet de le sequestrer hors de SDB, seule facon de survivre a la
	// perte de sdb.db. Immuable apres creation.
	ResticPassword string `json:"restic_password,omitempty"`
}

func (r storageRequest) toDomain(id int64) *domain.StorageConfig {
	return &domain.StorageConfig{
		ID:          id,
		Name:        r.Name,
		Type:        domain.StorageType(r.Type),
		Endpoint:    r.Endpoint,
		Credentials: r.Credentials,
		AppendOnly:  r.AppendOnly,
		// Vide a la creation = genere par StorageService.Create. En mise a
		// jour, toute valeur differente de l existante est refusee : le mot
		// de passe d un depot est immuable (cf. StorageService.Update).
		ResticPassword: r.ResticPassword,
	}
}

// storageCreatedDTO : reponse de CREATION uniquement. Porte le mot de passe
// du depot en clair, une seule fois dans la vie de la configuration.
//
// C est deliberement le SEUL chemin de lecture : sans sequestre externe, la
// perte de sdb.db rend le depot definitivement illisible, mais un endpoint
// d export permanent donnerait a un admin compromis la totalite des depots.
// Un unique affichage a la creation resout le premier probleme sans ouvrir
// le second.
type storageCreatedDTO struct {
	storageDTO
	ResticPassword string `json:"restic_password"`
	Warning        string `json:"restic_password_warning"`
}

const escrowWarning = "Store this repository password in your secret manager NOW. " +
	"It is shown once and never again. Without it, losing SDB's database makes this repository permanently unreadable."

func toStorageCreatedDTO(cfg domain.StorageConfig) storageCreatedDTO {
	return storageCreatedDTO{
		storageDTO:     toStorageDTO(cfg),
		ResticPassword: cfg.ResticPassword,
		Warning:        escrowWarning,
	}
}

type hookDTO struct {
	Command        []string `json:"command" binding:"required"`
	TimeoutSeconds int      `json:"timeout_seconds"`
	OnFailure      string   `json:"on_failure"` // abort | continue
}

func (h *hookDTO) toDomain() *domain.Hook {
	if h == nil {
		return nil
	}
	return &domain.Hook{
		Command:   h.Command,
		Timeout:   time.Duration(h.TimeoutSeconds) * time.Second,
		OnFailure: domain.HookFailurePolicy(h.OnFailure),
	}
}

type retentionDTO struct {
	KeepLast    int  `json:"keep_last"`
	KeepHourly  int  `json:"keep_hourly"`
	KeepDaily   int  `json:"keep_daily"`
	KeepWeekly  int  `json:"keep_weekly"`
	KeepMonthly int  `json:"keep_monthly"`
	KeepYearly  int  `json:"keep_yearly"`
	Prune       bool `json:"prune"`
}

func (r *retentionDTO) toDomain() *domain.RetentionPolicy {
	if r == nil {
		return nil
	}
	return &domain.RetentionPolicy{
		KeepLast: r.KeepLast, KeepHourly: r.KeepHourly, KeepDaily: r.KeepDaily,
		KeepWeekly: r.KeepWeekly, KeepMonthly: r.KeepMonthly, KeepYearly: r.KeepYearly,
		Prune: r.Prune,
	}
}

type backupRequest struct {
	ContainerID   string        `json:"container_id" binding:"required"`
	StorageID     int64         `json:"storage_id" binding:"required"`
	Volumes       []string      `json:"volumes"`
	StopContainer bool          `json:"stop_container"`
	PreHook       *hookDTO      `json:"pre_hook"`
	PostHook      *hookDTO      `json:"post_hook"`
	Retention     *retentionDTO `json:"retention"`
	Tags          []string      `json:"tags"`
}

func (r backupRequest) toDomain() domain.BackupRequest {
	return domain.BackupRequest{
		ContainerID:   r.ContainerID,
		StorageID:     r.StorageID,
		Volumes:       r.Volumes,
		StopContainer: r.StopContainer,
		PreHook:       r.PreHook.toDomain(),
		PostHook:      r.PostHook.toDomain(),
		Retention:     r.Retention.toDomain(),
		Tags:          r.Tags,
	}
}

type restoreRequest struct {
	StorageID  int64  `json:"storage_id" binding:"required"`
	SnapshotID string `json:"snapshot_id" binding:"required"`
	// SourceVolume : volume tel qu archive. Vide = restauration en place.
	// Different de TargetVolume = clonage vers un volume neuf.
	SourceVolume  string `json:"source_volume"`
	TargetVolume  string `json:"target_volume" binding:"required"`
	StopContainer string `json:"stop_container"`
}

func (r restoreRequest) toDomain() usecase.RestoreRequest {
	return usecase.RestoreRequest{
		StorageID:     r.StorageID,
		SnapshotID:    r.SnapshotID,
		SourceVolume:  r.SourceVolume,
		TargetVolume:  r.TargetVolume,
		StopContainer: r.StopContainer,
	}
}

type scheduleRequest struct {
	Name          string        `json:"name" binding:"required"`
	Cron          string        `json:"cron" binding:"required"`
	Enabled       *bool         `json:"enabled"`
	ContainerName string        `json:"container_name" binding:"required"`
	StorageID     int64         `json:"storage_id" binding:"required"`
	Volumes       []string      `json:"volumes"`
	StopContainer bool          `json:"stop_container"`
	PreHook       *hookDTO      `json:"pre_hook"`
	PostHook      *hookDTO      `json:"post_hook"`
	Retention     *retentionDTO `json:"retention"`
	Tags          []string      `json:"tags"`
}

func (r scheduleRequest) toDomain(id int64) *domain.BackupSchedule {
	enabled := true
	if r.Enabled != nil {
		enabled = *r.Enabled
	}
	return &domain.BackupSchedule{
		ID:            id,
		Name:          r.Name,
		Cron:          r.Cron,
		Enabled:       enabled,
		ContainerName: r.ContainerName,
		StorageID:     r.StorageID,
		Volumes:       r.Volumes,
		StopContainer: r.StopContainer,
		PreHook:       r.PreHook.toDomain(),
		PostHook:      r.PostHook.toDomain(),
		Retention:     r.Retention.toDomain(),
		Tags:          r.Tags,
	}
}
