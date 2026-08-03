package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/standalone-docker-backup/sdb/internal/domain"
)

func newStorageFixture() (*StorageService, *memStorages, *fakeEngine) {
	storages := newMemStorages()
	engine := &fakeEngine{}
	return NewStorageService(storages, engine, discardLogger()), storages, engine
}

func TestStorageCreateGeneratesPasswordAndInitialisesRepo(t *testing.T) {
	ctx := context.Background()
	svc, storages, engine := newStorageFixture()

	cfg := &domain.StorageConfig{Name: "local", Type: domain.StorageLocal, Endpoint: "/backups"}
	if err := svc.Create(ctx, cfg); err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	if len(cfg.ResticPassword) < 32 {
		t.Fatalf("generated restic password too short: %d chars", len(cfg.ResticPassword))
	}
	if engine.ensureCalls != 1 {
		t.Fatalf("EnsureRepository calls = %d, want 1", engine.ensureCalls)
	}
	stored, err := storages.GetByID(ctx, cfg.ID)
	if err != nil || stored.ResticPassword != cfg.ResticPassword {
		t.Fatalf("stored config mismatch: %+v, %v", stored, err)
	}
}

func TestStorageCreateRollsBackWhenRepoInitFails(t *testing.T) {
	ctx := context.Background()
	svc, storages, engine := newStorageFixture()
	engine.ensureErr = errors.New("bucket unreachable")

	cfg := &domain.StorageConfig{Name: "s3", Type: domain.StorageS3, Endpoint: "s3.example.com/b"}
	err := svc.Create(ctx, cfg)
	if !errors.Is(err, engine.ensureErr) {
		t.Fatalf("Create() must surface the repository error, got %v", err)
	}
	if _, err := storages.GetByID(ctx, cfg.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("config must be rolled back, GetByID err = %v", err)
	}
}

func TestStorageUpdatePasswordImmutable(t *testing.T) {
	ctx := context.Background()
	svc, storages, _ := newStorageFixture()

	cfg := &domain.StorageConfig{Name: "local", Type: domain.StorageLocal, Endpoint: "/backups"}
	if err := svc.Create(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	original := cfg.ResticPassword

	// Empty password on update keeps the existing one.
	upd := &domain.StorageConfig{ID: cfg.ID, Name: "local-renamed", Type: domain.StorageLocal, Endpoint: "/backups"}
	if err := svc.Update(ctx, upd); err != nil {
		t.Fatalf("Update(keep password) error: %v", err)
	}
	stored, _ := storages.GetByID(ctx, cfg.ID)
	if stored.ResticPassword != original || stored.Name != "local-renamed" {
		t.Fatalf("update mishandled: %+v", stored)
	}

	// A different password is refused.
	bad := &domain.StorageConfig{ID: cfg.ID, Name: "local", Type: domain.StorageLocal, Endpoint: "/backups", ResticPassword: "new-password"}
	if err := svc.Update(ctx, bad); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("Update(change password) err = %v, want ErrInvalidInput", err)
	}
}

// Une copie secondaire s'initialise DEPUIS sa source, pour hériter de ses
// paramètres de découpage : c'est irrattrapable après création, un dépôt les
// garde pour toujours.
func TestStorageCreateInitialisesCopyFromItsSource(t *testing.T) {
	ctx := context.Background()
	svc, _, engine := newStorageFixture()

	primary := &domain.StorageConfig{Name: "primary", Type: domain.StorageLocal, Endpoint: "/srv/primary"}
	if err := svc.Create(ctx, primary); err != nil {
		t.Fatal(err)
	}
	copyCfg := &domain.StorageConfig{
		Name: "offsite", Type: domain.StorageLocal, Endpoint: "/srv/offsite", CopyOf: primary.ID,
	}
	if err := svc.Create(ctx, copyCfg); err != nil {
		t.Fatalf("Create(copy) error: %v", err)
	}
	if engine.ensureCopies != 1 {
		t.Fatalf("EnsureCopyTarget calls = %d, want 1 (sinon les paramètres de découpage ne sont pas hérités)", engine.ensureCopies)
	}
}

