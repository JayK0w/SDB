package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/standalone-docker-backup/sdb/internal/domain"
)

func appendOnlyFixture(t *testing.T) (*BackupService, *fakeEngine, *memHistory, *memStorages) {
	t.Helper()
	runtime := &fakeRuntime{container: &domain.Container{
		ID: "c1", Name: "postgres", State: domain.ContainerRunning,
		Mounts: []domain.Mount{
			{Type: domain.MountVolume, Name: "pgdata", Source: "pgdata", Destination: "/var/lib/postgresql/data"},
		},
	}}
	storages := newMemStorages()
	if err := storages.Create(context.Background(), &domain.StorageConfig{
		Name: "vault", Type: domain.StorageLocal, Endpoint: "/backups",
		ResticPassword: "pw", AppendOnly: true,
	}); err != nil {
		t.Fatal(err)
	}
	engine := &fakeEngine{summary: &domain.BackupSummary{SnapshotID: "snap1"}}
	history := newMemHistory()
	svc := NewBackupService(runtime, engine, storages, history, &capturePublisher{}, discardLogger())
	return svc, engine, history, storages
}

// Un dépôt append-only ne subit jamais forget/prune : c'est le garde-fou qui
// empêche une politique de rétention mal réglée — ou un SDB compromis —
// d'effacer les sauvegardes qu'il est censé protéger.
func TestBackupSkipsRetentionOnAppendOnlyStorage(t *testing.T) {
	svc, engine, history, _ := appendOnlyFixture(t)

	rec, err := svc.Start(context.Background(), domain.BackupRequest{
		ContainerID: "c1", StorageID: 1,
		Retention: &domain.RetentionPolicy{KeepLast: 1, Prune: true},
	})
	if err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	final := waitTerminal(t, history, rec.ID)
	if !final.Status.Terminal() {
		t.Fatalf("run did not settle: %s", final.Status)
	}
	if got := engine.forgotten(); len(got) != 0 {
		t.Fatalf("forget ran on an append-only storage: %v", got)
	}
}

// À l'inverse, un dépôt normal applique bien sa rétention : le garde-fou ne
// doit pas désactiver la fonctionnalité pour tout le monde.
func TestBackupAppliesRetentionOnMutableStorage(t *testing.T) {
	svc, _, engine, history, _ := newBackupFixture(t)

	rec, err := svc.Start(context.Background(), domain.BackupRequest{
		ContainerID: "c1", StorageID: 1,
		Retention: &domain.RetentionPolicy{KeepLast: 3},
	})
	if err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	waitTerminal(t, history, rec.ID)
	if got := engine.forgotten(); len(got) != 1 {
		t.Fatalf("retention should have run once, got %v", got)
	}
}

func TestStorageDeleteRefusedOnAppendOnly(t *testing.T) {
	storages := newMemStorages()
	ctx := context.Background()
	if err := storages.Create(ctx, &domain.StorageConfig{
		Name: "vault", Type: domain.StorageLocal, Endpoint: "/b",
		ResticPassword: "pw", AppendOnly: true,
	}); err != nil {
		t.Fatal(err)
	}
	svc := NewStorageService(storages, &fakeEngine{}, discardLogger())

	if err := svc.Delete(ctx, 1); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("Delete() err = %v, want ErrForbidden", err)
	}
	if _, err := storages.GetByID(ctx, 1); err != nil {
		t.Fatalf("storage must still exist after a refused delete: %v", err)
	}
}

// Cliquet : un admin compromis ne doit pas pouvoir lever la protection puis
// purger le dépôt.
func TestAppendOnlyCannotBeDisabledThroughTheAPI(t *testing.T) {
	storages := newMemStorages()
	ctx := context.Background()
	if err := storages.Create(ctx, &domain.StorageConfig{
		Name: "vault", Type: domain.StorageLocal, Endpoint: "/b",
		ResticPassword: "pw", AppendOnly: true,
	}); err != nil {
		t.Fatal(err)
	}
	svc := NewStorageService(storages, &fakeEngine{}, discardLogger())

	err := svc.Update(ctx, &domain.StorageConfig{
		ID: 1, Name: "vault", Type: domain.StorageLocal, Endpoint: "/b", AppendOnly: false,
	})
	if !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("Update() err = %v, want ErrForbidden", err)
	}
	after, err := storages.GetByID(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !after.AppendOnly {
		t.Fatal("append-only was cleared despite the refusal")
	}
}

// Le sens inverse doit rester possible : on peut toujours durcir un dépôt.
func TestAppendOnlyCanBeEnabled(t *testing.T) {
	storages := newMemStorages()
	ctx := context.Background()
	if err := storages.Create(ctx, &domain.StorageConfig{
		Name: "vault", Type: domain.StorageLocal, Endpoint: "/b", ResticPassword: "pw",
	}); err != nil {
		t.Fatal(err)
	}
	svc := NewStorageService(storages, &fakeEngine{}, discardLogger())

	if err := svc.Update(ctx, &domain.StorageConfig{
		ID: 1, Name: "vault", Type: domain.StorageLocal, Endpoint: "/b", AppendOnly: true,
	}); err != nil {
		t.Fatalf("Update() error: %v", err)
	}
	after, _ := storages.GetByID(ctx, 1)
	if !after.AppendOnly {
		t.Fatal("append-only was not enabled")
	}
}

// L'attribution doit traverser tout le pipeline jusqu'à l'historique, sinon
// l'audit ne peut pas nommer l'auteur d'une opération destructrice.
func TestBackupRecordsItsActor(t *testing.T) {
	svc, _, _, history, _ := newBackupFixture(t)
	actor := domain.Actor{UserID: 3, Name: "bob"}

	rec, err := svc.Start(context.Background(), domain.BackupRequest{
		ContainerID: "c1", StorageID: 1, TriggeredBy: actor,
	})
	if err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	final := waitTerminal(t, history, rec.ID)
	if final.TriggeredBy != actor {
		t.Fatalf("TriggeredBy = %+v, want %+v", final.TriggeredBy, actor)
	}
}

func TestRestoreRecordsItsActor(t *testing.T) {
	svc, _, _, history, _ := newRestoreFixture(t)
	actor := domain.Actor{UserID: 9, Name: "carol"}

	rec, err := svc.Start(context.Background(), RestoreRequest{
		StorageID: 1, SnapshotID: "s", TargetVolume: "pgdata", TriggeredBy: actor,
	})
	if err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	final := waitRestoreTerminal(t, history, rec.ID)
	if final.TriggeredBy != actor {
		t.Fatalf("TriggeredBy = %+v, want %+v", final.TriggeredBy, actor)
	}
}

// Un run planifié n'a pas d'humain : il doit être attribué au système, et se
// distinguer sans ambiguïté d'un déclenchement manuel.
func TestScheduledBackupIsAttributedToTheSystem(t *testing.T) {
	sched := domain.BackupSchedule{Name: "nightly", ContainerName: "postgres", StorageID: 1}
	req := sched.ToRequest()

	if req.TriggeredBy.UserID != 0 {
		t.Fatalf("a scheduled run must not claim a user id, got %d", req.TriggeredBy.UserID)
	}
	if req.TriggeredBy.Name != "system:schedule:nightly" {
		t.Fatalf("TriggeredBy = %q, want the schedule-qualified system actor", req.TriggeredBy.Name)
	}
}
