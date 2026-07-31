package domain

import (
	"regexp"
	"time"
)

// volumeNamePattern : règle Docker pour un volume nommé. Le nom finit en
// source de montage du worker : une valeur non contrainte y ferait passer
// un chemin hôte arbitraire.
var volumeNamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,254}$`)

// ValidVolumeName : nom de volume Docker acceptable.
func ValidVolumeName(name string) bool { return volumeNamePattern.MatchString(name) }

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
