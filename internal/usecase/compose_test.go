package usecase

import (
	"errors"
	"strings"
	"testing"

	"github.com/standalone-docker-backup/sdb/internal/domain"
)

func cloneComposeContainer() *domain.Container {
	return &domain.Container{
		ID:    "c1",
		Name:  "postgres",
		Image: "postgres:16",
		State: domain.ContainerRunning,
		Mounts: []domain.Mount{
			{Type: domain.MountVolume, Name: "pgdata", Destination: "/var/lib/postgresql/data"},
			{Type: domain.MountVolume, Name: "pgconf", Destination: "/etc/postgresql"},
			{Type: domain.MountBind, Source: "/srv/init", Destination: "/docker-entrypoint-initdb.d"},
		},
	}
}

func TestRenderCloneComposeMapsTargetVolumeToSourceMountPoint(t *testing.T) {
	out, err := renderCloneCompose(cloneComposeContainer(), "pgdata", "pgdata_clone")
	if err != nil {
		t.Fatalf("renderCloneCompose() error: %v", err)
	}

	// le volume clone doit atterrir au point de montage du volume d'origine
	if !strings.Contains(out, `- "pgdata_clone:/var/lib/postgresql/data"`) {
		t.Fatalf("clone volume not mapped to the source mount point:\n%s", out)
	}
	if !strings.Contains(out, "postgres-clone:") {
		t.Fatalf("service name missing:\n%s", out)
	}
	if !strings.Contains(out, `image: "postgres:16"`) {
		t.Fatalf("image missing:\n%s", out)
	}
	if !strings.Contains(out, "external: true") {
		t.Fatalf("volume must be declared external (SDB already created it):\n%s", out)
	}
}

// Monter les autres volumes du service d'origine ferait ecrire deux
// conteneurs dans les memes donnees : ils doivent sortir commentes.
func TestRenderCloneComposeCommentsOutSharedMounts(t *testing.T) {
	out, err := renderCloneCompose(cloneComposeContainer(), "pgdata", "pgdata_clone")
	if err != nil {
		t.Fatalf("renderCloneCompose() error: %v", err)
	}
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.Contains(trimmed, "pgconf:") || strings.Contains(trimmed, "/srv/init") {
			t.Fatalf("shared mount is active instead of commented out: %q\n%s", line, out)
		}
	}
	if !strings.Contains(out, `# - "pgconf:/etc/postgresql"`) {
		t.Fatalf("shared volume should still be listed as a comment:\n%s", out)
	}
}

// L'environnement du conteneur d'origine contient regulierement des secrets :
// il ne doit jamais etre recopie dans un fichier.
func TestRenderCloneComposeNeverEmitsConcreteEnvOrPorts(t *testing.T) {
	out, err := renderCloneCompose(cloneComposeContainer(), "pgdata", "pgdata_clone")
	if err != nil {
		t.Fatalf("renderCloneCompose() error: %v", err)
	}
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, "environment:") || strings.HasPrefix(trimmed, "ports:") {
			t.Fatalf("env/ports must stay commented, got active line %q:\n%s", line, out)
		}
	}
}

func TestRenderCloneComposeRejectsBadInput(t *testing.T) {
	c := cloneComposeContainer()

	if _, err := renderCloneCompose(c, "pgdata", "../escape"); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("bad target name: err = %v, want ErrInvalidInput", err)
	}
	if _, err := renderCloneCompose(c, "nope", "nope_clone"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown source volume: err = %v, want ErrNotFound", err)
	}
	noImage := cloneComposeContainer()
	noImage.Image = ""
	if _, err := renderCloneCompose(noImage, "pgdata", "pgdata_clone"); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("missing image: err = %v, want ErrInvalidInput", err)
	}
}
