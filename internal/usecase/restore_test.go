package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/standalone-docker-backup/sdb/internal/domain"
)

func newRestoreFixture(t *testing.T) (*RestoreService, *fakeRuntime, *fakeEngine, *memRestores, *capturePublisher) {
	t.Helper()
	runtime := &fakeRuntime{container: &domain.Container{
		ID:    "c1",
		Name:  "postgres",
		State: domain.ContainerRunning,
		Mounts: []domain.Mount{
			{Type: domain.MountVolume, Name: "pgdata", Source: "pgdata", Destination: "/var/lib/postgresql/data"},
		},
	}}
	storages := newMemStorages()
	if err := storages.Create(context.Background(), &domain.StorageConfig{
		Name: "local", Type: domain.StorageLocal, Endpoint: "/backups", ResticPassword: "pw",
	}); err != nil {
		t.Fatal(err)
	}
	engine := &fakeEngine{}
	history := newMemRestores()
	pub := &capturePublisher{}
	svc := NewRestoreService(runtime, engine, storages, history, pub, discardLogger())
	return svc, runtime, engine, history, pub
}

func waitRestoreTerminal(t *testing.T, history *memRestores, id int64) domain.RestoreRecord {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		rec, err := history.GetByID(context.Background(), id)
		if err == nil && rec.Status.Terminal() {
			return *rec
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("restore %d did not reach a terminal status", id)
	return domain.RestoreRecord{}
}

func TestRestoreSuccessWithContainerStopRestart(t *testing.T) {
	svc, runtime, engine, history, pub := newRestoreFixture(t)

	rec, err := svc.Start(context.Background(), RestoreRequest{
		StorageID: 1, SnapshotID: "snap1", TargetVolume: "pgdata", StopContainer: "c1",
	})
	if err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	final := waitRestoreTerminal(t, history, rec.ID)
	if final.Status != domain.BackupSuccess {
		t.Fatalf("Status = %s (%s), want success", final.Status, final.ErrorLog)
	}
	if final.ContainerName != "postgres" || final.EndTime == nil {
		t.Fatalf("record incomplete: %+v", final)
	}
	if len(runtime.stopped()) != 1 || len(runtime.started()) != 1 {
		t.Fatalf("stop/start = %v/%v, want one each", runtime.stopped(), runtime.started())
	}
	if len(engine.restores) != 1 || engine.restores[0] != "snap1->pgdata" {
		t.Fatalf("engine restores = %v", engine.restores)
	}

	var sawStamped bool
	for _, ev := range pub.all() {
		if ev.RestoreID == rec.ID && ev.Type == domain.EventStatus && ev.Status == domain.BackupSuccess {
			sawStamped = true
		}
		if ev.BackupID != 0 {
			t.Fatalf("restore event leaked a backup id: %+v", ev)
		}
	}
	if !sawStamped {
		t.Fatal("no terminal status event stamped with the restore id")
	}
}

func TestRestoreFailureStillRestartsContainer(t *testing.T) {
	svc, runtime, engine, history, _ := newRestoreFixture(t)
	engine.restoreErr = errors.New("snapshot not found")

	rec, err := svc.Start(context.Background(), RestoreRequest{
		StorageID: 1, SnapshotID: "missing", TargetVolume: "pgdata", StopContainer: "c1",
	})
	if err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	final := waitRestoreTerminal(t, history, rec.ID)
	if final.Status != domain.BackupFailed {
		t.Fatalf("Status = %s, want failed", final.Status)
	}
	if len(runtime.started()) != 1 {
		t.Fatal("container must be restarted even when the restore fails")
	}
}

// Le clonage doit transmettre le volume SOURCE au moteur : sans lui,
// --include designerait un chemin absent du snapshot et rien ne serait
// restaure (le bug que la fonctionnalite corrige).
func TestRestoreCloneIntoNewVolumePassesSourceToEngine(t *testing.T) {
	svc, runtime, engine, history, _ := newRestoreFixture(t)

	rec, err := svc.Start(context.Background(), RestoreRequest{
		StorageID: 1, SnapshotID: "snap1",
		SourceVolume: "pgdata", TargetVolume: "pgdata_clone",
	})
	if err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	final := waitRestoreTerminal(t, history, rec.ID)
	if final.Status != domain.BackupSuccess {
		t.Fatalf("Status = %s (%s), want success", final.Status, final.ErrorLog)
	}
	if !final.IsClone() {
		t.Fatalf("record should be flagged as a clone: %+v", final)
	}
	if len(engine.restores) != 1 || engine.restores[0] != "snap1:pgdata->pgdata_clone" {
		t.Fatalf("engine restores = %v, want the source volume forwarded", engine.restores)
	}
	// le conteneur source ne touche pas au volume cible : le laisser tourner
	// est tout l'interet du clonage
	if len(runtime.stopped()) != 0 || len(runtime.started()) != 0 {
		t.Fatalf("clone must not stop/start the source container: %v/%v",
			runtime.stopped(), runtime.started())
	}
}

// Un clone vise un volume different : il ne doit pas entrer en conflit avec
// une restauration en cours sur le volume d'origine.
func TestRestoreCloneDoesNotConflictWithInPlaceRestore(t *testing.T) {
	svc, _, engine, history, _ := newRestoreFixture(t)
	release := make(chan struct{})
	engine.restoreFn = func(ctx context.Context) error {
		<-release
		return nil
	}

	inPlace, err := svc.Start(context.Background(), RestoreRequest{
		StorageID: 1, SnapshotID: "s", TargetVolume: "pgdata",
	})
	if err != nil {
		t.Fatalf("in-place Start() error: %v", err)
	}
	clone, err := svc.Start(context.Background(), RestoreRequest{
		StorageID: 1, SnapshotID: "s", SourceVolume: "pgdata", TargetVolume: "pgdata_clone",
	})
	if err != nil {
		t.Fatalf("clone Start() error: %v, want no conflict on a distinct volume", err)
	}
	close(release)
	waitRestoreTerminal(t, history, inPlace.ID)
	waitRestoreTerminal(t, history, clone.ID)
}

func TestRestoreRejectsInvalidVolumeName(t *testing.T) {
	svc, _, _, _, _ := newRestoreFixture(t)

	for _, name := range []string{"../../etc", "/host/path", "bad name", ""} {
		_, err := svc.Start(context.Background(), RestoreRequest{
			StorageID: 1, SnapshotID: "s", TargetVolume: name,
		})
		if !errors.Is(err, domain.ErrInvalidInput) {
			t.Fatalf("target volume %q: err = %v, want ErrInvalidInput", name, err)
		}
	}
	_, err := svc.Start(context.Background(), RestoreRequest{
		StorageID: 1, SnapshotID: "s", SourceVolume: "../escape", TargetVolume: "ok_clone",
	})
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("source volume escape: err = %v, want ErrInvalidInput", err)
	}
}

func TestRestoreConflictOnSameVolume(t *testing.T) {
	svc, _, engine, history, _ := newRestoreFixture(t)
	release := make(chan struct{})
	// moteur bloquant : garde le premier restore en cours
	engine.restoreFn = func(ctx context.Context) error {
		<-release
		return nil
	}

	first, err := svc.Start(context.Background(), RestoreRequest{StorageID: 1, SnapshotID: "s", TargetVolume: "pgdata"})
	if err != nil {
		t.Fatalf("first Start() error: %v", err)
	}
	_, err = svc.Start(context.Background(), RestoreRequest{StorageID: 1, SnapshotID: "s", TargetVolume: "pgdata"})
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("second Start() err = %v, want ErrConflict", err)
	}
	close(release)
	waitRestoreTerminal(t, history, first.ID)
}
