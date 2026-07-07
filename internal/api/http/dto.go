package httpapi

import (
	"sort"
	"time"

	"github.com/standalone-docker-backup/sdb/internal/domain"
	"github.com/standalone-docker-backup/sdb/internal/usecase"
)

// ---------------------------------------------------------------------------
// Responses
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

// storageDTO never carries secret values: only the credential key names,
// so the UI can show what is configured.
type storageDTO struct {
	ID             int64     `json:"id"`
	Name           string    `json:"name"`
	Type           string    `json:"type"`
	Endpoint       string    `json:"endpoint"`
	CredentialKeys []string  `json:"credential_keys"`
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
		CredentialKeys: keys, CreatedAt: red.CreatedAt, UpdatedAt: red.UpdatedAt,
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
	StartTime      time.Time  `json:"start_time"`
	EndTime        *time.Time `json:"end_time,omitempty"`
	ErrorLog       string     `json:"error_log,omitempty"`
}

func toRecordDTO(rec domain.BackupRecord) backupRecordDTO {
	return backupRecordDTO{
		ID: rec.ID, ContainerID: rec.ContainerID, ContainerName: rec.ContainerName,
		StorageID: rec.StorageID, Status: string(rec.Status), BytesProcessed: rec.BytesProcessed,
		SnapshotID: rec.SnapshotID, StartTime: rec.StartTime, EndTime: rec.EndTime, ErrorLog: rec.ErrorLog,
	}
}

// ---------------------------------------------------------------------------
// Requests
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
}

func (r storageRequest) toDomain(id int64) *domain.StorageConfig {
	return &domain.StorageConfig{
		ID:          id,
		Name:        r.Name,
		Type:        domain.StorageType(r.Type),
		Endpoint:    r.Endpoint,
		Credentials: r.Credentials,
		// ResticPassword stays empty: generated on create, immutable on
		// update (see StorageService).
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
	StorageID     int64  `json:"storage_id" binding:"required"`
	SnapshotID    string `json:"snapshot_id" binding:"required"`
	TargetVolume  string `json:"target_volume" binding:"required"`
	StopContainer string `json:"stop_container"`
}

func (r restoreRequest) toDomain() usecase.RestoreRequest {
	return usecase.RestoreRequest{
		StorageID:     r.StorageID,
		SnapshotID:    r.SnapshotID,
		TargetVolume:  r.TargetVolume,
		StopContainer: r.StopContainer,
	}
}
