package restic

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/standalone-docker-backup/sdb/internal/domain"
)

// captureRuntime : retient la WorkerSpec au lieu de lancer un conteneur.
type captureRuntime struct {
	spec domain.WorkerSpec
}

func (c *captureRuntime) Ping(context.Context) error { return nil }
func (c *captureRuntime) List(context.Context, bool) ([]domain.Container, error) {
	return nil, nil
}
func (c *captureRuntime) Get(context.Context, string) (*domain.Container, error) {
	return nil, domain.ErrNotFound
}
func (c *captureRuntime) Stop(context.Context, string, time.Duration) error { return nil }
func (c *captureRuntime) Start(context.Context, string) error              { return nil }
func (c *captureRuntime) Exec(context.Context, string, []string, time.Duration) (*domain.ExecResult, error) {
	return &domain.ExecResult{}, nil
}
func (c *captureRuntime) RemoveVolume(context.Context, string) error { return nil }

func (c *captureRuntime) RunWorker(_ context.Context, spec domain.WorkerSpec, _, _ io.Writer) (int, error) {
	c.spec = spec
	return 0, nil
}

func testStorage() *domain.StorageConfig {
	return &domain.StorageConfig{
		ID: 1, Name: "local", Type: domain.StorageLocal,
		Endpoint: "/backups", ResticPassword: "pw",
	}
}

// findDataMount : le montage projeté sous /sdb/data (l'autre est le dépôt).
func findDataMount(t *testing.T, spec domain.WorkerSpec) domain.Mount {
	t.Helper()
	for _, m := range spec.Mounts {
		if strings.HasPrefix(m.Destination, dataMountRoot+"/") {
			return m
		}
	}
	t.Fatalf("no mount under %s in %+v", dataMountRoot, spec.Mounts)
	return domain.Mount{}
}

func includePath(t *testing.T, cmd []string) string {
	t.Helper()
	for i, arg := range cmd {
		if arg == "--include" && i+1 < len(cmd) {
			return cmd[i+1]
		}
	}
	t.Fatalf("no --include in %v", cmd)
	return ""
}

// Le clonage est exactement ceci : le chemin --include vient du volume
// SOURCE (seul présent dans le snapshot) tandis que le volume monté en
// écriture est la CIBLE. Dériver --include de la cible ne restaurerait
// rien, le chemin étant absent de l'archive.
func TestRestoreIntoDifferentVolumeIncludesSourcePath(t *testing.T) {
	rt := &captureRuntime{}
	e := New(rt, "restic/restic:latest")

	if err := e.Restore(context.Background(), testStorage(), domain.RestoreSpec{
		SnapshotID: "snap1", SourceVolume: "pgdata", TargetVolume: "pgdata_clone",
	}, nil); err != nil {
		t.Fatalf("Restore() error: %v", err)
	}

	want := dataMountRoot + "/pgdata"
	if got := includePath(t, rt.spec.Cmd); got != want {
		t.Fatalf("--include = %q, want %q (path as archived, not the target name)", got, want)
	}
	m := findDataMount(t, rt.spec)
	if m.Name != "pgdata_clone" {
		t.Fatalf("mounted volume = %q, want the target pgdata_clone", m.Name)
	}
	if m.Destination != want {
		t.Fatalf("mount destination = %q, want %q", m.Destination, want)
	}
	if m.ReadOnly {
		t.Fatal("restore target must be mounted read-write")
	}
}

// Source vide = restauration en place : le comportement historique ne doit
// pas bouger.
func TestRestoreInPlaceFallsBackToTargetPath(t *testing.T) {
	rt := &captureRuntime{}
	e := New(rt, "restic/restic:latest")

	if err := e.Restore(context.Background(), testStorage(), domain.RestoreSpec{
		SnapshotID: "snap1", TargetVolume: "pgdata",
	}, nil); err != nil {
		t.Fatalf("Restore() error: %v", err)
	}

	want := dataMountRoot + "/pgdata"
	if got := includePath(t, rt.spec.Cmd); got != want {
		t.Fatalf("--include = %q, want %q", got, want)
	}
	m := findDataMount(t, rt.spec)
	if m.Name != "pgdata" || m.Destination != want {
		t.Fatalf("mount = %+v, want pgdata at %s", m, want)
	}
}