// Une chaîne source → copie → copie rendrait le retard d'un maillon invisible
// derrière celui du suivant.
func TestStorageRefusesCopyOfCopy(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newStorageFixture()

	primary := &domain.StorageConfig{Name: "primary", Type: domain.StorageLocal, Endpoint: "/srv/primary"}
	if err := svc.Create(ctx, primary); err != nil {
		t.Fatal(err)
	}
	first := &domain.StorageConfig{Name: "offsite", Type: domain.StorageLocal, Endpoint: "/srv/offsite", CopyOf: primary.ID}
	if err := svc.Create(ctx, first); err != nil {
		t.Fatal(err)
	}

	chained := &domain.StorageConfig{Name: "third", Type: domain.StorageLocal, Endpoint: "/srv/third", CopyOf: first.ID}
	if err := svc.Create(ctx, chained); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("Create(copy of a copy) err = %v, want ErrInvalidInput", err)
	}
}

func TestStorageRefusesCopyOfMissingSource(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newStorageFixture()

	cfg := &domain.StorageConfig{Name: "offsite", Type: domain.StorageLocal, Endpoint: "/srv/offsite", CopyOf: 404}
	if err := svc.Create(ctx, cfg); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Create() err = %v, want ErrNotFound", err)
	}
}

// Rebrancher une copie sur une autre source mélangerait deux origines dans un
// même dépôt ; la détacher laisserait une copie que plus rien ne réconcilie,
// en la faisant passer pour un dépôt principal sans sauvegarde.
func TestStorageCopySourceIsImmutable(t *testing.T) {
	ctx := context.Background()
	svc, storages, _ := newStorageFixture()

	primary := &domain.StorageConfig{Name: "primary", Type: domain.StorageLocal, Endpoint: "/srv/primary"}
	other := &domain.StorageConfig{Name: "other", Type: domain.StorageLocal, Endpoint: "/srv/other"}
	for _, cfg := range []*domain.StorageConfig{primary, other} {
		if err := svc.Create(ctx, cfg); err != nil {
			t.Fatal(err)
		}
	}
	copyCfg := &domain.StorageConfig{Name: "offsite", Type: domain.StorageLocal, Endpoint: "/srv/offsite", CopyOf: primary.ID}
	if err := svc.Create(ctx, copyCfg); err != nil {
		t.Fatal(err)
	}

	rebranch := &domain.StorageConfig{
		ID: copyCfg.ID, Name: "offsite", Type: domain.StorageLocal, Endpoint: "/srv/offsite", CopyOf: other.ID,
	}
	if err := svc.Update(ctx, rebranch); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("Update(rebranch) err = %v, want ErrInvalidInput", err)
	}

	// une mise à jour ordinaire n'a pas à répéter le rattachement : 0 = inchangé
	rename := &domain.StorageConfig{
		ID: copyCfg.ID, Name: "offsite-renamed", Type: domain.StorageLocal, Endpoint: "/srv/offsite",
	}
	if err := svc.Update(ctx, rename); err != nil {
		t.Fatalf("Update(rename) error: %v", err)
	}
	stored, _ := storages.GetByID(ctx, copyCfg.ID)
	if stored.CopyOf != primary.ID {
		t.Fatalf("CopyOf = %d after a rename, want %d", stored.CopyOf, primary.ID)
	}
}

func TestMaintenanceRunChecksContinuesOnFailure(t *testing.T) {
	ctx := context.Background()
	storages := newMemStorages()
	for _, name := range []string{"a", "b"} {
		if err := storages.Create(ctx, &domain.StorageConfig{
			Name: name, Type: domain.StorageLocal, Endpoint: "/b", ResticPassword: "pw",
		}); err != nil {
			t.Fatal(err)
		}
	}
	engine := &fakeEngine{checkErr: errors.New("pack corrupted")}
	svc := NewMaintenanceService(storages, engine, discardLogger())

	err := svc.RunChecks(ctx)
	if err == nil {
		t.Fatal("RunChecks() must report failures")
	}
	if engine.checkCalls != 2 {
		t.Fatalf("check calls = %d, want 2 (one failure must not stop the sweep)", engine.checkCalls)
	}
}
