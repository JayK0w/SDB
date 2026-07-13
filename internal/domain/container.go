package domain

import "time"

type ContainerState string

const (
	ContainerRunning ContainerState = "running"
	ContainerPaused  ContainerState = "paused"
	ContainerExited  ContainerState = "exited"
)

type MountType string

const (
	MountVolume MountType = "volume"
	MountBind   MountType = "bind"
)

type Mount struct {
	Type        MountType
	Name        string // nom du volume, vide pour un bind
	Source      string // chemin hôte
	Destination string // chemin dans le conteneur
	ReadOnly    bool
}

// Container : projection d'un conteneur Docker utilisée par SDB.
type Container struct {
	ID      string
	Name    string
	Image   string
	State   ContainerState
	Mounts  []Mount
	Created time.Time
}

func (c *Container) IsRunning() bool { return c.State == ContainerRunning }

// BackupableMounts : seuls volumes nommés et binds sont sauvegardables
// (tmpfs exclus).
func (c *Container) BackupableMounts() []Mount {
	out := make([]Mount, 0, len(c.Mounts))
	for _, m := range c.Mounts {
		if m.Type == MountVolume || m.Type == MountBind {
			out = append(out, m)
		}
	}
	return out
}