// `restic check` seul ne valide que la structure du dépôt : sans
// --read-data-subset, une corruption de pack n'est découverte qu'au moment
// de restaurer.
func TestCheckReadsDataWhenSubsetConfigured(t *testing.T) {
	rt := &captureRuntime{}
	e := New(rt, "restic/restic:latest", WithReadDataSubset("5%"))

	if err := e.Check(context.Background(), testStorage()); err != nil {
		t.Fatalf("Check() error: %v", err)
	}
	var found bool
	for _, arg := range rt.spec.Cmd {
		if arg == "--read-data-subset=5%" {
			found = true
		}
	}
	if !found {
		t.Fatalf("check must relay the read-data subset, got %v", rt.spec.Cmd)
	}
}

func TestCheckStaysStructuralWithoutSubset(t *testing.T) {
	rt := &captureRuntime{}
	e := New(rt, "restic/restic:latest")

	if err := e.Check(context.Background(), testStorage()); err != nil {
		t.Fatalf("Check() error: %v", err)
	}
	for _, arg := range rt.spec.Cmd {
		if strings.HasPrefix(arg, "--read-data") {
			t.Fatalf("no subset configured, yet check asked to read data: %v", rt.spec.Cmd)
		}
	}
}

// Sans --verify, une restauration réussie prouve seulement que restic n'a pas
// planté : c'est le drapeau qui fait recomparer les empreintes écrites au
// snapshot, donc qui transforme l'exercice en preuve de restaurabilité.
func TestRestoreForwardsVerifyFlag(t *testing.T) {
	rt := &captureRuntime{}
	e := New(rt, "restic/restic:latest")

	if err := e.Restore(context.Background(), testStorage(), domain.RestoreSpec{
		SnapshotID: "snap1", SourceVolume: "pgdata", TargetVolume: "sdb-verify-x", Verify: true,
	}, nil); err != nil {
		t.Fatalf("Restore() error: %v", err)
	}
	var found bool
	for _, arg := range rt.spec.Cmd {
		if arg == "--verify" {
			found = true
		}
	}
	if !found {
		t.Fatalf("verification restore must pass --verify, got %v", rt.spec.Cmd)
	}
}

// Une restauration ordinaire ne doit pas payer le coût de la relecture.
func TestRestoreOmitsVerifyByDefault(t *testing.T) {
	rt := &captureRuntime{}
	e := New(rt, "restic/restic:latest")

	if err := e.Restore(context.Background(), testStorage(), domain.RestoreSpec{
		SnapshotID: "snap1", TargetVolume: "pgdata",
	}, nil); err != nil {
		t.Fatalf("Restore() error: %v", err)
	}
	for _, arg := range rt.spec.Cmd {
		if arg == "--verify" {
			t.Fatalf("ordinary restore should not verify: %v", rt.spec.Cmd)
		}
	}
}

func TestRestoreRequiresSnapshotAndTarget(t *testing.T) {
	e := New(&captureRuntime{}, "restic/restic:latest")

	if err := e.Restore(context.Background(), testStorage(), domain.RestoreSpec{
		SnapshotID: "", SourceVolume: "pgdata", TargetVolume: "pgdata_clone",
	}, nil); err == nil {
		t.Fatal("empty snapshot id should be rejected")
	}
	if err := e.Restore(context.Background(), testStorage(), domain.RestoreSpec{
		SnapshotID: "snap1", SourceVolume: "pgdata", TargetVolume: "",
	}, nil); err == nil {
		t.Fatal("empty target volume should be rejected")
	}
}
