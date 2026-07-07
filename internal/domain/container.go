package domain

import "time"

// ContainerState mirrors the Docker container lifecycle states SDB cares
// about; other daemon states are carried through as-is.
type ContainerState string

const (
	ContainerRunning ContainerState = "running"
	ContainerPaused  ContainerState = "paused"
	ContainerExited  ContainerState = "exited"
)

// MountType restricts backups to mounts that map to real data: named
// volumes and bind mounts. tmpfs and friends are excluded.
type MountType string

const (
	MountVolume MountType = "volume"
	MountBind   MountType = "bind"
)

// Mount is a volume or bind mount attached to a container.
type Mount struct {
	Type        MountType
	Name        string // volume name; empty for bind mounts
	Source      string // host path (bind) or volume source
	Destination string // path inside the container
	ReadOnly    bool
}

// Container is the projection of a Docker container used by SDB.
type Container struct {
	ID      string
	Name    string
	Image   string
	State   ContainerState
	Labels  map[string]string
	Mounts  []Mount
	Created time.Time
}

func (c *Container) IsRunning() bool { return c.State == ContainerRunning }

// BackupableMounts filters mounts down to those SDB can snapshot.
func (c *Container) BackupableMounts() []Mount {
	out := make([]Mount, 0, len(c.Mounts))
	for _, m := range c.Mounts {
		if m.Type == MountVolume || m.Type == MountBind {
			out = append(out, m)
		}
	}
	return out
}
